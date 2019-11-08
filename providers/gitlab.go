package providers

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"github.com/cenkalti/backoff/v3"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
	"github.com/xanzy/go-gitlab"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
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

// TODO Implement live updates once https://github.com/xanzy/go-gitlab/pull/731 is merged
func (c GitLabClient) Builds(ctx context.Context, repositoryURL string, maxAge time.Duration, buildc chan<- cache.Build) error {
	repository, err := c.Repository(ctx, repositoryURL)
	if err != nil {
		return err
	}

	b := backoff.ExponentialBackOff{
		InitialInterval:     5 * time.Second,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         time.Minute,
		MaxElapsedTime:      0,
		Clock:               backoff.SystemClock,
	}

	var active bool
	for {
		if active, err = c.LastBuilds(ctx, repository, maxAge, buildc); err != nil {
			return err
		}
		if active {
			b.Reset()
		}

		waitTime := b.NextBackOff()
		if waitTime == backoff.Stop {
			break
		}

		select {
		case <-time.After(waitTime):
			// Do nothing
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (c GitLabClient) StreamLog(ctx context.Context, repositoryID int, jobID int, writer io.Writer) error {
	b := backoff.ExponentialBackOff{
		InitialInterval:     10 * time.Second,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         time.Hour,
		MaxElapsedTime:      0,
		Clock:               backoff.SystemClock,
	}
	b.Reset()

	contentLength := 0
	traceLength := 0
	jobIsRunning := true
	for jobIsRunning {
		job, _, err := c.GetJob(ctx, repositoryID, jobID)
		if err != nil {
			return err
		}
		jobIsRunning = FromGitLabState(job.Status).IsActive()

		// Ideally we would use the Range HTTP request header to get the next part of the log
		// but that is not supported by GitLab. So we rely on Content-Length to monitor log
		// changes.
		nextContentLength, _, err := c.GetTraceFileSize(ctx, repositoryID, jobID)
		if err != nil {
			return err
		}
		if nextContentLength > contentLength {
			buf, n, _, err := c.GetTraceFile(ctx, repositoryID, jobID)
			if err != nil {
				return err
			}
			contentLength = n

			if buf.Len() > traceLength {
				b.Reset()
				_ = buf.Next(traceLength)
				written, err := buf.WriteTo(writer)
				if err != nil {
					return err
				}
				traceLength += int(written)
			}
		}

		waitTime := b.NextBackOff()
		if waitTime == backoff.Stop {
			break
		}

		select {
		case <-time.After(waitTime):
			// Do nothing
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (c *GitLabClient) GetJob(ctx context.Context, repositoryID int, jobID int) (*gitlab.Job, *gitlab.Response, error) {
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	return c.remote.Jobs.GetJob(repositoryID, jobID, gitlab.WithContext(ctx))
}

func (c *GitLabClient) GetTraceFileSize(ctx context.Context, projectID int, jobID int) (int, *gitlab.Response, error) {
	u := fmt.Sprintf("projects/%d/jobs/%d/trace", projectID, jobID)

	req, err := c.remote.NewRequest("HEAD", u, nil, []gitlab.OptionFunc{gitlab.WithContext(ctx)})
	if err != nil {
		return 0, nil, err
	}

	traceBuf := new(bytes.Buffer)
	resp, err := c.remote.Do(req, traceBuf)
	if err != nil {
		return 0, resp, err
	}

	contentLength, err := strconv.Atoi(resp.Header.Get("Content-Length"))
	if err != nil {
		return 0, resp, err
	}

	return contentLength, resp, err
}

func (c *GitLabClient) GetTraceFile(ctx context.Context, repositoryID int, jobID int) (bytes.Buffer, int, *gitlab.Response, error) {
	buf := bytes.Buffer{}
	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return buf, 0, nil, ctx.Err()
	}
	trace, resp, err := c.remote.Jobs.GetTraceFile(repositoryID, jobID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return buf, 0, nil, err
	}
	n, err := strconv.Atoi(resp.Header.Get("Content-Length"))
	if err != nil {
		return buf, 0, nil, err
	}

	_, err = buf.ReadFrom(trace)
	return buf, n, resp, err
}

func (c GitLabClient) Repository(ctx context.Context, repositoryURL string) (cache.Repository, error) {
	repositorySlug, err := utils.RepositorySlugFromURL(repositoryURL)
	if err != nil {
		return cache.Repository{}, err
	}

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return cache.Repository{}, ctx.Err()
	}
	project, _, err := c.remote.Projects.GetProject(repositorySlug, nil, gitlab.WithContext(ctx))
	if err != nil {
		if err, ok := err.(*gitlab.ErrorResponse); ok && err.Response.StatusCode == 404 {
			return cache.Repository{}, cache.ErrRepositoryNotFound
		}
		return cache.Repository{}, err
	}

	splitPath := strings.SplitN(project.PathWithNamespace, "/", 2)
	if len(splitPath) != 2 {
		return cache.Repository{}, fmt.Errorf("invalid repository path: '%s'", project.PathWithNamespace)
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
	case "pending":
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

func (c GitLabClient) LastBuilds(ctx context.Context, repository cache.Repository, maxAge time.Duration, buildc chan<- cache.Build) (bool, error) {
	opt := gitlab.ListProjectPipelinesOptions{
		ListOptions: gitlab.ListOptions{
			Page:    0,
			PerPage: 20,
		},
	}
	active := false
	lastPage := false

	for opt.ListOptions.Page = 0; !lastPage; opt.ListOptions.Page++ {
		select {
		case <-c.rateLimiter:
		case <-ctx.Done():
			return active, ctx.Err()
		}
		pipelines, _, err := c.remote.Pipelines.ListProjectPipelines(repository.ID, &opt,
			gitlab.WithContext(ctx))
		if err != nil {
			return active, err
		}
		if len(pipelines) == 0 {
			lastPage = true
			continue
		}

	pipelines:
		for _, minimalPipeline := range pipelines {
			active = active || FromGitLabState(minimalPipeline.Status).IsActive()

			if minimalPipeline.CreatedAt != nil && time.Since(*minimalPipeline.CreatedAt) > maxAge {
				lastPage = true
				continue pipelines
			}

			lastUpdate := c.updateTimePerBuildID[strconv.Itoa(minimalPipeline.ID)]
			if minimalPipeline.UpdatedAt != nil && minimalPipeline.UpdatedAt.After(lastUpdate) {
				build, err := c.fetchBuild(ctx, &repository, minimalPipeline.ID)
				if err != nil {
					return active, err
				}
				select {
				case buildc <- build:
				case <-ctx.Done():
					return active, ctx.Err()
				}
			}
		}
	}

	return active, nil
}

func (c GitLabClient) Log(ctx context.Context, repository cache.Repository, jobID int) (string, error) {
	buf, _, _, err := c.GetTraceFile(ctx, repository.ID, jobID)
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
		Duration: cache.NullDuration{
			Duration: time.Duration(pipeline.Duration) * time.Second,
			Valid:    pipeline.Duration > 0,
		},
		WebURL: pipeline.WebURL,
		Stages: make(map[int]*cache.Stage),
		Jobs:   make(map[int]*cache.Job),
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
				Build: &build,
				ID:    len(stagesByName) + 1,
				Name:  job.Stage,
				Jobs:  make(map[int]*cache.Job),
			}
			stagesByName[job.Stage] = &stage
			build.Stages[stage.ID] = &stage
		}
	}

	for _, gitlabJob := range jobs {
		job := cache.Job{
			Build:      &build,
			Stage:      stagesByName[gitlabJob.Stage],
			ID:         gitlabJob.ID,
			State:      FromGitLabState(gitlabJob.Status),
			Name:       gitlabJob.Name,
			Log:        sql.NullString{},
			CreatedAt:  utils.NullTimeFromTime(gitlabJob.CreatedAt),
			StartedAt:  utils.NullTimeFromTime(gitlabJob.StartedAt),
			FinishedAt: utils.NullTimeFromTime(gitlabJob.FinishedAt),
			Duration: cache.NullDuration{
				Duration: time.Duration(gitlabJob.Duration) * time.Second,
				Valid:    int64(gitlabJob.Duration) > 0,
			},
			WebURL:       gitlabJob.WebURL,
			AllowFailure: gitlabJob.AllowFailure,
		}
		build.Stages[job.Stage.ID].Jobs[job.ID] = &job
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
