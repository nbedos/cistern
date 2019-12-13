package providers

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
	"github.com/xanzy/go-gitlab"
)

type GitLabClient struct {
	provider             cache.Provider
	remote               *gitlab.Client
	rateLimiter          <-chan time.Time
	updateTimePerBuildID map[string]time.Time
	mux                  *sync.Mutex
}

func NewGitLabClient(id string, name string, token string, rateLimit time.Duration) GitLabClient {
	return GitLabClient{
		provider: cache.Provider{
			ID:   id,
			Name: name,
		},
		remote:               gitlab.NewClient(nil, token),
		rateLimiter:          time.Tick(rateLimit),
		updateTimePerBuildID: make(map[string]time.Time),
		mux:                  &sync.Mutex{},
	}
}

func (c GitLabClient) Commit(ctx context.Context, repo string, ref string) (cache.Commit, error) {
	slug, err := c.parseRepositoryURL(repo)
	if err != nil {
		return cache.Commit{}, cache.ErrUnknownRepositoryURL
	}

	ref = url.PathEscape(ref)

	gitlabCommit, _, err := c.remote.Commits.GetCommit(slug, url.PathEscape(ref))
	if err != nil {
		return cache.Commit{}, err
	}

	commit := cache.Commit{
		Sha:     gitlabCommit.ID,
		Author:  fmt.Sprintf("%s <%s>", gitlabCommit.AuthorName, gitlabCommit.AuthorEmail),
		Date:    *gitlabCommit.AuthoredDate,
		Message: gitlabCommit.Message,
	}

	opt := gitlab.GetCommitRefsOptions{}
	for {
		refs, resp, err := c.remote.Commits.GetCommitRefs(slug, commit.Sha, &opt)
		if err != nil {
			return cache.Commit{}, err
		}

		for _, ref := range refs {
			switch ref.Type {
			case "tag":
				commit.Tags = append(commit.Tags, ref.Name)
			case "branch":
				commit.Branches = append(commit.Branches, ref.Name)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return commit, nil
}

func (c GitLabClient) buildURLsPipelines(ctx context.Context, slug string, sha string) ([]string, error) {
	options := gitlab.ListProjectPipelinesOptions{
		SHA: &sha,
	}
	urls := make([]string, 0)
	for {
		select {
		case <-c.rateLimiter:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		pipelines, resp, err := c.remote.Pipelines.ListProjectPipelines(slug, &options)
		if err != nil {
			if err, ok := err.(*gitlab.ErrorResponse); ok && err.Response.StatusCode == 404 {
				return nil, cache.ErrUnknownRepositoryURL
			}
			return nil, err
		}

		for _, pipeline := range pipelines {
			urls = append(urls, pipeline.WebURL)
		}

		if resp.NextPage == 0 {
			break
		}
		options.Page = resp.NextPage
	}

	return urls, nil
}

func (c GitLabClient) buildURLsStatuses(ctx context.Context, slug string, sha string) ([]string, error) {
	options := gitlab.GetCommitStatusesOptions{}
	urls := make([]string, 0)
	for {
		select {
		case <-c.rateLimiter:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		statuses, resp, err := c.remote.Commits.GetCommitStatuses(slug, url.PathEscape(sha), &options)
		if err != nil {
			return nil, err
		}

		for _, status := range statuses {
			if status.TargetURL != "" {
				urls = append(urls, status.TargetURL)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		options.Page = resp.NextPage
	}

	return urls, nil
}

func (c GitLabClient) RefStatuses(ctx context.Context, url string, ref string) ([]string, error) {
	slug, err := c.parseRepositoryURL(url)
	if err != nil {
		return nil, err
	}

	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	var statusURLs []string
	go func() {
		var err error
		statusURLs, err = c.buildURLsStatuses(ctx, slug, ref)
		errc <- err
	}()

	var pipelineURLs []string
	go func() {
		var err error
		pipelineURLs, err = c.buildURLsPipelines(ctx, slug, ref)
		errc <- err
	}()

	for i := 0; i < 2; i++ {
		if e := <-errc; e != nil && err == nil {
			cancel()
			err = e
		}
	}
	urls := append(pipelineURLs, statusURLs...)

	return urls, nil
}

func (c GitLabClient) ID() string {
	return c.provider.ID
}

func (c GitLabClient) Name() string {
	return c.provider.Name
}

func (c GitLabClient) BuildFromURL(ctx context.Context, u string) (cache.Pipeline, error) {
	slug, id, err := c.parsePipelineURL(u)
	if err != nil {
		return cache.Pipeline{}, err
	}

	repository, err := c.Repository(ctx, slug)
	if err != nil {
		return cache.Pipeline{}, err
	}

	return c.fetchPipeline(ctx, &repository, id)
}

func (c GitLabClient) parseRepositoryURL(u string) (string, error) {
	host, owner, repo, err := utils.RepoHostOwnerAndName(u)
	if err != nil || host != c.remote.BaseURL().Hostname() {
		return "", cache.ErrUnknownRepositoryURL
	}

	slug := fmt.Sprintf("%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	return slug, nil
}

func (c GitLabClient) parsePipelineURL(u string) (string, int, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", 0, err
	}

	if v.Hostname() != c.remote.BaseURL().Hostname() {
		return "", 0, cache.ErrUnknownPipelineURL
	}

	// URL format: https://gitlab.com/nbedos/citop/pipelines/97604657
	pathComponents := strings.FieldsFunc(v.EscapedPath(), func(c rune) bool { return c == '/' })
	if len(pathComponents) < 4 || pathComponents[2] != "pipelines" {
		return "", 0, cache.ErrUnknownPipelineURL
	}

	slug := fmt.Sprintf("%s/%s", pathComponents[0], pathComponents[1])
	id, err := strconv.Atoi(pathComponents[3])
	if err != nil {
		return "", 0, err
	}
	return slug, id, nil
}

func (c *GitLabClient) GetTraceFile(ctx context.Context, repositorySlug string, jobID int) (bytes.Buffer, error) {
	buf := bytes.Buffer{}
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return buf, ctx.Err()
	}
	trace, _, err := c.remote.Jobs.GetTraceFile(repositorySlug, jobID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return buf, err
	}

	_, err = buf.ReadFrom(trace)
	return buf, err
}

func (c GitLabClient) Repository(ctx context.Context, slug string) (cache.Repository, error) {
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return cache.Repository{}, ctx.Err()
	}
	project, _, err := c.remote.Projects.GetProject(slug, nil, gitlab.WithContext(ctx))
	if err != nil {
		if err, ok := err.(*gitlab.ErrorResponse); ok && err.Response.StatusCode == 404 {
			return cache.Repository{}, cache.ErrUnknownRepositoryURL
		}
		return cache.Repository{}, err
	}

	splitPath := strings.SplitN(project.PathWithNamespace, "/", 2)
	if len(splitPath) != 2 {
		return cache.Repository{}, fmt.Errorf("invalid repository path: %q", project.PathWithNamespace)
	}

	return cache.Repository{
		Owner: splitPath[0],
		Name:  splitPath[1],
		URL:   project.WebURL,
	}, nil
}

func FromGitLabState(s string) cache.State {
	switch strings.ToLower(s) {
	case "created", "pending":
		return cache.Pending
	case "running":
		return cache.Running
	case "canceled":
		return cache.Canceled
	case "success", "passed":
		return cache.Passed
	case "failed":
		return cache.Failed
	case "skipped":
		return cache.Skipped
	case "manual":
		return cache.Manual
	default:
		return cache.Unknown
	}
}

func (c GitLabClient) GetJob(ctx context.Context, repositoryID int, jobID int) (*gitlab.Job, *gitlab.Response, error) {
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	return c.remote.Jobs.GetJob(repositoryID, jobID, gitlab.WithContext(ctx))
}

func (c GitLabClient) Log(ctx context.Context, repository cache.Repository, jobID string) (string, error) {
	id, err := strconv.Atoi(jobID)
	if err != nil {
		return "", err
	}

	buf, err := c.GetTraceFile(ctx, repository.Slug(), id)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (c GitLabClient) fetchPipeline(ctx context.Context, repository *cache.Repository, pipelineID int) (pipeline cache.Pipeline, err error) {
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return pipeline, ctx.Err()
	}
	gitlabPipeline, _, err := c.remote.Pipelines.GetPipeline(repository.Slug(), pipelineID, gitlab.WithContext(ctx))
	if err != nil {
		return pipeline, err
	}

	if gitlabPipeline.UpdatedAt == nil {
		return pipeline, fmt.Errorf("missing UpdatedAt data for pipeline #%d", gitlabPipeline.ID)
	}
	pipeline = cache.Pipeline{
		Repository: repository,
		GitReference: cache.GitReference{
			SHA:   gitlabPipeline.SHA,
			Ref:   gitlabPipeline.Ref,
			IsTag: gitlabPipeline.Tag,
		},
		Step: cache.Step{
			ID:         strconv.Itoa(gitlabPipeline.ID),
			Type:       cache.StepPipeline,
			State:      FromGitLabState(gitlabPipeline.Status),
			CreatedAt:  utils.NullTimeFromTime(gitlabPipeline.CreatedAt),
			StartedAt:  utils.NullTimeFromTime(gitlabPipeline.StartedAt),
			FinishedAt: utils.NullTimeFromTime(gitlabPipeline.FinishedAt),
			UpdatedAt:  *gitlabPipeline.UpdatedAt,
			Duration: utils.NullDuration{
				Duration: time.Duration(gitlabPipeline.Duration) * time.Second,
				Valid:    gitlabPipeline.Duration > 0,
			},
			WebURL: utils.NullString{
				String: gitlabPipeline.WebURL,
				Valid:  true,
			},
		},
	}

	jobs := make([]*gitlab.Job, 0)
	options := gitlab.ListJobsOptions{}
	for {
		select {
		case <-c.rateLimiter:
		case <-ctx.Done():
			return pipeline, ctx.Err()
		}
		pageJobs, resp, err := c.remote.Jobs.ListPipelineJobs(repository.Slug(), gitlabPipeline.ID, &options, gitlab.WithContext(ctx))
		if err != nil {
			return pipeline, nil
		}
		jobs = append(jobs, pageJobs...)

		if resp.NextPage == 0 {
			break
		}
		options.Page = resp.NextPage
	}

	stagesIndexByName := make(map[string]int)
	for _, job := range jobs {
		if _, exists := stagesIndexByName[job.Stage]; !exists {
			stage := cache.Step{
				ID:   strconv.Itoa(len(stagesIndexByName) + 1),
				Type: cache.StepStage,
				Name: job.Stage,
			}
			stagesIndexByName[job.Stage] = len(pipeline.Children)
			pipeline.Children = append(pipeline.Children, &stage)
		}
	}

	for _, gitlabJob := range jobs {
		job := cache.Step{
			ID:         strconv.Itoa(gitlabJob.ID),
			Type:       cache.StepJob,
			State:      FromGitLabState(gitlabJob.Status),
			Name:       gitlabJob.Name,
			Log:        utils.NullString{},
			CreatedAt:  utils.NullTimeFromTime(gitlabJob.CreatedAt),
			StartedAt:  utils.NullTimeFromTime(gitlabJob.StartedAt),
			FinishedAt: utils.NullTimeFromTime(gitlabJob.FinishedAt),
			Duration: utils.NullDuration{
				Duration: time.Duration(gitlabJob.Duration) * time.Second,
				Valid:    int64(gitlabJob.Duration) > 0,
			},
			WebURL: utils.NullString{
				String: gitlabJob.WebURL,
				Valid:  true,
			},
			AllowFailure: gitlabJob.AllowFailure,
		}
		index := stagesIndexByName[gitlabJob.Stage]
		pipeline.Children[index].Children = append(pipeline.Children[index].Children, &job)
	}

	// Compute stage state
	for _, stage := range pipeline.Children {
		// Each stage contains all job runs. Select only the last run of each job
		// Earliest runs should not influence the current state of the stage
		jobsByName := make(map[string]*cache.Step)
		for _, job := range stage.Children {
			previousJob, exists := jobsByName[job.Name]
			// Dates may be NULL so we have to rely on IDs to find out which job is older. meh.
			if !exists || previousJob.ID < job.ID {
				jobsByName[job.Name] = job
			}
		}
		jobs := make([]*cache.Step, 0, len(jobsByName))
		for _, job := range jobsByName {
			jobs = append(jobs, job)
		}
		stage.State = cache.AggregateStatuses(jobs)
	}

	c.mux.Lock()
	c.updateTimePerBuildID[pipeline.ID] = pipeline.UpdatedAt
	c.mux.Unlock()
	return pipeline, nil
}
