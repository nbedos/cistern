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
	"time"

	"github.com/nbedos/cistern/cache"
	"github.com/nbedos/cistern/utils"
)

type CircleCIClient struct {
	baseURL     url.URL
	httpClient  *http.Client
	rateLimiter <-chan time.Time
	token       string
	provider    cache.Provider
}

var CircleCIURL = url.URL{
	Scheme:  "https",
	Host:    "circleci.com",
	Path:    "api/v1.1",
	RawPath: "api/v1.1",
}

func NewCircleCIClient(id string, name string, token string, requestsPerSecond float64) CircleCIClient {
	rateLimit := time.Second / 10
	if requestsPerSecond > 0 {
		rateLimit = time.Second / time.Duration(requestsPerSecond)
	}

	if name == "" {
		name = "circleci"
	}

	return CircleCIClient{
		baseURL:     CircleCIURL,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		rateLimiter: time.Tick(rateLimit),
		token:       token,
		provider: cache.Provider{
			ID:   id,
			Name: name,
		},
	}
}

func (c CircleCIClient) get(ctx context.Context, resourceURL url.URL) (*bytes.Buffer, error) {
	if c.token != "" {
		parameters := resourceURL.Query()
		parameters.Add("circle-token", c.token)
		resourceURL.RawQuery = parameters.Encode()
	}

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

		// Remove the authentication token from the url so that it's not leaked in logs
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

func (c CircleCIClient) projectEndpoint(owner string, name string) url.URL {
	endpoint := c.baseURL
	pathFormat := "/project/gh/%s/%s"
	endpoint.Path += fmt.Sprintf(pathFormat, owner, name)
	endpoint.RawPath += fmt.Sprintf(pathFormat, url.PathEscape(owner), url.PathEscape(name))

	return endpoint
}

func (c CircleCIClient) ID() string {
	return c.provider.ID
}

func (c CircleCIClient) Host() string {
	return c.baseURL.Host
}

func (c CircleCIClient) Name() string {
	return c.provider.Name
}

func (c CircleCIClient) BuildFromURL(ctx context.Context, u string) (cache.Pipeline, error) {
	owner, repo, id, err := parseCircleCIWebURL(&c.baseURL, u)
	if err != nil {
		return cache.Pipeline{}, err
	}

	endPoint := c.projectEndpoint(owner, repo)
	return c.fetchPipeline(ctx, endPoint, id)
}

// Extract owner, repository and build ID from web url of build
func parseCircleCIWebURL(baseURL *url.URL, u string) (string, string, int, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", "", 0, err
	}

	if v.Hostname() != strings.TrimPrefix(baseURL.Hostname(), "api.") {
		return "", "", 0, cache.ErrUnknownPipelineURL
	}

	// url format: https://circleci.com/gh/nbedos/cistern/36
	cs := strings.Split(v.EscapedPath(), "/")
	if len(cs) < 5 {
		return "", "", 0, cache.ErrUnknownPipelineURL
	}

	owner, repo := cs[2], cs[3]
	id, err := strconv.Atoi(cs[4])
	if err != nil {
		return "", "", 0, err
	}

	return owner, repo, id, nil
}

