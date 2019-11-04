package providers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff"
)

type CircleCIClient struct {
	baseURL                 url.URL
	httpClient              *http.Client
	rateLimiter             <-chan time.Time
	token                   string
	accountID               string
	updateTimePerPipelineID map[int]time.Time
	mux                     *sync.Mutex
}

var CircleCIURL = url.URL{
	Scheme:  "https",
	Host:    "circleci.com",
	Path:    "api/v1.1",
	RawPath: "api/v1.1",
}

func NewCircleCIClient(URL url.URL, accountID string, token string, rateLimit time.Duration) CircleCIClient {
	return CircleCIClient{
		baseURL:                 URL,
		httpClient:              &http.Client{Timeout: 10 * time.Second},
		rateLimiter:             time.Tick(rateLimit),
		token:                   token,
		accountID:               accountID,
		updateTimePerPipelineID: make(map[int]time.Time),
		mux:                     &sync.Mutex{},
	}
}

func (c CircleCIClient) get(ctx context.Context, resourceURL url.URL) (*bytes.Buffer, error) {
	parameters := resourceURL.Query()
	parameters.Add("circle-token", c.token)
	resourceURL.RawQuery = parameters.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", resourceURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Accept", "application/json")

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errorBody struct {
			Message string `json:"message"`
		}
		var message string
		if jsonErr := json.Unmarshal(body.Bytes(), &errorBody); jsonErr == nil {
			message = errorBody.Message
		}

		// Remove the authentication token from the URL so that it's not leaked in logs
		noTokenURL := req.URL
		parameters := noTokenURL.Query()
		parameters.Del("circle-token")
		noTokenURL.RawQuery = parameters.Encode()

		return nil, HTTPError{
			Method:  req.Method,
			URL:     noTokenURL.String(),
			Status:  resp.StatusCode,
			Message: message,
		}
	}

	return body, nil
}

func (c CircleCIClient) fetchLog(ctx context.Context, url string) (string, error) {
	// FIXME Deduplicate code below
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", HTTPError{
			Method:  "GET",
			URL:     url,
			Status:  resp.StatusCode,
			Message: "", // FIXME
		}
	}

	var logs []struct {
		Message string `json:"message"`
	}
	if err = json.Unmarshal(body.Bytes(), &logs); err != nil {
		return "", err
	}

	builder := strings.Builder{}
	for _, log := range logs {
		builder.WriteString(log.Message)
	}

	return builder.String(), err
}

func (c CircleCIClient) projectEndpoint(owner string, name string) url.URL {
	endpoint := c.baseURL
	pathFormat := "/project/gh/%s/%s"
	endpoint.Path += fmt.Sprintf(pathFormat, owner, name)
	endpoint.RawPath += fmt.Sprintf(pathFormat, url.PathEscape(owner), url.PathEscape(name))

	return endpoint
}

func (c CircleCIClient) AccountID() string {
	return c.accountID
}

func (c CircleCIClient) Builds(ctx context.Context, repositoryURL string, maxAge time.Duration, buildc chan<- cache.Build) error {
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
	b.Reset()

	for {
		waitTime := b.NextBackOff()
		if waitTime == backoff.Stop {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
		}

		active, err := c.fetchRepositoryBuilds(ctx, repository, maxAge, buildc)
		if err != nil {
			return err
		}
		if active {
			b.Reset()
		}
	}

	return nil
}

func (c CircleCIClient) StreamLogs(ctx context.Context, writerByJobID map[int]io.WriteCloser) error {
	return nil
}

func (c CircleCIClient) Log(ctx context.Context, repository cache.Repository, jobID int) (string, error) {
	endPoint := c.projectEndpoint(repository.Owner, repository.Name)
	build, err := c.fetchBuild(ctx, endPoint, jobID, &repository, true)
	if err != nil {
		return "", err
	}
	job, exists := build.Jobs[jobID]
	if !exists {
		return "", fmt.Errorf("no matching job found for ID %d", jobID)
	}
	if !job.Log.Valid {
		return "", nil
	}

	return job.Log.String, nil
}

