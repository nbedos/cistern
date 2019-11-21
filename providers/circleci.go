package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
)

type CircleCIClient struct {
	baseURL     url.URL
	httpClient  *http.Client
	rateLimiter <-chan time.Time
	token       string
	accountID   string
	mux         *sync.Mutex
}

var CircleCIURL = url.URL{
	Scheme:  "https",
	Host:    "circleci.com",
	Path:    "api/v1.1",
	RawPath: "api/v1.1",
}

func NewCircleCIClient(URL url.URL, accountID string, token string, rateLimit time.Duration) CircleCIClient {
	return CircleCIClient{
		baseURL:     URL,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		rateLimiter: time.Tick(rateLimit),
		token:       token,
		accountID:   accountID,
		mux:         &sync.Mutex{},
	}
}

func (c CircleCIClient) get(ctx context.Context, resourceURL url.URL) (*bytes.Buffer, error) {
	parameters := resourceURL.Query()
	parameters.Add("circle-token", c.token)
	resourceURL.RawQuery = parameters.Encode()

	req, err := http.NewRequest("GET", resourceURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Accept", "application/json")
	req.WithContext(ctx)

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
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req = req.WithContext(ctx)

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

func (c CircleCIClient) BuildFromURL(ctx context.Context, u string) (cache.Build, error) {
	// TODO
	/*owner, repo, id, err := parseCircleCIWebURL(&c.baseURL, u)
	if err != nil {
		return cache.Build{}, err
	}

	repository, err := c.Repository(ctx, fmt.Sprintf("%s/%s", owner, repo))
	if err != nil {
		return cache.Build{}, err
	}*/

	return cache.Build{}, nil
}

// Extract owner, repository and build ID from web URL of build
func parseCircleCIWebURL(baseURL *url.URL, u string) (string, string, string, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", "", "", err
	}

	if v.Hostname() != strings.TrimPrefix(baseURL.Hostname(), "api.") {
		return "", "", "", cache.ErrUnknownURL
	}

	// URL format: https://circleci.com/gh/nbedos/citop/36
	cs := strings.Split(v.EscapedPath(), "/")
	if len(cs) < 5 {
		return "", "", "", cache.ErrUnknownURL
	}

	owner, repo, id := cs[2], cs[3], cs[4]
	return owner, repo, id, nil
}

func (c CircleCIClient) Log(ctx context.Context, repository cache.Repository, jobID int) (string, bool, error) {
	endPoint := c.projectEndpoint(repository.Owner, repository.Name)
	job, _, err := c.fetchJob(ctx, endPoint, jobID, true)
	if err != nil {
		return "", false, err
	}
	if !job.Log.Valid {
		return "", false, nil
	}

	return job.Log.String, !job.State.IsActive(), nil
}

func (c *CircleCIClient) Repository(ctx context.Context, repositoryURL string) (cache.Repository, error) {
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
	parameters := endPoint.Query()
	parameters.Add("offset", strconv.Itoa(0))
	parameters.Add("limit", strconv.Itoa(1))
	parameters.Add("shallow", "true")
	endPoint.RawQuery = parameters.Encode()
	if _, err = c.get(ctx, endPoint); err != nil {
		if err, ok := err.(HTTPError); ok && err.Status == 404 {
			return cache.Repository{}, cache.ErrRepositoryNotFound
		}
		return cache.Repository{}, err
	}

	// FIXME What about repository.ID?
	return cache.Repository{
		AccountID: c.accountID,
		URL:       repositoryURL,
		Owner:     owner,
		Name:      name,
	}, nil
}

type circleRef struct {
	commit cache.Commit
	ref    string
	isTag  bool
}

func (c CircleCIClient) fetchWorkflow(ctx context.Context, projectEndpoint url.URL, id string, buildIDs []int, repository *cache.Repository, log bool) (build cache.Build, err error) {
	if len(buildIDs) == 0 {
		return cache.Build{}, nil
	}

	webURL := projectEndpoint
	webURL.Path = fmt.Sprintf(fmt.Sprintf("/workflow-run/%s", id))

	build = cache.Build{
		Repository:      repository,
		ID:              id,
		Commit:          cache.Commit{},
		Ref:             "",
		IsTag:           false,
		RepoBuildNumber: "",
		State:           "",
		CreatedAt:       utils.NullTime{},
		StartedAt:       utils.NullTime{},
		FinishedAt:      utils.NullTime{},
		UpdatedAt:       time.Time{},
		Duration:        utils.NullDuration{},
		WebURL:          webURL.String(),
		Stages:          nil,
		Jobs:            make(map[int]*cache.Job, len(buildIDs)),
	}

	jobs := make([]cache.Statuser, 0, len(buildIDs))
	for i, id := range buildIDs {
		job, ref, err := c.fetchJob(ctx, projectEndpoint, id, log)
		if err != nil {
			return build, err
		}
		if i == 0 {
			build.Commit = ref.commit
			build.Ref = ref.ref
			build.IsTag = ref.isTag
		}
		build.Jobs[job.ID] = &job

		build.CreatedAt = utils.MinNullTime(build.CreatedAt, job.CreatedAt)
		build.StartedAt = utils.MinNullTime(build.StartedAt, job.StartedAt)
		build.FinishedAt = utils.MaxNullTime(build.StartedAt, job.FinishedAt)

		if !build.Duration.Valid || job.Duration.Duration > build.Duration.Duration {
			build.Duration = job.Duration
		}

		jobs = append(jobs, job)
	}

	updatedAt := utils.MaxNullTime(build.CreatedAt, build.StartedAt, build.FinishedAt)
	if updatedAt.Valid {
		build.UpdatedAt = updatedAt.Time
	}

	build.State = cache.AggregateStatuses(jobs)

	return build, nil
}

func (c CircleCIClient) fetchJob(ctx context.Context, projectEndpoint url.URL, jobID int, log bool) (cache.Job, circleRef, error) {
	var err error
	var ref circleRef
	var job cache.Job

	projectEndpoint.Path += fmt.Sprintf("/%d", jobID)
	body, err := c.get(ctx, projectEndpoint)
	if err != nil {
		return job, ref, err
	}

	var circleCIBuild circleCIBuild
	if err := json.Unmarshal(body.Bytes(), &circleCIBuild); err != nil {
		return job, ref, err
	}

	job, err = circleCIBuild.ToCacheJob()
	if err != nil {
		return job, ref, err
	}

	ref.ref = circleCIBuild.Branch
	if circleCIBuild.Tag != "" {
		ref.isTag = true
		ref.ref = circleCIBuild.Tag
	}
	ref.commit = cache.Commit{
		Sha:     circleCIBuild.Sha,
		Message: circleCIBuild.Message,
	}
	if ref.commit.Date, err = utils.NullTimeFromString(circleCIBuild.CommittedAt); err != nil {
		return job, ref, err
	}

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
						return job, ref, err
					}

					fullLog.WriteString(utils.Prefix(log, prefix))
				}
			}
		}
		job.Log = utils.NullString{String: fullLog.String(), Valid: true}
	}

	return job, ref, nil
}

