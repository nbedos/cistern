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
	accountID            string
	remote               *gitlab.Client
	rateLimiter          <-chan time.Time
	updateTimePerBuildID map[string]time.Time
	mux                  *sync.Mutex
}

func NewGitLabClient(accountID string, token string, rateLimit time.Duration) GitLabClient {
	return GitLabClient{
		accountID:            accountID,
		remote:               gitlab.NewClient(nil, token),
		rateLimiter:          time.Tick(rateLimit),
		updateTimePerBuildID: make(map[string]time.Time),
		mux:                  &sync.Mutex{},
	}
}

func (c GitLabClient) AccountID() string {
	return c.accountID
}

func (c GitLabClient) BuildFromURL(ctx context.Context, u string) (cache.Build, error) {
	owner, repo, id, err := parseGitlabWebURL(c.remote.BaseURL(), u)
	if err != nil {
		return cache.Build{}, err
	}

	repository, err := c.Repository(ctx, fmt.Sprintf("%s/%s", owner, repo))
	if err != nil {
		return cache.Build{}, err
	}

	return c.fetchBuild(ctx, &repository, id)
}

// Extract owner, repository and build ID from web URL of build
func parseGitlabWebURL(baseURL *url.URL, u string) (string, string, int, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", "", 0, err
	}

	if v.Hostname() != strings.TrimPrefix(baseURL.Hostname(), "api.") {
		return "", "", 0, cache.ErrUnknownURL
	}

	// URL format: https://gitlab.com/nbedos/citop/pipelines/97604657
	cs := strings.Split(v.EscapedPath(), "/")
	if len(cs) < 5 || cs[3] != "pipelines" {
		return "", "", 0, cache.ErrUnknownURL
	}

	owner, repo := cs[1], cs[2]
	id, err := strconv.Atoi(cs[4])
	if err != nil {
		return "", "", 0, err
	}
	return owner, repo, id, nil
}

func (c *GitLabClient) GetTraceFile(ctx context.Context, repositoryID int, jobID int) (bytes.Buffer, error) {
	buf := bytes.Buffer{}
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return buf, ctx.Err()
	}
	trace, _, err := c.remote.Jobs.GetTraceFile(repositoryID, jobID, nil, gitlab.WithContext(ctx))
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
			return cache.Repository{}, cache.ErrRepositoryNotFound
		}
		return cache.Repository{}, err
	}

	splitPath := strings.SplitN(project.PathWithNamespace, "/", 2)
	if len(splitPath) != 2 {
		return cache.Repository{}, fmt.Errorf("invalid repository path: %q", project.PathWithNamespace)
	}

	return cache.Repository{
		ID:        project.ID,
		Owner:     splitPath[0],
		Name:      splitPath[1],
		URL:       project.WebURL,
		AccountID: c.accountID,
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
	buf, err := c.GetTraceFile(ctx, repository.ID, id)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (c GitLabClient) fetchBuild(ctx context.Context, repository *cache.Repository, pipelineID int) (build cache.Build, err error) {
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return build, ctx.Err()
	}
	pipeline, _, err := c.remote.Pipelines.GetPipeline(repository.ID, pipelineID, gitlab.WithContext(ctx))
	if err != nil {
		return build, err
	}

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return build, ctx.Err()
	}
	commit, _, err := c.remote.Commits.GetCommit(repository.ID, pipeline.SHA, gitlab.WithContext(ctx))
	if err != nil {
		return build, err
	}

	cacheCommit := cache.Commit{
		Sha:     commit.ID,
		Message: commit.Message,
		Date:    utils.NullTimeFromTime(commit.AuthoredDate),
	}

	if pipeline.UpdatedAt == nil {
		return build, fmt.Errorf("missing UpdatedAt data for pipeline #%d", pipeline.ID)
	}
	build = cache.Build{
		Repository:      repository,
		ID:              strconv.Itoa(pipeline.ID),
		Commit:          cacheCommit,
		Ref:             pipeline.Ref,
		IsTag:           pipeline.Tag,
		RepoBuildNumber: strconv.Itoa(pipeline.ID),
		State:           FromGitLabState(pipeline.Status),
		CreatedAt:       utils.NullTimeFromTime(pipeline.CreatedAt),
		StartedAt:       utils.NullTimeFromTime(pipeline.StartedAt),
		FinishedAt:      utils.NullTimeFromTime(pipeline.FinishedAt),
		UpdatedAt:       *pipeline.UpdatedAt,
		Duration: utils.NullDuration{
			Duration: time.Duration(pipeline.Duration) * time.Second,
			Valid:    pipeline.Duration > 0,
		},
		WebURL: pipeline.WebURL,
		Stages: make(map[int]*cache.Stage),
		Jobs:   make([]*cache.Job, 0),
	}

	jobs := make([]*gitlab.Job, 0)
	options := gitlab.ListJobsOptions{}
	for {
		select {
		case <-c.rateLimiter:
		case <-ctx.Done():
			return build, ctx.Err()
		}
		pageJobs, resp, err := c.remote.Jobs.ListPipelineJobs(repository.ID, pipeline.ID, &options, gitlab.WithContext(ctx))
		if err != nil {
			return build, nil
		}
		jobs = append(jobs, pageJobs...)

		if resp.NextPage == 0 {
			break
		}
		options.Page = resp.NextPage
	}

	stagesByName := make(map[string]*cache.Stage)
	build.Stages = make(map[int]*cache.Stage)
	for _, job := range jobs {
		if _, exists := stagesByName[job.Stage]; !exists {
			stage := cache.Stage{
				ID:   len(stagesByName) + 1,
				Name: job.Stage,
				Jobs: make([]*cache.Job, 0),
			}
			stagesByName[job.Stage] = &stage
			build.Stages[stage.ID] = &stage
		}
	}

	for _, gitlabJob := range jobs {
		job := cache.Job{
			ID:         strconv.Itoa(gitlabJob.ID),
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
			WebURL:       gitlabJob.WebURL,
			AllowFailure: gitlabJob.AllowFailure,
		}
		stagesByName[gitlabJob.Stage].Jobs = append(stagesByName[gitlabJob.Stage].Jobs, &job)
	}

	// Compute stage state
	for _, stage := range build.Stages {
		// Each stage contains all job runs. Select only the last run of each job
		// Earliest runs should not influence the current state of the stage
		jobsByName := make(map[string]*cache.Job)
		for _, job := range stage.Jobs {
			previousJob, exists := jobsByName[job.Name]
			// Dates may be NULL so we have to rely on IDs to find out which job is older. meh.
			if !exists || previousJob.ID < job.ID {
				jobsByName[job.Name] = job
			}
		}
		jobs := make([]cache.Statuser, 0, len(jobsByName))
		for _, job := range jobsByName {
			jobs = append(jobs, *job)
		}
		stage.State = cache.AggregateStatuses(jobs)
	}

	c.mux.Lock()
	c.updateTimePerBuildID[build.ID] = build.UpdatedAt
	c.mux.Unlock()
	return build, nil
}