func (c CircleCIClient) Repository(ctx context.Context, repositoryURL string) (cache.Repository, error) {
	slug, err := utils.RepositorySlugFromURL(repositoryURL)
	if err != nil {
		return cache.Repository{}, err
	}

	components := strings.Split(slug, "/")
	if len(components) != 2 {
		return cache.Repository{}, fmt.Errorf("invalid repository slug "+
			"(expected two path components): '%s' ", slug)
	}
	owner, name := components[0], components[1]

	// Validate repository existence on CircleCI
	endPoint := c.projectEndpoint(owner, name)
	if _, err = c.listRecentAndOutdatedBuildIDs(ctx, endPoint, 0, 1, time.Second); err != nil {
		if err, ok := err.(HTTPError); ok && err.Status == 404 {
			return cache.Repository{}, cache.ErrRepositoryNotFound
		}
		return cache.Repository{}, err
	}

	// FIXME What about Repository.ID?
	return cache.Repository{
		AccountID: c.accountID,
		URL:       repositoryURL,
		Owner:     owner,
		Name:      name,
	}, nil
}

func (c CircleCIClient) listRecentAndOutdatedBuildIDs(ctx context.Context, projectEndpoint url.URL, offset int, limit int, maxAge time.Duration) ([]int, error) {
	parameters := projectEndpoint.Query()
	parameters.Add("offset", strconv.Itoa(offset))
	parameters.Add("limit", strconv.Itoa(limit))
	parameters.Add("shallow", "true")
	projectEndpoint.RawQuery = parameters.Encode()

	body, err := c.get(ctx, projectEndpoint)
	if err != nil {
		return nil, err
	}

	var builds []circleCIBuild
	if err := json.Unmarshal(body.Bytes(), &builds); err != nil {
		return nil, err
	}

	ids := make([]int, 0, len(builds))
	for _, build := range builds {
		createdAt, err := utils.NullTimeFromString(build.CreatedAt)
		if err != nil {
			return nil, err
		}
		if !createdAt.Valid || time.Since(createdAt.Time) > maxAge {
			continue
		}
		startedAt, err := utils.NullTimeFromString(build.StartedAt)
		if err != nil {
			return nil, err
		}
		finishedAt, err := utils.NullTimeFromString(build.FinishedAt)
		if err != nil {
			return nil, err
		}

		updatedAt := utils.MaxNullTime(createdAt, startedAt, finishedAt)
		c.mux.Lock()
		lastUpdate, exists := c.updateTimePerPipelineID[build.ID]
		if !exists || (updatedAt.Valid && updatedAt.Time.After(lastUpdate)) {
			c.updateTimePerPipelineID[build.ID] = updatedAt.Time
			ids = append(ids, build.ID)
		}
		c.mux.Unlock()
	}

	return ids, nil
}

func (c CircleCIClient) fetchRepositoryBuilds(ctx context.Context, repository cache.Repository, maxAge time.Duration, buildc chan<- cache.Build) (bool, error) {
	projectEndpoint := c.projectEndpoint(repository.Owner, repository.Name)
	pageSize := 20
	active := false

	var err error
	var buildIDs []int
	for offset := 0; ; offset += pageSize {
		buildIDs, err = c.listRecentAndOutdatedBuildIDs(ctx, projectEndpoint, offset, pageSize, maxAge)
		if err != nil {
			return active, err
		}
		if len(buildIDs) == 0 {
			break
		}

		for _, buildID := range buildIDs {
			build, err := c.fetchBuild(ctx, projectEndpoint, buildID, &repository, false)
			if err != nil {
				return active, err
			}
			active = active || build.State.IsActive()
			select {
			case buildc <- build:
			case <-ctx.Done():
				return active, ctx.Err()
			}

		}
	}

	return active, nil
}

