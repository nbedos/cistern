package providers

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
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
	accountID   string
	remote      *gitlab.Client
	rateLimiter <-chan time.Time
}

func NewGitLabClient(accountID string, token string, rateLimit time.Duration) GitLabClient {
	return GitLabClient{
		accountID:   accountID,
		remote:      gitlab.NewClient(nil, token),
		rateLimiter: time.Tick(rateLimit),
	}
}

func (c GitLabClient) AccountID() string {
	return c.accountID
}

func (c GitLabClient) Builds(ctx context.Context, repositoryURL string, maxAge time.Duration, buildc chan<- cache.Build) error {
	repository, err := c.Repository(ctx, repositoryURL)
	if err != nil {
		return err
	}
	return c.LastBuilds(ctx, repository, maxAge, buildc)
}

func (c GitLabClient) StreamLogs(ctx context.Context, writerByJobID map[int]io.WriteCloser) error {
	return nil
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

	return cache.Repository{
		ID:        project.ID,
		Owner:     project.Owner.Username,
		Name:      project.Name,
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
	default:
		return cache.Unknown
	}
}

func (c GitLabClient) LastBuilds(ctx context.Context, repository cache.Repository, maxAge time.Duration, buildc chan<- cache.Build) error {
	opt := gitlab.ListProjectPipelinesOptions{
		ListOptions: gitlab.ListOptions{
			Page:    0,
			PerPage: 20,
		},
	}
	lastPage := false

pageLoop:
	for opt.ListOptions.Page = 0; !lastPage; opt.ListOptions.Page++ {
		select {
		case <-c.rateLimiter:
		case <-ctx.Done():
			return ctx.Err()
		}

		pipelines, _, err := c.remote.Pipelines.ListProjectPipelines(repository.ID, &opt,
			gitlab.WithContext(ctx))
		if err != nil {
			return err
		}
		if len(pipelines) == 0 {
			lastPage = true
			continue
		}

		for _, minimalPipeline := range pipelines {
			build, err := c.fetchBuild(ctx, &repository, minimalPipeline.ID)
			if err != nil {
				return err
			}

			// minimalPipeline is so minimal that it has no date attribute so we have to test
			// the date of the full build.
			if build.CreatedAt.Valid && time.Since(build.CreatedAt.Time) > maxAge {
				lastPage = true
				continue pageLoop
			}
			select {
			case buildc <- build:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return nil
}

func (c GitLabClient) Log(ctx context.Context, repository cache.Repository, jobID int) (string, error) {
	trace, _, err := c.remote.Jobs.GetTraceFile(repository.ID, jobID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return "", err
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(trace); err != nil {
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
		ID:              pipeline.ID,
		Commit:          cacheCommit,
		Ref:             pipeline.Ref,
		IsTag:           pipeline.Tag,
		RepoBuildNumber: strconv.Itoa(pipeline.ID),
		State:           FromGitLabState(pipeline.Status),
		CreatedAt:       utils.NullTimeFromTime(pipeline.CreatedAt),
		StartedAt:       utils.NullTimeFromTime(pipeline.StartedAt),
		FinishedAt:      utils.NullTimeFromTime(pipeline.FinishedAt),
		UpdatedAt:       *pipeline.UpdatedAt,
		Duration:        sql.NullInt64{Int64: int64(pipeline.Duration), Valid: pipeline.Duration > 0},
		WebURL:          pipeline.WebURL,
		Stages:          make(map[int]*cache.Stage),
		Jobs:            make(map[int]*cache.Job),
	}

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return build, ctx.Err()
	}
	jobs, _, err := c.remote.Jobs.ListPipelineJobs(repository.ID, pipeline.ID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return build, nil
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

	jobc := make(chan cache.Job)
	errc := make(chan error, len(jobs))
	wg := sync.WaitGroup{}

	for _, job := range jobs {
		wg.Add(1)
		go func(job *gitlab.Job) {
			defer wg.Done()
			select {
			case <-c.rateLimiter:
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}

			jobc <- cache.Job{
				Build:        &build,
				Stage:        stagesByName[job.Stage],
				ID:           job.ID,
				State:        FromGitLabState(job.Status),
				Name:         job.Name,
				Log:          sql.NullString{},
				CreatedAt:    utils.NullTimeFromTime(job.CreatedAt),
				StartedAt:    utils.NullTimeFromTime(job.StartedAt),
				FinishedAt:   utils.NullTimeFromTime(job.FinishedAt),
				Duration:     sql.NullInt64{Int64: int64(job.Duration), Valid: int64(job.Duration) > 0},
				WebURL:       job.WebURL,
				AllowFailure: job.AllowFailure,
			}
		}(job)
	}

	go func() {
		wg.Wait()
		close(jobc)
		close(errc)
	}()

	for job := range jobc {
		job := job
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
		jobs := make([]cache.Job, 0, len(jobsByName))
		for _, job := range jobsByName {
			jobs = append(jobs, *job)
		}
		stage.State = cache.StageState(jobs)
	}

	return build, <-errc
}
