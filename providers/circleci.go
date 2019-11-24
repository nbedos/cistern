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

func NewCircleCIClient(accountID string, token string, URL url.URL, rateLimit time.Duration) CircleCIClient {
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
	owner, repo, id, err := parseCircleCIWebURL(&c.baseURL, u)
	if err != nil {
		return cache.Build{}, err
	}

	repository, err := c.repository(ctx, owner, repo)
	if err != nil {
		return cache.Build{}, err
	}

	endPoint := c.projectEndpoint(repository.Owner, repository.Name)
	return c.fetchBuild(ctx, endPoint, &repository, id, false)
}

// Extract owner, repository and build ID from web URL of build
func parseCircleCIWebURL(baseURL *url.URL, u string) (string, string, int, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", "", 0, err
	}

	if v.Hostname() != strings.TrimPrefix(baseURL.Hostname(), "api.") {
		return "", "", 0, cache.ErrUnknownURL
	}

	// URL format: https://circleci.com/gh/nbedos/citop/36
	cs := strings.Split(v.EscapedPath(), "/")
	if len(cs) < 5 {
		return "", "", 0, cache.ErrUnknownURL
	}

	owner, repo := cs[2], cs[3]
	id, err := strconv.Atoi(cs[4])
	if err != nil {
		return "", "", 0, err
	}

	return owner, repo, id, nil
}

func (c CircleCIClient) Log(ctx context.Context, repository cache.Repository, jobID string) (string, error) {
	return "", nil
}

func (c *CircleCIClient) repository(ctx context.Context, owner string, repo string) (cache.Repository, error) {
	// Validate repository existence on CircleCI

	endPoint := c.projectEndpoint(owner, repo)
	parameters := endPoint.Query()
	parameters.Add("offset", strconv.Itoa(0))
	parameters.Add("limit", strconv.Itoa(1))
	parameters.Add("shallow", "true")
	endPoint.RawQuery = parameters.Encode()
	if _, err := c.get(ctx, endPoint); err != nil {
		if err, ok := err.(HTTPError); ok && err.Status == 404 {
			return cache.Repository{}, cache.ErrRepositoryNotFound
		}
		return cache.Repository{}, err
	}

	// FIXME What about repository.ID?
	return cache.Repository{
		AccountID: c.accountID,
		URL:       fmt.Sprintf("https://github.com/%s/%s", owner, repo),
		Owner:     owner,
		Name:      repo,
	}, nil
}

func (c CircleCIClient) fetchBuild(ctx context.Context, projectEndpoint url.URL, repo *cache.Repository, buildID int, log bool) (cache.Build, error) {
	var err error
	var build cache.Build

	projectEndpoint.Path += fmt.Sprintf("/%d", buildID)
	body, err := c.get(ctx, projectEndpoint)
	if err != nil {
		return build, err
	}

	var circleCIBuild circleCIBuild
	if err := json.Unmarshal(body.Bytes(), &circleCIBuild); err != nil {
		return build, err
	}

	build, err = circleCIBuild.ToCacheBuild(c.accountID, repo)
	if err != nil {
		return build, err
	}

	/*s := utils.NullString{}
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
		s = utils.NullString{String: fullLog.String(), Valid: true}
	}*/

	return build, nil
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
		ID:              strconv.Itoa(b.ID),
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
