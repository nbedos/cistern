package providers

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
	"github.com/xanzy/go-gitlab"
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

func (c GitLabClient) Builds(ctx context.Context, repository cache.Repository, duration time.Duration, inserters chan<- []cache.Inserter) error {
	return c.LastBuilds(ctx, repository, 20, inserters)
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
		return cache.Repository{}, err
	}

	return cache.Repository{
		Owner:     project.Owner.Username,
		Name:      project.Name,
		URL:       project.WebURL,
		AccountID: c.accountID,
		RemoteID:  project.ID,
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

func (c GitLabClient) LastBuilds(ctx context.Context, repository cache.Repository, limit int, insertersc chan<- []cache.Inserter) error {
	opt := gitlab.ListProjectPipelinesOptions{
		ListOptions: gitlab.ListOptions{PerPage: limit},
	}

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return ctx.Err()
	}
	pipelines, _, err := c.remote.Pipelines.ListProjectPipelines(repository.RemoteID, &opt,
		gitlab.WithContext(ctx))
	if err != nil {
		return err
	}

	errc := make(chan error)
	wg := sync.WaitGroup{}
	for _, minimalPipeline := range pipelines {
		wg.Add(1)
		go func(pipelineID int) {
			defer wg.Done()
			buildInserters, err := c.fetchBuild(ctx, repository.RemoteID, repository.URL, pipelineID)
			if err != nil {
				errc <- err
				return
			}
			select {
			case insertersc <- buildInserters:
			case <-ctx.Done():
				errc <- ctx.Err()
			}

		}(minimalPipeline.ID)
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	for e := range errc {
		if err == nil {
			err = e
		}
	}

	return err
}

func (c GitLabClient) fetchBuild(ctx context.Context, projectID int, repositoryURL string, pipelineID int) ([]cache.Inserter, error) {
	inserters := make([]cache.Inserter, 0)

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	pipeline, _, err := c.remote.Pipelines.GetPipeline(projectID, pipelineID, gitlab.WithContext(ctx))
	if err != nil {
		return inserters, err
	}

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	commit, _, err := c.remote.Commits.GetCommit(projectID, pipeline.SHA, gitlab.WithContext(ctx))
	if err != nil {
		return inserters, err
	}

	cacheCommit := cache.Commit{
		AccountID:     c.accountID,
		ID:            commit.ID,
		RepositoryURL: repositoryURL,
		Message:       commit.Message,
		Date:          utils.NullTimeFrom(commit.AuthoredDate),
	}

	inserters = append(inserters, cacheCommit)

	if pipeline.UpdatedAt == nil {
		return inserters, fmt.Errorf("missing UpdatedAt data for pipeline #%d", pipeline.ID)
	}
	cacheBuild := cache.Build{
		AccountID:       c.accountID,
		ID:              pipeline.ID,
		RepositoryURL:   repositoryURL,
		CommitID:        pipeline.SHA,
		Ref:             pipeline.Ref,
		IsTag:           pipeline.Tag,
		RepoBuildNumber: strconv.Itoa(pipeline.ID),
		State:           FromGitLabState(pipeline.Status),
		CreatedAt:       utils.NullTimeFrom(pipeline.CreatedAt),
		StartedAt:       utils.NullTimeFrom(pipeline.StartedAt),
		FinishedAt:      utils.NullTimeFrom(pipeline.FinishedAt),
		UpdatedAt:       *pipeline.UpdatedAt,
		Duration:        sql.NullInt64{Int64: int64(pipeline.Duration), Valid: pipeline.Duration > 0},
		WebURL:          pipeline.WebURL,
	}
	inserters = append(inserters, cacheBuild)

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	jobs, _, err := c.remote.Jobs.ListPipelineJobs(projectID, pipeline.ID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return inserters, nil
	}

	stages := make(map[string]cache.Stage)
	for _, job := range jobs {
		if _, exists := stages[job.Stage]; !exists {
			stages[job.Stage] = cache.Stage{
				AccountID: cacheBuild.AccountID,
				BuildID:   cacheBuild.ID,
				ID:        len(stages) + 1,
				Name:      job.Stage,
				State:     cache.Unknown,
			}
			inserters = append(inserters, stages[job.Stage])
		}
	}

	jobc := make(chan cache.Job)
	errc := make(chan error, len(jobs))
	wg := sync.WaitGroup{}

	for jobIndex, job := range jobs {
		wg.Add(1)
		go func(job *gitlab.Job, jobIndex int) {
			defer wg.Done()
			select {
			case <-c.rateLimiter:
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}
			reader, _, err := c.remote.Jobs.GetTraceFile(projectID, job.ID, nil, gitlab.WithContext(ctx))
			if err != nil {
				errc <- err
				return
			}
			buf := new(bytes.Buffer)
			_, err = buf.ReadFrom(reader)
			if err != nil {
				errc <- err
				return
			}

			jobc <- cache.Job{
				Key: cache.JobKey{
					AccountID: c.accountID,
					BuildID:   cacheBuild.ID,
					StageID:   stages[job.Stage].ID,
					ID:        jobIndex + 1,
				},
				State:      FromGitLabState(job.Status),
				Name:       job.Name,
				Log:        buf.String(),
				CreatedAt:  utils.NullTimeFrom(job.CreatedAt),
				StartedAt:  utils.NullTimeFrom(job.StartedAt),
				FinishedAt: utils.NullTimeFrom(job.FinishedAt),
				Duration:   sql.NullInt64{Int64: int64(job.Duration), Valid: int64(job.Duration) > 0},
			}
		}(job, jobIndex)
	}

	go func() {
		wg.Wait()
		close(jobc)
		close(errc)
	}()

	for job := range jobc {
		inserters = append(inserters, job)
	}

	return inserters, <-errc
}
