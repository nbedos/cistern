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

	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
)

type travisRepository struct {
	ID    int
	Slug  string
	Owner struct {
		Login string
	}
	Name string
}

func fromTravisState(s string) cache.State {
	switch strings.ToLower(s) {
	case "created", "queued", "received":
		return cache.Pending
	case "started":
		return cache.Running
	case "canceled":
		return cache.Canceled
	case "passed":
		return cache.Passed
	case "failed", "errored":
		return cache.Failed
	case "skipped":
		return cache.Skipped
	default:
		return cache.Unknown
	}
}

func (r travisRepository) toCacheRepository() cache.Repository {
	return cache.Repository{
		URL:   fmt.Sprintf("https://github.com/%v", r.Slug), // FIXME
		Name:  r.Name,
		Owner: r.Owner.Login,
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
	Repository travisRepository
	CreatedBy  struct {
		Login string
	} `json:"created_by"`
	Jobs []travisJob
}

func (b travisBuild) toPipeline(repository *cache.Repository, webURL string) (pipeline cache.Pipeline, err error) {
	pipeline = cache.Pipeline{
		Number:     b.Number,
		Repository: repository,
		GitReference: cache.GitReference{
			SHA:   b.Commit.Sha,
			Ref:   "",
			IsTag: b.Tag.Name != "",
		},
		Step: cache.Step{
			ID:        strconv.Itoa(b.ID),
			State:     fromTravisState(b.State),
			CreatedAt: utils.NullTime{}, // FIXME We need this
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
	for _, travisJob := range b.Jobs {
		job, err := travisJob.toStep(webURL)
		if err != nil {
			return pipeline, err
		}
		pipeline.CreatedAt = utils.MinNullTime(pipeline.CreatedAt, job.CreatedAt)

		if travisJob.Stage.ID != 0 {
			s, err := travisJob.Stage.toStep(pipeline.WebURL.String)
			if err != nil {
				return pipeline, err
			}

			if len(pipeline.Children) == 0 || pipeline.Children[len(pipeline.Children)-1].ID != s.ID {
				pipeline.Children = append(pipeline.Children, s)
			}

			stage := &pipeline.Children[len(pipeline.Children)-1]
			stage.Children = append(stage.Children, job)
			stage.CreatedAt = utils.MinNullTime(stage.CreatedAt, job.CreatedAt)
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

func (j travisJob) toStep(webURL string) (cache.Step, error) {
	var err error

	name, _ := j.Config["name"].(string)
	if name == "" {
		name = j.Config.String()
	}

	job := cache.Step{
		ID:    strconv.Itoa(j.ID),
		Type:  cache.StepJob,
		State: fromTravisState(j.State),
		Name:  name,
		Log:   utils.NullString{String: j.Log, Valid: j.Log != ""},
		WebURL: utils.NullString{
			String: fmt.Sprintf("%s/jobs/%d", webURL, j.ID),
			Valid:  true,
		},
		AllowFailure: j.AllowFailure,
	}

	ats := map[string]*utils.NullTime{
		j.CreatedAt:  &job.CreatedAt,
		j.StartedAt:  &job.StartedAt,
		j.FinishedAt: &job.FinishedAt,
	}
	for s, t := range ats {
		*t, err = utils.NullTimeFromString(s)
		if err != nil {
			return cache.Step{}, err
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

func (s travisStage) toStep(webURL string) (cache.Step, error) {
	stage := cache.Step{
		ID:    strconv.Itoa(s.ID),
		Type:  cache.StepStage,
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
	provider           cache.Provider
}

var TravisOrgURL = url.URL{Scheme: "https", Host: "api.travis-ci.org"}
var TravisComURL = url.URL{Scheme: "https", Host: "api.travis-ci.com"}

func NewTravisClient(id string, name string, token string, URL url.URL, rateLimit time.Duration) TravisClient {
	return TravisClient{
		baseURL:            URL,
		httpClient:         &http.Client{Timeout: 10 * time.Second},
		rateLimiter:        time.Tick(rateLimit),
		logBackoffInterval: 10 * time.Second,
		token:              token,
		provider: cache.Provider{
			ID:   id,
			Name: name,
		},
		buildsPageSize: 10,
	}
}

func (c TravisClient) ID() string {
	return c.provider.ID
}

func (c TravisClient) Name() string {
	return c.provider.Name
}

func (c TravisClient) BuildFromURL(ctx context.Context, u string) (cache.Pipeline, error) {
	owner, repo, id, err := parseTravisWebURL(&c.baseURL, u)
	if err != nil {
		return cache.Pipeline{}, err
	}

	repository, err := c.repository(ctx, fmt.Sprintf("%s/%s", owner, repo))
	if err != nil {
		return cache.Pipeline{}, err
	}

	return c.fetchPipeline(ctx, &repository, id)
}

// Extract owner, repository and build ID from web URL of build
func parseTravisWebURL(baseURL *url.URL, u string) (string, string, string, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", "", "", err
	}

	if v.Hostname() != strings.TrimPrefix(baseURL.Hostname(), "api.") {
		return "", "", "", cache.ErrUnknownPipelineURL
	}

	// URL format: https://travis-ci.org/nbedos/termtosvg/builds/612815758
	cs := strings.Split(v.EscapedPath(), "/")
	if len(cs) < 5 || cs[3] != "builds" {
		return "", "", "", cache.ErrUnknownPipelineURL
	}

	owner, repo, id := cs[1], cs[2], cs[4]
	return owner, repo, id, nil
}

func (c TravisClient) repository(ctx context.Context, slug string) (cache.Repository, error) {
	var reqURL = c.baseURL
	buildPathFormat := "/repo/%s"
	reqURL.Path += fmt.Sprintf(buildPathFormat, slug)
	reqURL.RawPath += fmt.Sprintf(buildPathFormat, url.PathEscape(slug))

	body, err := c.get(ctx, "GET", reqURL)
	if err != nil {
		if err, ok := err.(HTTPError); ok && err.Status == 404 {
			return cache.Repository{}, cache.ErrUnknownRepositoryURL
		}
		return cache.Repository{}, err
	}

	travisRepo := travisRepository{}
	if err = json.Unmarshal(body.Bytes(), &travisRepo); err != nil {
		return cache.Repository{}, err
	}

	return travisRepo.toCacheRepository(), nil
}

func (c TravisClient) webURL(repository cache.Repository) (url.URL, error) {
	var err error
	webURL := c.baseURL
	webURL.Host = strings.TrimPrefix(webURL.Host, "api.")
	webURL.Path += fmt.Sprintf("/%s", repository.Slug())

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

func (c TravisClient) fetchPipeline(ctx context.Context, repository *cache.Repository, buildID string) (cache.Pipeline, error) {
	buildURL := c.baseURL
	buildURL.Path += fmt.Sprintf("/build/%s", buildID)
	parameters := buildURL.Query()
	parameters.Add("include", "build.jobs,build.commit,job.config")
	buildURL.RawQuery = parameters.Encode()

	body, err := c.get(ctx, "GET", buildURL)
	if err != nil {
		return cache.Pipeline{}, err
	}

	var build travisBuild
	err = json.Unmarshal(body.Bytes(), &build)

	webURL, err := c.webURL(*repository)
	if err != nil {
		return cache.Pipeline{}, err
	}
	pipeline, err := build.toPipeline(repository, webURL.String())
	if err != nil {
		return cache.Pipeline{}, err
	}

	return pipeline, nil
}

func (c TravisClient) Log(ctx context.Context, repository cache.Repository, step cache.Step) (string, error) {
	if step.Type != cache.StepJob {
		return "", cache.ErrNoLogHere
	}

	var reqURL = c.baseURL
	reqURL.Path += fmt.Sprintf("/job/%s/log", step.ID)

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
