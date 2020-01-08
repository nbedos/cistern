package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nbedos/cistern/utils"
)

type AppVeyorClient struct {
	url         url.URL
	client      *http.Client
	rateLimiter <-chan time.Time
	token       string
	provider    Provider
}

var appVeyorURL = url.URL{
	Scheme:  "https",
	Host:    "ci.appveyor.com",
	Path:    "/api",
	RawPath: "/api",
}

func NewAppVeyorClient(id string, name string, token string, requestsPerSecond float64) AppVeyorClient {
	rateLimit := time.Second / 10
	if requestsPerSecond > 0 {
		rateLimit = time.Second / time.Duration(requestsPerSecond)
	}

	return AppVeyorClient{
		url:         appVeyorURL,
		client:      &http.Client{Timeout: 10 * time.Second},
		rateLimiter: time.Tick(rateLimit),
		token:       token,
		provider: Provider{
			ID:   id,
			Name: name,
		},
	}
}

func (c AppVeyorClient) ID() string {
	return c.provider.ID
}

func (c AppVeyorClient) Host() string {
	return c.url.Host
}

func (c AppVeyorClient) Name() string {
	return c.provider.Name
}

func (c AppVeyorClient) Log(ctx context.Context, step Step) (string, error) {
	if step.Type != StepJob {
		return "", ErrNoLogHere
	}

	endpoint := c.url
	endpoint.Path += fmt.Sprintf("/buildjobs/%s/log", url.PathEscape(step.ID))

	body, err := c.get(ctx, endpoint)
	if err != nil {
		return "", err
	}
	defer func() {
		if errClose := body.Close(); err == nil {
			err = errClose
		}
	}()

	log, err := ioutil.ReadAll(body)
	if err != nil {
		return "", err
	}

	return string(log), err
}

func (c AppVeyorClient) BuildFromURL(ctx context.Context, u string) (Pipeline, error) {
	owner, repo, id, err := parseAppVeyorURL(u)
	if err != nil {
		return Pipeline{}, err
	}

	return c.fetchPipeline(ctx, owner, repo, id)
}

func (c AppVeyorClient) getJSON(ctx context.Context, u url.URL, v interface{}) error {
	r, err := c.get(ctx, u)
	if err != nil {
		return err
	}
	defer func() {
		if errClose := r.Close(); err == nil {
			err = errClose
		}
	}()

	err = json.NewDecoder(r).Decode(v)
	return err
}

func (c AppVeyorClient) get(ctx context.Context, u url.URL) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.token))
	}
	req = req.WithContext(ctx)

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			message = nil
		}
		resp.Body.Close()
		return nil, HTTPError{
			Method:  req.Method,
			URL:     u.String(),
			Status:  resp.StatusCode,
			Message: string(message),
		}
	}

	return resp.Body, err
}

func (c AppVeyorClient) fetchPipeline(ctx context.Context, owner string, repoName string, id int) (Pipeline, error) {
	// We only have the build ID and need a build object. We have to query two endpoints:
	// 		1. /projects/owner/repoName/history with startBuildId = id gives us a build object with
	//      an empty job list but with a version number
	//      2. /projects/owner/repoName/build/<version> using the version number from the last call
	//      gives us a build with a complete job list
	history := c.url
	historyFormat := "/projects/%s/%s/history"
	history.Path += fmt.Sprintf(historyFormat, owner, repoName)
	history.RawPath += fmt.Sprintf(historyFormat, url.PathEscape(owner), url.PathEscape(repoName))
	params := history.Query()
	params.Add("recordsNumber", "1")
	params.Add("startBuildId", strconv.Itoa(id+1))
	history.RawQuery = params.Encode()

	var b struct {
		Project struct {
			ID    int    `json:"projectId"`
			Owner string `json:"accountName"`
			Name  string `json:"name"`
		}
		Builds []appVeyorBuild `json:"builds"`
	}
	if err := c.getJSON(ctx, history, &b); err != nil {
		return Pipeline{}, err
	}

	if len(b.Builds) != 1 {
		return Pipeline{}, fmt.Errorf("found no build with id %d", id)
	}
	if b.Builds[0].ID != id {
		return Pipeline{}, fmt.Errorf("expected build #%d but got %d", id, b.Builds[0].ID)
	}
	version := b.Builds[0].Version

	endpoint := c.url
	pathFormat := "/projects/%s/%s/build/%s"
	endpoint.Path += fmt.Sprintf(pathFormat, owner, repoName, version)
	endpoint.RawPath += fmt.Sprintf(pathFormat, url.PathEscape(owner),
		url.PathEscape(repoName), url.PathEscape(version))
	var bVersion struct {
		Build appVeyorBuild `json:"build"`
	}
	if err := c.getJSON(ctx, endpoint, &bVersion); err != nil {
		return Pipeline{}, err
	}

	return bVersion.Build.toCachePipeline(b.Project.Owner, b.Project.Name)
}