type circleCIBuild struct {
	ID        int    `json:"build_num"`
	WebURL    string `json:"build_url"`
	Branch    string `json:"branch"`
	Sha       string `json:"vcs_revision"`
	Workflows struct {
		JobName string `json:"job_name"`
		ID      string `json:"workflow_id"`
	} `json:"workflows"`
	Tag                  string `json:"vcs_tag"`
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
		Commit: cache.Commit{
			Sha:     b.Sha,
			Message: b.Message,
		},
		RepoBuildNumber: strconv.Itoa(b.ID),
		State:           fromCircleCIStatus(b.Lifecycle, b.Outcome),
		WebURL:          b.WebURL,
	}

	if b.DurationMilliseconds > 0 {
		build.Duration = utils.NullDuration{
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

func (b circleCIBuild) ToCacheJob() (cache.Job, error) {
	job := cache.Job{
		ID:           b.ID,
		State:        fromCircleCIStatus(b.Lifecycle, b.Outcome),
		Name:         b.Workflows.JobName,
		Log:          utils.NullString{},
		WebURL:       b.WebURL,
		AllowFailure: false,
	}

	if b.DurationMilliseconds > 0 {
		job.Duration = utils.NullDuration{
			Duration: time.Duration(b.DurationMilliseconds) * time.Millisecond,
			Valid:    true,
		}
	}

	var err error
	if job.CreatedAt, err = utils.NullTimeFromString(b.CreatedAt); err != nil {
		return job, err
	}
	if job.StartedAt, err = utils.NullTimeFromString(b.StartedAt); err != nil {
		return job, err
	}
	if job.FinishedAt, err = utils.NullTimeFromString(b.FinishedAt); err != nil {
		return job, err
	}

	return job, nil
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
