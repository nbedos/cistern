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
	"sync"
	"time"

	"github.com/cenkalti/backoff/v3"
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

func (r travisRepository) toCacheRepository(accountID string) cache.Repository {
	return cache.Repository{
		ID:        r.ID,
		AccountID: accountID,
		URL:       fmt.Sprintf("https://github.com/%v", r.Slug), // FIXME
		Name:      r.Name,
		Owner:     r.Owner.Login,
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

func (c travisCommit) toCacheCommit(accountID string, repository *cache.Repository) (cache.Commit, error) {
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

func (b travisBuild) toCacheBuild(accountID string, repository *cache.Repository, webURL string) (build cache.Build, err error) {
	commit, err := b.Commit.toCacheCommit(accountID, repository)
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
		Jobs:            make(map[int]*cache.Job),
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
			stage.Jobs[job.ID] = &job
		} else {
			build.Jobs[job.ID] = &job
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
		ID:           j.ID,
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
		Jobs:  make(map[int]*cache.Job),
	}
}

type TravisClient struct {
	baseURL              url.URL
	httpClient           *http.Client
	rateLimiter          <-chan time.Time
	logBackoffInterval   time.Duration
	buildsPageSize       int
	token                string
	accountID            string
	updateTimePerBuildID map[string]time.Time
	mux                  *sync.Mutex
}

var TravisOrgURL = url.URL{Scheme: "https", Host: "api.travis-ci.org"}
var TravisComURL = url.URL{Scheme: "https", Host: "api.travis-ci.com"}

func NewTravisClient(URL url.URL, token string, accountID string, rateLimit time.Duration) TravisClient {
	return TravisClient{
		baseURL:              URL,
		httpClient:           &http.Client{Timeout: 10 * time.Second},
		rateLimiter:          time.Tick(rateLimit),
		logBackoffInterval:   10 * time.Second,
		token:                token,
		accountID:            accountID,
		buildsPageSize:       10,
		mux:                  &sync.Mutex{},
		updateTimePerBuildID: make(map[string]time.Time),
	}
}

func (c TravisClient) AccountID() string {
	return c.accountID
}

func (c TravisClient) Builds(ctx context.Context, repositoryURL string, limit int, buildc chan<- cache.Build) error {
	repository, err := c.repository(ctx, repositoryURL)
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

	// Fetch build history and active builds list
	var active bool
	for {
		if err := c.repositoryBuilds(ctx, &repository, limit, buildc); err != nil {
			return err
		}
		if active {
			b.Reset()
		}

		waitTime := b.NextBackOff()
		if waitTime == backoff.Stop {
			break
		}

		select {
		case <-time.After(waitTime):
			// Do nothing
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (c TravisClient) Log(ctx context.Context, repository cache.Repository, jobID int) (string, error) {
	log, err := c.fetchJobLog(ctx, jobID)
	return log, err
}

func (c TravisClient) repository(ctx context.Context, repositoryURL string) (cache.Repository, error) {
	slug, err := utils.RepositorySlugFromURL(repositoryURL)
	if err != nil {
		return cache.Repository{}, err
	}

	var reqURL = c.baseURL
	buildPathFormat := "/repo/%s"
	reqURL.Path += fmt.Sprintf(buildPathFormat, slug)
	reqURL.RawPath += fmt.Sprintf(buildPathFormat, url.PathEscape(slug))

	body, _, err := c.get(ctx, "GET", reqURL, nil)
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

	return travisRepo.toCacheRepository(c.accountID), nil
}

func (c TravisClient) webURL(repository cache.Repository) (url.URL, error) {
	var err error
	webURL := c.baseURL
	webURL.Host = strings.TrimPrefix(webURL.Host, "api.")
	webURL.Path += fmt.Sprintf("/%s", repository.Slug())

	return webURL, err
}

// Rate-limited HTTP GET request with custom headers
func (c TravisClient) get(ctx context.Context, method string, resourceURL url.URL, headers map[string]string) (*bytes.Buffer, *http.Response, error) {
	req, err := http.NewRequest(method, resourceURL.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Add("Travis-API-Version", "3")
	req.Header.Add("Authorization", fmt.Sprintf("token %s", c.token))
	for header, value := range headers {
		req.Header.Add(header, value)
	}
	req = req.WithContext(ctx)

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close() // FIXME return error

	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		return nil, nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = HTTPError{
			Method:  req.Method,
			URL:     req.URL.String(),
			Status:  resp.StatusCode,
			Message: body.String(),
		}
	}

	return body, resp, err
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

	body, _, err := c.get(ctx, "GET", buildURL, nil)
	if err != nil {
		return cache.Build{}, err
	}

	var build travisBuild
	err = json.Unmarshal(body.Bytes(), &build)

	webURL, err := c.webURL(*repository)
	if err != nil {
		return cache.Build{}, err
	}
	cacheBuild, err := build.toCacheBuild(c.accountID, repository, webURL.String())
	if err != nil {
		return cache.Build{}, err
	}

	c.mux.Lock()
	c.updateTimePerBuildID[cacheBuild.ID] = cacheBuild.UpdatedAt
	c.mux.Unlock()

	return cacheBuild, nil
}

func (c TravisClient) fetchBuilds(ctx context.Context, repository *cache.Repository, offset int, limit int) ([]travisBuild, error) {
	var buildsURL = c.baseURL
	buildPathFormat := "/repo/%v/builds"
	buildsURL.Path += fmt.Sprintf(buildPathFormat, repository.Slug())
	buildsURL.RawPath += fmt.Sprintf(buildPathFormat, url.PathEscape(repository.Slug()))

	parameters := buildsURL.Query()
	parameters.Add("limit", strconv.Itoa(limit))
	parameters.Add("offset", strconv.Itoa(offset))
	buildsURL.RawQuery = parameters.Encode()

	body, _, err := c.get(ctx, "GET", buildsURL, nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Builds []travisBuild
	}

	if err = json.Unmarshal(body.Bytes(), &response); err != nil {
		return nil, err
	}

	return response.Builds, err
}

func (c TravisClient) repositoryBuilds(ctx context.Context, repository *cache.Repository, limit int, buildc chan<- cache.Build) error {
	wg := sync.WaitGroup{}
	errc := make(chan error)
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg.Add(1)
	active := false
	go func() {
		defer wg.Done()

		for offset := 0; offset < limit; offset += c.buildsPageSize {
			pageSize := utils.MinInt(c.buildsPageSize, limit-offset)
			builds, err := c.fetchBuilds(subCtx, repository, offset, pageSize)
			if err != nil {
				errc <- err
				return
			}
			if len(builds) == 0 {
				break
			}

			for _, build := range builds {
				buildID := strconv.Itoa(build.ID)
				c.mux.Lock()
				lastUpdate := c.updateTimePerBuildID[buildID]
				c.mux.Unlock()
				active = active || fromTravisState(build.State).IsActive()
				updatedAt, err := utils.NullTimeFromString(build.UpdatedAt)
				if err != nil {
					errc <- err
					return
				}
				if updatedAt.Valid && updatedAt.Time.After(lastUpdate) {
					wg.Add(1)
					go func(buildID string) {
						defer wg.Done()

						build, err := c.fetchBuild(subCtx, repository, buildID)
						if err != nil {
							errc <- err
							return
						}
						select {
						case buildc <- build:
						case <-subCtx.Done():
							errc <- subCtx.Err()
						}
					}(buildID)
				}
			}
		}
	}()

	go func() {
		wg.Wait()
		close(errc)
	}()

	// FIXME Improve error reporting
	var err error
	for e := range errc {
		if e != nil && err == nil {
			cancel()
			err = e
		}
	}

	return err
}

func (c TravisClient) fetchJobLog(ctx context.Context, jobID int) (string, error) {
	var reqURL = c.baseURL
	reqURL.Path += fmt.Sprintf("/job/%d/log", jobID)

	body, _, err := c.get(ctx, "GET", reqURL, map[string]string{"Accept": "text/plain"})
	if err != nil {
		return "", err
	}

	return body.String(), nil
}
