package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

func (r travisRepository) toCacheRepository(provider cache.Provider) cache.Repository {
	return cache.Repository{
		ID:       r.ID,
		Provider: provider,
		URL:      fmt.Sprintf("https://github.com/%v", r.Slug), // FIXME
		Name:     r.Name,
		Owner:    r.Owner.Login,
	}
}

type travisCommit struct {
	ID      string `json:"sha"`
	Message string
	Author  struct {
		Name string
	}
	Date string `json:"committed_at"`
}

func (c travisCommit) toCacheCommit() (cache.Commit, error) {
	committedAt, err := utils.NullTimeFromString(c.Date)
	if err != nil {
		return cache.Commit{}, err
	}

	return cache.Commit{
		Sha:     c.ID,
		Message: c.Message,
		Date:    committedAt,
	}, nil
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
	Commit     travisCommit
	Repository travisRepository
	CreatedBy  struct {
		Login string
	} `json:"created_by"`
	Jobs []travisJob
}

func (b travisBuild) toCacheBuild(repository *cache.Repository, webURL string) (build cache.Build, err error) {
	commit, err := b.Commit.toCacheCommit()
	if err != nil {
		return build, err
	}

	build = cache.Build{
		Repository: repository,
		ID:         strconv.Itoa(b.ID),
		Commit:     commit,
		IsTag:      b.Tag.Name != "",
		State:      fromTravisState(b.State),
		CreatedAt:  utils.NullTime{}, // FIXME We need this
		Duration: utils.NullDuration{
			Duration: time.Duration(b.Duration) * time.Second,
			Valid:    b.Duration > 0,
		},
		Stages:          make(map[int]*cache.Stage),
		Jobs:            make([]*cache.Job, 0),
		RepoBuildNumber: b.Number,
	}

	if build.StartedAt, err = utils.NullTimeFromString(b.StartedAt); err != nil {
		return build, err
	}
	if build.FinishedAt, err = utils.NullTimeFromString(b.FinishedAt); err != nil {
		return build, err
	}
	if build.UpdatedAt, err = time.Parse(time.RFC3339, b.UpdatedAt); err != nil {
		return build, err
	}

	if b.Tag.Name == "" {
		build.Ref = b.Branch.Name
	} else {
		build.Ref = b.Tag.Name
	}

	build.WebURL = fmt.Sprintf("%s/builds/%d", webURL, b.ID)

	for _, travisJob := range b.Jobs {
		var stage *cache.Stage
		if travisJob.Stage.ID != 0 {
			var exists bool
			stage, exists = build.Stages[travisJob.Stage.ID]
			if !exists {
				s := travisJob.Stage.toCacheStage(&build)
				stage = &s
				build.Stages[stage.ID] = stage
			}
		}
		job, err := travisJob.toCacheJob(&build, stage, webURL)
		if err != nil {
			return build, err
		}

		if job.CreatedAt.Valid {
			build.CreatedAt = utils.MinNullTime(build.CreatedAt, job.CreatedAt)
		}
		if stage != nil {
			stage.Jobs = append(stage.Jobs, &job)
		} else {
			build.Jobs = append(build.Jobs, &job)
		}
	}

	return build, nil
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

func (j travisJob) toCacheJob(build *cache.Build, stage *cache.Stage, webURL string) (cache.Job, error) {
	var err error

	name, _ := j.Config["name"].(string)
	if name == "" {
		name = j.Config.String()
	}

	job := cache.Job{
		ID:           strconv.Itoa(j.ID),
		State:        fromTravisState(j.State),
		Name:         name,
		Log:          utils.NullString{String: j.Log, Valid: j.Log != ""},
		WebURL:       fmt.Sprintf("%s/jobs/%d", webURL, j.ID),
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
			return cache.Job{}, err
		}
	}

	if job.StartedAt.Valid && job.FinishedAt.Valid {
		d := int64(job.FinishedAt.Time.Sub(job.StartedAt.Time).Seconds())
		job.Duration = utils.NullDuration{
			Valid:    true,
			Duration: time.Duration(d) * time.Second,
		}
	}

	return job, nil
}

type travisStage struct {
	ID    int
	Name  string
	State string
}

func (s travisStage) toCacheStage(build *cache.Build) cache.Stage {
	return cache.Stage{
		ID:    s.ID,
		Name:  s.Name,
		State: fromTravisState(s.State),
		Jobs:  make([]*cache.Job, 0),
	}
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

func (c TravisClient) BuildFromURL(ctx context.Context, u string) (cache.Build, error) {
	owner, repo, id, err := parseTravisWebURL(&c.baseURL, u)
	if err != nil {
		return cache.Build{}, err
	}

	repository, err := c.repository(ctx, fmt.Sprintf("%s/%s", owner, repo))
	if err != nil {
		return cache.Build{}, err
	}

	return c.fetchBuild(ctx, &repository, id)
}

// Extract owner, repository and build ID from web URL of build
func parseTravisWebURL(baseURL *url.URL, u string) (string, string, string, error) {
	v, err := url.Parse(u)
	if err != nil {
		return "", "", "", err
	}

	if v.Hostname() != strings.TrimPrefix(baseURL.Hostname(), "api.") {
		return "", "", "", cache.ErrUnknownURL
	}

	// URL format: https://travis-ci.org/nbedos/termtosvg/builds/612815758
	cs := strings.Split(v.EscapedPath(), "/")
	if len(cs) < 5 || cs[3] != "builds" {
		return "", "", "", cache.ErrUnknownURL
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
			return cache.Repository{}, cache.ErrRepositoryNotFound
		}
		return cache.Repository{}, err
	}

	travisRepo := travisRepository{}
	if err = json.Unmarshal(body.Bytes(), &travisRepo); err != nil {
		return cache.Repository{}, err
	}

	return travisRepo.toCacheRepository(c.provider), nil
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
	req.Header.Add("Authorization", fmt.Sprintf("token %s", c.token))
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

func (c TravisClient) fetchBuild(ctx context.Context, repository *cache.Repository, buildID string) (cache.Build, error) {
	buildURL := c.baseURL
	buildURL.Path += fmt.Sprintf("/build/%s", buildID)
	parameters := buildURL.Query()
	parameters.Add("include", "build.jobs,build.commit,job.config")
	buildURL.RawQuery = parameters.Encode()

	body, err := c.get(ctx, "GET", buildURL)
	if err != nil {
		return cache.Build{}, err
	}

	var build travisBuild
	err = json.Unmarshal(body.Bytes(), &build)

	webURL, err := c.webURL(*repository)
	if err != nil {
		return cache.Build{}, err
	}
	cacheBuild, err := build.toCacheBuild(repository, webURL.String())
	if err != nil {
		return cache.Build{}, err
	}

	return cacheBuild, nil
}

func (c TravisClient) Log(ctx context.Context, repository cache.Repository, jobID string) (string, error) {
	var reqURL = c.baseURL
	reqURL.Path += fmt.Sprintf("/job/%s/log", jobID)

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