func (c CircleCIClient) Log(ctx context.Context, step cache.Step) (string, error) {
	if step.Log.Key == "" {
		return "", cache.ErrNoLogHere
	}

	req, err := http.NewRequest("GET", step.Log.Key, nil)
	if err != nil {
		return "", err
	}
	req = req.WithContext(ctx)

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
			URL:     step.Log.Key,
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

func (c CircleCIClient) fetchPipeline(ctx context.Context, projectEndpoint url.URL, buildID int) (cache.Pipeline, error) {
	var err error
	var pipeline cache.Pipeline

	projectEndpoint.Path += fmt.Sprintf("/%d", buildID)
	body, err := c.get(ctx, projectEndpoint)
	if err != nil {
		return pipeline, err
	}

	var circleCIBuild circleCIBuild
	if err := json.Unmarshal(body.Bytes(), &circleCIBuild); err != nil {
		return pipeline, err
	}

	pipeline, err = circleCIBuild.toPipeline()
	if err != nil {
		return pipeline, err
	}

	return pipeline, nil
}

type circleCIAction struct {
	Index                int    `json:"index"`
	Name                 string `json:"name"`
	LogURL               string `json:"output_url"`
	Status               string `json:"status"`
	EndTime              string `json:"end_time"`
	Type                 string `json:"type"`
	StartTime            string `json:"start_time"`
	DurationMilliseconds int    `json:"run_time_millis"`
}

func (a circleCIAction) toStep(webURL utils.NullString) (cache.Step, error) {
	step := cache.Step{
		ID:    strconv.Itoa(a.Index),
		Type:  cache.StepTask,
		State: fromCircleCIStatus(a.Status),
		Name:  a.Name,
		Log: cache.Log{
			Key: a.LogURL,
		},
		WebURL: webURL,
	}

	if a.DurationMilliseconds > 0 {
		step.Duration = utils.NullDuration{
			Duration: time.Duration(a.DurationMilliseconds) * time.Millisecond,
			Valid:    true,
		}
	}

	var err error
	if step.StartedAt, err = utils.NullTimeFromString(a.StartTime); err != nil {
		return step, err
	}
	if step.FinishedAt, err = utils.NullTimeFromString(a.EndTime); err != nil {
		return step, err
	}

	return step, nil
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
	Status               string `json:"status"`
	Message              string `json:"subject"`
	CreatedAt            string `json:"queued_at"`
	CommittedAt          string `json:"committer_date"`
	DurationMilliseconds int    `json:"build_time_millis"`
	Steps                []struct {
		Actions []circleCIAction `json:"actions"`
	} `json:"steps"`
}

func (b circleCIBuild) toPipeline() (cache.Pipeline, error) {
	pipeline := cache.Pipeline{
		GitReference: cache.GitReference{
			SHA:   b.Sha,
			Ref:   "",
			IsTag: false,
		},
		Step: cache.Step{
			ID:    strconv.Itoa(b.ID),
			Name:  b.Workflows.JobName,
			Type:  cache.StepPipeline,
			State: fromCircleCIStatus(b.Status),
			WebURL: utils.NullString{
				String: b.WebURL,
				Valid:  true,
			},
		},
	}

	if b.DurationMilliseconds > 0 {
		pipeline.Duration = utils.NullDuration{
			Duration: time.Duration(b.DurationMilliseconds) * time.Millisecond,
			Valid:    true,
		}
	}

	var err error
	if pipeline.CreatedAt, err = time.Parse(time.RFC3339, b.CreatedAt); err != nil {
		return pipeline, err
	}
	if pipeline.StartedAt, err = utils.NullTimeFromString(b.StartedAt); err != nil {
		return pipeline, err
	}
	if pipeline.FinishedAt, err = utils.NullTimeFromString(b.FinishedAt); err != nil {
		return pipeline, err
	}

	updatedAt := utils.MaxNullTime(
		utils.NullTime{Time: pipeline.CreatedAt, Valid: true},
		pipeline.FinishedAt,
		pipeline.StartedAt)
	if !updatedAt.Valid {
		return pipeline, errors.New("updatedAt attribute cannot be null")
	}
	pipeline.UpdatedAt = updatedAt.Time

	if pipeline.IsTag = b.Tag != ""; pipeline.IsTag {
		pipeline.Ref = b.Tag
	} else {
		pipeline.Ref = b.Branch
	}

	for i, buildStep := range b.Steps {
		// TODO If there is more than one action for the step, the step should be made a Stage
		for j, action := range buildStep.Actions {
			task, err := action.toStep(pipeline.WebURL)
			if err != nil {
				return pipeline, err
			}
			task.ID = fmt.Sprintf("%d.%d", i, j)
			task.CreatedAt = pipeline.CreatedAt
			pipeline.Children = append(pipeline.Children, task)
		}
	}

	return pipeline, nil
}

func fromCircleCIStatus(status string) cache.State {
	switch status {
	case "canceled", "cancelled":
		return cache.Canceled
	case "infrastructure_fail", "timedout", "failed":
		return cache.Failed
	case "running":
		return cache.Running
	case "queued", "scheduled":
		return cache.Pending
	case "not_running", "not_run":
		return cache.Skipped
	case "success":
		return cache.Passed
	case "retried", "no_tests", "fixed":
		// What do those mean?
		return cache.Unknown
	default:
		return cache.Unknown
	}
}
