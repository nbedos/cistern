package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nbedos/cistern/utils"
)

func fromTravisState(s string) State {
	switch strings.ToLower(s) {
	case "created", "queued", "received":
		return Pending
	case "started":
		return Running
	case "canceled":
		return Canceled
	case "passed":
		return Passed
	case "failed", "errored":
		return Failed
	case "skipped":
		return Skipped
	default:
		return Unknown
	}
}

type travisBuild struct {
	APIURL     string `json:"@href"`
	ID         int
	State      string
	Number     string
	EventType  string `json:"event_type"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	UpdatedAt  string `json:"updated_at"`
	Duration   int
	Tag        struct {
		Name string
	}
	Branch struct {
		Name string
	}
	Commit struct {
		Sha string `json:"sha"`
	}
	CreatedBy struct {
		Login string
	} `json:"created_by"`
	Jobs []travisJob
}

func (b travisBuild) toPipeline(webURL string) (pipeline Pipeline, err error) {
	pipeline = Pipeline{
		Number: b.Number,
		GitReference: GitReference{
			SHA:   b.Commit.Sha,
			Ref:   "",
			IsTag: b.Tag.Name != "",
		},
		Step: Step{
			ID:    strconv.Itoa(b.ID),
			State: fromTravisState(b.State),
			Duration: utils.NullDuration{
				Duration: time.Duration(b.Duration) * time.Second,
				Valid:    b.Duration > 0,
			},
		},
	}

	if pipeline.StartedAt, err = utils.NullTimeFromString(b.StartedAt); err != nil {
		return pipeline, err
	}
	if pipeline.FinishedAt, err = utils.NullTimeFromString(b.FinishedAt); err != nil {
		return pipeline, err
	}
	if pipeline.UpdatedAt, err = time.Parse(time.RFC3339, b.UpdatedAt); err != nil {
		return pipeline, err
	}

	if b.Tag.Name == "" {
		pipeline.Ref = b.Branch.Name
	} else {
		pipeline.Ref = b.Tag.Name
	}

	pipeline.WebURL = utils.NullString{
		String: fmt.Sprintf("%s/builds/%d", webURL, b.ID),
		Valid:  true,
	}

	sort.Slice(b.Jobs, func(i, j int) bool {
		return b.Jobs[i].Stage.ID < b.Jobs[j].Stage.ID || (b.Jobs[i].Stage.ID == b.Jobs[j].Stage.ID && b.Jobs[i].ID < b.Jobs[j].ID)
	})
	for i, travisJob := range b.Jobs {
		job, err := travisJob.toStep(webURL)
		if err != nil {
			return pipeline, err
		}

		if i == 0 {
			pipeline.CreatedAt = job.CreatedAt
		} else {
			if job.CreatedAt.Before(pipeline.CreatedAt) {
				pipeline.CreatedAt = job.CreatedAt
			}
		}

		if travisJob.Stage.ID != 0 {
			s, err := travisJob.Stage.toStep(pipeline.WebURL.String)
			if err != nil {
				return pipeline, err
			}

			isNewStage := false
			if len(pipeline.Children) == 0 || pipeline.Children[len(pipeline.Children)-1].ID != s.ID {
				pipeline.Children = append(pipeline.Children, s)
				isNewStage = true
			}

			stage := &pipeline.Children[len(pipeline.Children)-1]
			stage.Children = append(stage.Children, job)
			if isNewStage {
				stage.CreatedAt = job.CreatedAt
			} else {
				if job.CreatedAt.Before(stage.CreatedAt) {
					stage.CreatedAt = job.CreatedAt
				}
			}
		} else {
			pipeline.Children = append(pipeline.Children, job)
		}
	}

	return pipeline, nil
}

type travisJob struct {
	ID           int
	State        string
	StartedAt    string `json:"started_at"`
	CreatedAt    string `json:"created_at"`
	FinishedAt   string `json:"finished_at"`
	Stage        travisStage
	AllowFailure bool   `json:"allow_failure"`
	Number       string `json:"number"`
	Log          string
	Config       travisJobConfig
}

type travisJobConfig map[string]interface{}

func (c travisJobConfig) String() string {
	var os, dist, language, compiler string

	language, ok := c["language"].(string)
	if language != "" && ok {
		var s string
		switch version := c[language].(type) {
		case int64:
			s = strconv.Itoa(int(version))
		case float64:
			s = strconv.FormatFloat(version, 'f', -1, 64)
		case string:
			s = version
		}
		if s != "" {
			language = fmt.Sprintf("%s %s", language, s)
		}
	}

	compiler, _ = c["compiler"].(string)
	os, _ = c["os"].(string)
	dist, _ = c["dist"].(string)

	values := []string{os, dist, language, compiler}

	nonEmptyValues := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			nonEmptyValues = append(nonEmptyValues, value)
		}
	}
	return strings.Join(nonEmptyValues, ", ")
}

func (j travisJob) toStep(webURL string) (Step, error) {
	var err error

	name, _ := j.Config["name"].(string)
	if name == "" {
		name = j.Config.String()
	}

	job := Step{
		ID:    strconv.Itoa(j.ID),
		Type:  StepJob,
		State: fromTravisState(j.State),
		Name:  name,
		Log: Log{
			Content: utils.NullString{String: j.Log, Valid: j.Log != ""},
		},
		WebURL: utils.NullString{
			String: fmt.Sprintf("%s/jobs/%d", webURL, j.ID),
			Valid:  true,
		},
		AllowFailure: j.AllowFailure,
	}

	job.CreatedAt, err = time.Parse(time.RFC3339, j.CreatedAt)
	if err != nil {
		return job, err
	}

	ats := map[string]*utils.NullTime{
		j.StartedAt:  &job.StartedAt,
		j.FinishedAt: &job.FinishedAt,
	}
	for s, t := range ats {
		*t, err = utils.NullTimeFromString(s)
		if err != nil {
			return Step{}, err
		}
	}

	job.Duration = utils.NullSub(job.FinishedAt, job.StartedAt)
	job.Duration.Duration.Truncate(time.Second)

	return job, nil
}

type travisStage struct {
	ID         int    `json:"id"`
	Number     int    `json:"number"`
	Name       string `json:"name"`
	State      string `json:"state"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

func (s travisStage) toStep(webURL string) (Step, error) {
	stage := Step{
		ID:    strconv.Itoa(s.ID),
		Type:  StepStage,
		Name:  s.Name,
		State: fromTravisState(s.State),
		WebURL: utils.NullString{
			Valid:  true,
			String: webURL,
		},
	}

	var err error
	if stage.StartedAt, err = utils.NullTimeFromString(s.StartedAt); err != nil {
		return stage, err
	}

	if stage.FinishedAt, err = utils.NullTimeFromString(s.FinishedAt); err != nil {
		return stage, err
	}

	stage.Duration = utils.NullSub(stage.FinishedAt, stage.StartedAt)

	return stage, nil
}

type TravisClient struct {
	baseURL            url.URL
	httpClient         *http.Client
	rateLimiter        <-chan time.Time
	logBackoffInterval time.Duration
	buildsPageSize     int
	token              string
	provider           Provider
}

var TravisOrgURL = url.URL{Scheme: "https", Host: "api.travis-ci.org"}
var TravisComURL = url.URL{Scheme: "https", Host: "api.travis-ci.com"}

func NewTravisClient(id string, name string, token string, URL string, requestsPerSecond float64) (TravisClient, error) {
	rateLimit := time.Second / 20
	if requestsPerSecond > 0 {
		rateLimit = time.Second / time.Duration(requestsPerSecond)
	}

	var err error
	var u *url.URL
	switch strings.ToLower(URL) {
	case "org":
		u = &TravisOrgURL
	case "com":
		u = &TravisComURL
	default:
		if u, err = url.Parse(URL); err != nil {
			return TravisClient{}, err
		}
	}

	return TravisClient{
		baseURL:            *u,
		httpClient:         &http.Client{Timeout: 10 * time.Second},
		rateLimiter:        time.Tick(rateLimit),
		logBackoffInterval: 10 * time.Second,
		token:              token,
		provider: Provider{
			ID:   id,
			Name: name,
		},
		buildsPageSize: 10,
	}, nil
}

func (c TravisClient) ID() string {
	return c.provider.ID
}

func (c TravisClient) Host() string {
	return c.baseURL.Host
}

func (c TravisClient) Name() string {
	return c.provider.Name
}

func (c TravisClient) BuildFromURL(ctx context.Context, u string) (Pipeline, error) {
	owner, repo, id, err := parseTravisWebURL(&c.baseURL, u)
	if err != nil {
		return Pipeline{}, err
	}

	slug := fmt.Sprintf("%s/%s", owner, repo)
	return c.fetchPipeline(ctx, slug, id)
}

// Extract owner, repository and build ID from web url of build
func parseTravisWebURL(baseURL *url.URL, u string) (string, string, string, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", "", "", err
	}

	if v.Hostname() != strings.TrimPrefix(baseURL.Hostname(), "api.") {
		return "", "", "", ErrUnknownPipelineURL
	}

	// url format: https://travis-ci.org/nbedos/termtosvg/builds/612815758
	cs := strings.Split(v.EscapedPath(), "/")
	if len(cs) < 5 || cs[3] != "builds" {
		return "", "", "", ErrUnknownPipelineURL
	}

	owner, repo, id := cs[1], cs[2], cs[4]
	return owner, repo, id, nil
}