// Extract owner, repository and build ID from web url of build
func parseAppVeyorURL(u string) (string, string, int, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", "", 0, err
	}

	if !strings.HasSuffix(v.Hostname(), "appveyor.com") {
		return "", "", 0, ErrUnknownPipelineURL
	}

	// url format: https://ci.appveyor.com/project/nbedos/cistern/builds/29070120
	cs := strings.Split(v.EscapedPath(), "/")
	if len(cs) < 6 || cs[1] != "project" || cs[4] != "builds" {
		return "", "", 0, ErrUnknownPipelineURL
	}

	owner, repo := cs[2], cs[3]
	id, err := strconv.Atoi(cs[5])
	if err != nil {
		return "", "", 0, err
	}
	return owner, repo, id, nil
}

func fromAppVeyorState(s string) State {
	switch strings.ToLower(s) {
	case "queued", "received", "starting":
		return Pending
	case "running":
		return Running
	case "success":
		return Passed
	case "failed":
		return Failed
	case "cancelled":
		return Canceled
	default:
		return Unknown
	}
}

type appVeyorBuild struct {
	ID          int           `json:"buildId"`
	Jobs        []appVeyorJob `json:"jobs"`
	Number      int           `json:"buildNumber"`
	Version     string        `json:"version"`
	Message     string        `json:"message"`
	Branch      string        `json:"branch"`
	Tag         string        `json:"tag"`
	IsTag       bool          `json:"isTag"`
	Sha         string        `json:"commitId"`
	Author      string        `json:"authorUsername"`
	CommittedAt string        `json:"committed"`
	Status      string        `json:"status"`
	CreatedAt   string        `json:"created"`
	StartedAt   string        `json:"started"`
	FinishedAt  string        `json:"finished"`
	UpdatedAt   string        `json:"updated"`
}

func (b appVeyorBuild) toCachePipeline(owner string, repository string) (Pipeline, error) {
	ref := b.Branch
	if b.IsTag {
		ref = b.Tag
	}

	pipeline := Pipeline{
		Number: strconv.Itoa(b.Number),
		GitReference: GitReference{
			SHA:   b.Sha,
			Ref:   ref,
			IsTag: b.IsTag,
		},
		Step: Step{
			ID:    strconv.Itoa(b.ID),
			Type:  StepPipeline,
			State: fromAppVeyorState(b.Status),
		},
	}
	var err error
	pipeline.CreatedAt, err = time.Parse(time.RFC3339, b.CreatedAt)
	if err != nil {
		return pipeline, err
	}
	pipeline.StartedAt, err = utils.NullTimeFromString(b.StartedAt)
	if err != nil {
		return pipeline, err
	}
	pipeline.FinishedAt, err = utils.NullTimeFromString(b.FinishedAt)
	if err != nil {
		return pipeline, err
	}
	if pipeline.UpdatedAt, err = time.Parse(time.RFC3339, b.UpdatedAt); err != nil {
		// Best effort since it sometimes happens that updatedAt is null but another date is
		// available
		nullUpdateAt := utils.MinNullTime(
			utils.NullTime{Time: pipeline.CreatedAt, Valid: true},
			pipeline.StartedAt,
			pipeline.FinishedAt)
		if !nullUpdateAt.Valid {
			return pipeline, err
		}
		pipeline.UpdatedAt = nullUpdateAt.Time
	}

	pipeline.Duration = utils.NullSub(pipeline.FinishedAt, pipeline.StartedAt)
	pipeline.WebURL = utils.NullString{
		String: fmt.Sprintf("https://ci.appveyor.com/project/%s/%s/builds/%d",
			url.PathEscape(owner), url.PathEscape(repository), b.ID),
		Valid: true,
	}

	for _, job := range b.Jobs {
		j, err := job.toCacheStep(job.ID, pipeline.WebURL.String)
		if err != nil {
			return pipeline, err
		}
		pipeline.Children = append(pipeline.Children, j)
	}

	return pipeline, nil
}

type appVeyorJob struct {
	ID           string `json:"jobId"`
	Name         string `json:"name"`
	AllowFailure bool   `json:"allowFailure"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created"`
	StartedAt    string `json:"started"`
	FinishedAt   string `json:"finished"`
}

func (j appVeyorJob) toCacheStep(id string, buildURL string) (Step, error) {
	if id == "" {
		return Step{}, errors.New("job id must not be the empty string")
	}
	step := Step{
		ID:    id,
		Type:  StepJob,
		State: fromAppVeyorState(j.Status),
		Name:  j.Name,
		WebURL: utils.NullString{
			String: fmt.Sprintf("%s/job/%s", buildURL, url.PathEscape(j.ID)),
			Valid:  true,
		},
		AllowFailure: j.AllowFailure,
	}

	var err error
	step.CreatedAt, err = time.Parse(time.RFC3339, j.CreatedAt)
	if err != nil {
		return step, err
	}
	step.StartedAt, err = utils.NullTimeFromString(j.StartedAt)
	if err != nil {
		return step, err
	}
	step.FinishedAt, err = utils.NullTimeFromString(j.FinishedAt)
	if err != nil {
		return step, err
	}
	step.Duration = utils.NullSub(step.FinishedAt, step.StartedAt)

	return step, nil
}
