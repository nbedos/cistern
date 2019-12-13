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

	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
)

type AppVeyorClient struct {
	url         url.URL
	client      *http.Client
	rateLimiter <-chan time.Time
	token       string
	provider    cache.Provider
}

var appVeyorURL = url.URL{
	Scheme:  "https",
	Host:    "ci.appveyor.com",
	Path:    "/api",
	RawPath: "/api",
}

func NewAppVeyorClient(id string, name string, token string, rateLimit time.Duration) AppVeyorClient {
	return AppVeyorClient{
		url:         appVeyorURL,
		client:      &http.Client{Timeout: 10 * time.Second},
		rateLimiter: time.Tick(rateLimit),
		token:       token,
		provider: cache.Provider{
			ID:   id,
			Name: name,
		},
	}
}

func (c AppVeyorClient) ID() string {
	return c.provider.ID
}

func (c AppVeyorClient) Name() string {
	return c.provider.Name
}

func (c AppVeyorClient) Log(ctx context.Context, repository cache.Repository, jobID string) (string, error) {
	endpoint := c.url
	endpoint.Path += fmt.Sprintf("/buildjobs/%s/log", jobID)
	endpoint.RawPath += fmt.Sprintf("/buildjobs/%s/log", url.PathEscape(jobID))

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

func (c AppVeyorClient) BuildFromURL(ctx context.Context, u string) (cache.Pipeline, error) {
	owner, repo, id, err := parseAppVeyorURL(u)
	if err != nil {
		return cache.Pipeline{}, err
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
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.token))
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

func (c AppVeyorClient) fetchPipeline(ctx context.Context, owner string, repoName string, id int) (cache.Pipeline, error) {
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
		return cache.Pipeline{}, err
	}

	if len(b.Builds) != 1 {
		return cache.Pipeline{}, fmt.Errorf("found no build with id %d", id)
	}
	if b.Builds[0].ID != id {
		return cache.Pipeline{}, fmt.Errorf("expected build #%d but got %d", id, b.Builds[0].ID)
	}
	version := b.Builds[0].Version

	repository := cache.Repository{
		URL:   "",
		Owner: b.Project.Owner,
		Name:  b.Project.Name,
	}

	endpoint := c.url
	pathFormat := "/projects/%s/%s/build/%s"
	endpoint.Path += fmt.Sprintf(pathFormat, owner, repoName, version)
	endpoint.RawPath += fmt.Sprintf(pathFormat, url.PathEscape(owner),
		url.PathEscape(repoName), url.PathEscape(version))
	var bVersion struct {
		Build appVeyorBuild `json:"build"`
	}
	if err := c.getJSON(ctx, endpoint, &bVersion); err != nil {
		return cache.Pipeline{}, err
	}

	return bVersion.Build.toCachePipeline(&repository)
}

// Extract owner, repository and build ID from web URL of build
func parseAppVeyorURL(u string) (string, string, int, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", "", 0, err
	}

	if !strings.HasSuffix(v.Hostname(), "appveyor.com") {
		return "", "", 0, cache.ErrUnknownPipelineURL
	}

	// URL format: https://ci.appveyor.com/project/nbedos/citop/builds/29070120
	cs := strings.Split(v.EscapedPath(), "/")
	if len(cs) < 6 || cs[1] != "project" || cs[4] != "builds" {
		return "", "", 0, cache.ErrUnknownPipelineURL
	}

	owner, repo := cs[2], cs[3]
	id, err := strconv.Atoi(cs[5])
	if err != nil {
		return "", "", 0, err
	}
	return owner, repo, id, nil
}

func fromAppVeyorState(s string) cache.State {
	switch strings.ToLower(s) {
	case "queued", "received", "starting":
		return cache.Pending
	case "running":
		return cache.Running
	case "success":
		return cache.Passed
	case "failed":
		return cache.Failed
	case "cancelled":
		return cache.Canceled
	default:
		return cache.Unknown
	}
}

type appVeyorBuild struct {
	ID          int           `json:"buildId"`
	Jobs        []appVeyorJob `json:"jobs"`
	Number      int           `json:"buildNumber"`
	Version     string        `json:"version"`
	Message     string        `json:"message"`
	Branch      string        `json:"branch"`
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

func (b appVeyorBuild) toCachePipeline(repo *cache.Repository) (cache.Pipeline, error) {
	pipeline := cache.Pipeline{
		Repository: repo,
		GitReference: cache.GitReference{
			SHA:   b.Sha,
			Ref:   b.Branch,
			IsTag: b.IsTag,
		},
		Step: cache.Step{
			ID:    strconv.Itoa(b.ID),
			Type:  cache.StepPipeline,
			State: fromAppVeyorState(b.Status),
		},
	}
	var err error
	pipeline.CreatedAt, err = utils.NullTimeFromString(b.CreatedAt)
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
		// Best effort since it sometimes happens that UpdatedAt is null but another date is
		// available
		nullUpdateAt := utils.MinNullTime(pipeline.CreatedAt, pipeline.StartedAt, pipeline.FinishedAt)
		if !nullUpdateAt.Valid {
			return pipeline, err
		}
		pipeline.UpdatedAt = nullUpdateAt.Time
	}

	pipeline.Duration = utils.NullSub(pipeline.FinishedAt, pipeline.StartedAt)
	pipeline.WebURL = utils.NullString{
		String: fmt.Sprintf("https://ci.appveyor.com/project/%s/%s/builds/%d",
			url.PathEscape(repo.Owner), url.PathEscape(repo.Name), b.ID),
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

func (j appVeyorJob) toCacheStep(id string, buildURL string) (cache.Step, error) {
	if id == "" {
		return cache.Step{}, errors.New("job id must not be the empty string")
	}
	step := cache.Step{
		ID:    id,
		Type:  cache.StepJob,
		State: fromAppVeyorState(j.Status),
		Name:  j.Name,
		WebURL: utils.NullString{
			String: fmt.Sprintf("%s/job/%s", buildURL, url.PathEscape(j.ID)),
			Valid:  true,
		},
		AllowFailure: j.AllowFailure,
	}

	var err error
	step.CreatedAt, err = utils.NullTimeFromString(j.CreatedAt)
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
