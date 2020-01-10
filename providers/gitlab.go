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

	"github.com/nbedos/cistern/utils"
	"github.com/xanzy/go-gitlab"
)

type GitLabClient struct {
	provider    Provider
	remote      *gitlab.Client
	rateLimiter <-chan time.Time
}

const gitLabCom = "https://gitlab.com"

func NewGitLabClient(id string, name string, baseURL string, token string, requestsPerSecond float64) (GitLabClient, error) {
	remote := gitlab.NewClient(nil, token)
	if baseURL == "" {
		baseURL = gitLabCom
	}

	if err := remote.SetBaseURL(baseURL); err != nil {
		return GitLabClient{}, err
	}

	rateLimit := time.Second / 10
	if requestsPerSecond > 0 {
		rateLimit = time.Second / time.Duration(requestsPerSecond)
	}

	return GitLabClient{
		provider: Provider{
			ID:   id,
			Name: name,
		},
		remote:      remote,
		rateLimiter: time.Tick(rateLimit),
	}, nil
}

func (c GitLabClient) Commit(ctx context.Context, repo string, ref string) (Commit, error) {
	slug, err := c.parseRepositoryURL(repo)
	if err != nil {
		return Commit{}, ErrUnknownRepositoryURL
	}

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return Commit{}, ctx.Err()
	}
	gitlabCommit, _, err := c.remote.Commits.GetCommit(slug, ref, gitlab.WithContext(ctx))
	if err != nil {
		if err, ok := err.(*gitlab.ErrorResponse); ok {
			switch err.Response.StatusCode {
			case 401, 404:
				return Commit{}, ErrUnknownGitReference
			}
		}
		return Commit{}, err
	}

	commit := Commit{
		Sha:     gitlabCommit.ID,
		Author:  fmt.Sprintf("%s <%s>", gitlabCommit.AuthorName, gitlabCommit.AuthorEmail),
		Date:    *gitlabCommit.AuthoredDate,
		Message: gitlabCommit.Message,
	}

	opt := gitlab.GetCommitRefsOptions{}
	for {
		select {
		case <-c.rateLimiter:
		case <-ctx.Done():
			return Commit{}, ctx.Err()
		}
		refs, resp, err := c.remote.Commits.GetCommitRefs(slug, commit.Sha, &opt, gitlab.WithContext(ctx))
		if err != nil {
			return Commit{}, err
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
				return nil, ErrUnknownRepositoryURL
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
		statuses, resp, err := c.remote.Commits.GetCommitStatuses(slug, sha, &options, gitlab.WithContext(ctx))
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

func (c GitLabClient) RefStatuses(ctx context.Context, url string, ref string, sha string) ([]string, error) {
	slug, err := c.parseRepositoryURL(url)
	if err != nil {
		return nil, err
	}

	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	var statusURLs []string
	go func() {
		var err error
		statusURLs, err = c.buildURLsStatuses(ctx, slug, sha)
		errc <- err
	}()

	var pipelineURLs []string
	go func() {
		var err error
		pipelineURLs, err = c.buildURLsPipelines(ctx, slug, sha)
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

func (c GitLabClient) Host() string {
	return c.remote.BaseURL().Host
}

func (c GitLabClient) Name() string {
	return c.provider.Name
}

func (c GitLabClient) BuildFromURL(ctx context.Context, u string) (Pipeline, error) {
	slug, id, err := c.parsePipelineURL(u)
	if err != nil {
		return Pipeline{}, err
	}

	return c.fetchPipeline(ctx, slug, id)
}

func (c GitLabClient) parseRepositoryURL(u string) (string, error) {
	host, slug, err := utils.RepositoryHostAndSlug(u)
	if err != nil || host != c.remote.BaseURL().Hostname() {
		return "", ErrUnknownRepositoryURL
	}

	return slug, nil
}

func (c GitLabClient) parsePipelineURL(u string) (string, int, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", 0, err
	}

	if v.Hostname() != c.remote.BaseURL().Hostname() {
		return "", 0, ErrUnknownPipelineURL
	}

	// URL format:
	//    https://gitlab.com/nbedos/cistern/pipelines/97604657
	// OR https://gitlab.com/namespace/nbedos/cistern/pipelines/97604657
	pathComponents := strings.FieldsFunc(v.EscapedPath(), func(c rune) bool { return c == '/' })
	if len(pathComponents) < 4 || pathComponents[len(pathComponents)-2] != "pipelines" {
		return "", 0, ErrUnknownPipelineURL
	}

	slug := strings.Join(pathComponents[:len(pathComponents)-2], "/")
	id, err := strconv.Atoi(pathComponents[len(pathComponents)-1])
	if err != nil {
		return "", 0, err
	}
	return slug, id, nil
}

func (c *GitLabClient) getTraceFile(ctx context.Context, repositorySlug string, jobID int) (bytes.Buffer, error) {
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

func fromGitLabState(s string) State {
	switch strings.ToLower(s) {
	case "created", "pending":
		return Pending
	case "running":
		return Running
	case "canceled":
		return Canceled
	case "success", "passed":
		return Passed
	case "failed":
		return Failed
	case "skipped":
		return Skipped
	case "manual":
		return Manual
	default:
		return Unknown
	}
}

func (c GitLabClient) Log(ctx context.Context, step Step) (string, error) {
	if step.Log.Key == "" {
		return "", ErrNoLogHere
	}
	id, err := strconv.Atoi(step.ID)
	if err != nil {
		return "", err
	}

	buf, err := c.getTraceFile(ctx, step.Log.Key, id)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (c GitLabClient) fetchJobs(ctx context.Context, slug string, pipelineID int) ([]*gitlab.Job, error) {
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	jobs, resp, err := c.remote.Jobs.ListPipelineJobs(slug, pipelineID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return nil, nil
	}

	wg := sync.WaitGroup{}
	errc := make(chan error)
	pageCtx, cancel := context.WithCancel(ctx)
	allPages := make([][]*gitlab.Job, resp.TotalPages)
	if len(allPages) > 0 {
		allPages[0] = jobs
	}
	for page := 2; page <= resp.TotalPages; page++ {
		wg.Add(1)
		go func(page int) {
			defer wg.Done()
			select {
			case <-c.rateLimiter:
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}
			options := gitlab.ListJobsOptions{
				ListOptions: gitlab.ListOptions{
					Page: page,
				},
			}
			jobs, _, err := c.remote.Jobs.ListPipelineJobs(slug, pipelineID, &options, gitlab.WithContext(pageCtx))
			if err != nil {
				errc <- err
				return
			}
			allPages[page-1] = jobs
		}(page)
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	for e := range errc {
		if err == nil && e != nil {
			err = e
			cancel()
		}
	}

	allJobs := make([]*gitlab.Job, 0)
	for _, pageJobs := range allPages {
		allJobs = append(allJobs, pageJobs...)
	}

	return allJobs, err
}

func (c GitLabClient) fetchPipeline(ctx context.Context, slug string, pipelineID int) (pipeline Pipeline, err error) {
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return pipeline, ctx.Err()
	}
	gitlabPipeline, _, err := c.remote.Pipelines.GetPipeline(slug, pipelineID, gitlab.WithContext(ctx))
	if err != nil {
		return pipeline, err
	}

	if gitlabPipeline.UpdatedAt == nil {
		return pipeline, fmt.Errorf("missing UpdatedAt date for pipeline #%d", gitlabPipeline.ID)
	}

	pipeline = Pipeline{
		GitReference: GitReference{
			SHA:   gitlabPipeline.SHA,
			Ref:   gitlabPipeline.Ref,
			IsTag: gitlabPipeline.Tag,
		},
		Step: Step{
			ID:         strconv.Itoa(gitlabPipeline.ID),
			Type:       StepPipeline,
			State:      fromGitLabState(gitlabPipeline.Status),
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

	if gitlabPipeline.CreatedAt == nil {
		return pipeline, fmt.Errorf("missing CreatedAt date for pipeline #%d", gitlabPipeline.ID)
	}

	pipeline.Step.CreatedAt = *gitlabPipeline.CreatedAt

	jobs, err := c.fetchJobs(ctx, slug, gitlabPipeline.ID)
	if err != nil {
		return Pipeline{}, err
	}

	stagesIndexByName := make(map[string]int)
	for _, job := range jobs {
		if _, exists := stagesIndexByName[job.Stage]; !exists {
			stage := Step{
				ID:   strconv.Itoa(len(stagesIndexByName) + 1),
				Type: StepStage,
				Name: job.Stage,
			}
			stagesIndexByName[job.Stage] = len(pipeline.Children)
			pipeline.Children = append(pipeline.Children, stage)
		}
	}

	for _, gitlabJob := range jobs {
		job := Step{
			ID:    strconv.Itoa(gitlabJob.ID),
			Type:  StepJob,
			State: fromGitLabState(gitlabJob.Status),
			Name:  gitlabJob.Name,
			Log: Log{
				Key: slug,
			},
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
		if gitlabJob.CreatedAt == nil {
			return pipeline, fmt.Errorf("missing CreatedAt date for job #%d", gitlabJob.ID)
		}
		job.CreatedAt = *gitlabJob.CreatedAt

		index := stagesIndexByName[gitlabJob.Stage]
		pipeline.Children[index].Children = append(pipeline.Children[index].Children, job)
	}

	// Compute stage state
	for i, stage := range pipeline.Children {
		// Each stage contains all job runs. Select only the last run of each job
		// Earliest runs should not influence the current state of the stage
		jobsByName := make(map[string]Step)
		for _, job := range stage.Children {
			previousJob, exists := jobsByName[job.Name]
			// Dates may be NULL so we have to rely on IDs to find out which job is older. meh.
			if !exists || previousJob.ID < job.ID {
				jobsByName[job.Name] = job
			}
		}
		jobs := make([]Step, 0, len(jobsByName))
		for _, job := range jobsByName {
			jobs = append(jobs, job)
		}

		aggregate := Aggregate(jobs)
		pipeline.Children[i].State = aggregate.State
		pipeline.Children[i].StartedAt = aggregate.StartedAt
		pipeline.Children[i].CreatedAt = aggregate.CreatedAt
		pipeline.Children[i].FinishedAt = aggregate.FinishedAt
		pipeline.Children[i].UpdatedAt = aggregate.UpdatedAt
		pipeline.Children[i].Duration = aggregate.Duration
		pipeline.Children[i].WebURL = pipeline.WebURL
	}

	return pipeline, nil
}