func (c TravisClient) webURL(slug string) (url.URL, error) {
	var err error
	webURL := c.baseURL
	webURL.Host = strings.TrimPrefix(webURL.Host, "api.")
	webURL.Path += fmt.Sprintf("/%s", slug)

	return webURL, err
}

// Rate-limited HTTP GET request with custom headers
func (c TravisClient) get(ctx context.Context, method string, resourceURL url.URL) (*bytes.Buffer, error) {
	req, err := http.NewRequest(method, resourceURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Travis-API-Version", "3")
	if c.token != "" {
		req.Header.Add("Authorization", fmt.Sprintf("token %s", c.token))
	}
	req = req.WithContext(ctx)

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() // FIXME return error

	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = HTTPError{
			Method:  req.Method,
			URL:     req.URL.String(),
			Status:  resp.StatusCode,
			Message: body.String(),
		}
	}

	return body, err
}

type HTTPError struct {
	Method  string
	URL     string
	Status  int
	Message string
}

func (err HTTPError) Error() string {
	return fmt.Sprintf("%q %s returned an error: status %d (%s)",
		err.Method, err.URL, err.Status, err.Message)
}

func (c TravisClient) fetchPipeline(ctx context.Context, slug string, buildID string) (Pipeline, error) {
	buildURL := c.baseURL
	buildURL.Path += fmt.Sprintf("/build/%s", buildID)
	parameters := buildURL.Query()
	parameters.Add("include", "build.jobs,build.commit,job.config")
	buildURL.RawQuery = parameters.Encode()

	body, err := c.get(ctx, "GET", buildURL)
	if err != nil {
		return Pipeline{}, err
	}

	var build travisBuild
	err = json.Unmarshal(body.Bytes(), &build)

	webURL, err := c.webURL(slug)
	if err != nil {
		return Pipeline{}, err
	}
	pipeline, err := build.toPipeline(webURL.String())
	if err != nil {
		return Pipeline{}, err
	}

	return pipeline, nil
}

func (c TravisClient) Log(ctx context.Context, step Step) (string, error) {
	if step.Type != StepJob {
		return "", ErrNoLogHere
	}

	var reqURL = c.baseURL
	reqURL.Path += fmt.Sprintf("/job/%s/log", url.PathEscape(step.ID))

	body, err := c.get(ctx, "GET", reqURL)
	if err != nil {
		return "", err
	}

	var log struct {
		Content string `json:"content"`
	}

	if err := json.Unmarshal(body.Bytes(), &log); err != nil {
		return "", err
	}

	return log.Content, nil
}