func (c CircleCIClient) fetchBuild(ctx context.Context, projectEndpoint url.URL, buildID int, repository *cache.Repository, log bool) (build cache.Build, err error) {
	projectEndpoint.Path += fmt.Sprintf("/%d", buildID)
	body, err := c.get(ctx, projectEndpoint)
	if err != nil {
		return build, err
	}

	var circleCIBuild circleCIBuild
	if err := json.Unmarshal(body.Bytes(), &circleCIBuild); err != nil {
		return build, err
	}

	buildLog := sql.NullString{}
	if log {
		// FIXME Prefix each line by the name of the step in a way compatible with carriage returns
		fullLog := strings.Builder{}
		for _, step := range circleCIBuild.Steps {
			for _, action := range step.Actions {
				prefix := fmt.Sprintf("[%s] ", action.Name)
				if action.BashCommand != "" {
					// BashCommand contains the reason for failure when no configuration is found
					// for the project so include it in the log output
					fullLog.WriteString(utils.Prefix(action.BashCommand, prefix+"#"))
					fullLog.WriteString(utils.Prefix("\n", prefix))
				}
				if action.LogURL != "" {
					log, err := c.fetchLog(ctx, action.LogURL)
					if err != nil {
						return build, err
					}

					fullLog.WriteString(utils.Prefix(log, prefix))
				}
			}
		}
		buildLog.Valid = true
		buildLog.String = fullLog.String()
	}

	build, err = circleCIBuild.ToCacheBuild(c.accountID, repository)
	if err != nil {
		return build, err
	}

	// FIXME Creating this job would not be necessary if we could store the log in the build
	//  itself. Would that be better?
	build.Jobs = map[int]*cache.Job{
		1: {
			Build:      &build,
			Stage:      nil,
			ID:         1,
			State:      build.State,
			Name:       build.Commit.Message, // FIXME Jobs should have a specific name, not repeat the commit message
			CreatedAt:  build.CreatedAt,
			StartedAt:  build.StartedAt,
			FinishedAt: build.FinishedAt,
			Duration:   build.Duration,
			Log:        buildLog,
		},
	}

	c.mux.Lock()
	c.updateTimePerPipelineID[build.ID] = build.UpdatedAt
	c.mux.Unlock()

	return build, nil
}

type circleCIBuild struct {
	ID                   int    `json:"build_num"`
	WebURL               string `json:"build_url"`
	Branch               string `json:"branch"`
	Sha                  string `json:"vcs_revision"`
	Tag                  string `json:"vcs_tag"`
	State                string `json:"status"`
	StartedAt            string `json:"start_time"`
	FinishedAt           string `json:"stop_time"`
	Outcome              string `json:"outcome"`
	Lifecycle            string `json:"lifecycle"`
	Message              string `json:"subject"`
	CreatedAt            string `json:"queued_at"`
	CommittedAt          string `json:"committer_date"`
	DurationMilliseconds int    `json:"build_time_millis"`
	Steps                []struct {
		Name    string `json:"name"`
		Actions []struct {
			Name        string `json:"name"`
			BashCommand string `json:"bash_command"`
			LogURL      string `json:"output_url"`
		} `json:"actions"`
	} `json:"steps"`
}

func (b circleCIBuild) ToCacheBuild(accountID string, repository *cache.Repository) (cache.Build, error) {
	build := cache.Build{
		Repository: repository,
		ID:         b.ID,
		Commit: cache.Commit{
			Sha:     b.Sha,
			Message: b.Message,
		},
		RepoBuildNumber: strconv.Itoa(b.ID),
		State:           fromCircleCIStatus(b.Lifecycle, b.Outcome),
		WebURL:          b.WebURL,
	}

	if b.DurationMilliseconds > 0 {
		build.Duration = cache.NullDuration{
			Duration: time.Duration(b.DurationMilliseconds) * time.Millisecond,
			Valid:    true,
		}
	}

	var err error
	if build.CreatedAt, err = utils.NullTimeFromString(b.CreatedAt); err != nil {
		return build, err
	}
	if build.StartedAt, err = utils.NullTimeFromString(b.StartedAt); err != nil {
		return build, err
	}
	if build.FinishedAt, err = utils.NullTimeFromString(b.FinishedAt); err != nil {
		return build, err
	}

	updatedAt := utils.MaxNullTime(build.FinishedAt, build.StartedAt, build.CreatedAt)
	if !updatedAt.Valid {
		return build, errors.New("updatedAt attribute cannot be null")
	}
	build.UpdatedAt = updatedAt.Time

	build.Commit.Date, err = utils.NullTimeFromString(b.CommittedAt)
	if err != nil {
		return cache.Build{}, err
	}

	if build.IsTag = b.Tag != ""; build.IsTag {
		build.Ref = b.Tag
	} else {
		build.Ref = b.Branch
	}

	return build, nil
}

func fromCircleCIStatus(lifecycle string, outcome string) cache.State {
	switch lifecycle {
	case "queued", "scheduled":
		return cache.Pending
	case "not_run", "not_running":
		return cache.Skipped
	case "running":
		return cache.Running
	case "finished":
		switch outcome {
		case "canceled":
			return cache.Canceled
		case "infrastructure_fail", "timedout", "failed", "no_tests":
			return cache.Failed
		case "success":
			return cache.Passed
		}
	}

	return cache.Unknown
}
