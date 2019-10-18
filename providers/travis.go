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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type travisRepository struct {
	ID    int
	Slug  string
	Owner struct {
		Name string
	}
	Name string
}

func FromTravisState(s string) cache.State {
	switch strings.ToLower(s) {
	case "created", "queued":
		return cache.Pending
	case "started":
		return cache.Running
	case "canceled":
		return cache.Canceled
	case "passed":
		return cache.Passed
	case "failed":
		return cache.Failed
	case "skipped":
		return cache.Skipped
	default:
		return cache.Unknown
	}
}

func (r travisRepository) ToInserter(accountID string) cache.Repository {
	return cache.Repository{
		AccountID: accountID,
		URL:       fmt.Sprintf("https://github.com/%v", r.Slug), // FIXME
		Name:      r.Name,
		Owner:     r.Owner.Name,
		RemoteID:  r.ID,
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

func (c travisCommit) ToInserter(accountID string, repositoryURL string) (cache.Commit, error) {
	committedAt := sql.NullTime{}
	if c.Date != "" {
		date, err := time.Parse(time.RFC3339, c.Date)
		if err != nil {
			return cache.Commit{}, err
		}
		committedAt = sql.NullTime{
			Time:  date,
			Valid: true,
		}
	}

	return cache.Commit{
		AccountID:     accountID,
		ID:            c.ID,
		RepositoryURL: repositoryURL,
		Message:       c.Message,
		Date:          committedAt,
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

func (b travisBuild) ToInserters(accountID string, repositoryURL string, webURL string) (build cache.Build, err error) {
	commit, err := b.Commit.ToInserter(accountID, repositoryURL)
	if err != nil {
		return build, err
	}

	build = cache.Build{
		AccountID:     accountID,
		RepositoryURL: repositoryURL,
		Commit:        commit,
		IsTag:         b.Tag.Name != "",
		State:         FromTravisState(b.State),
		CreatedAt:     sql.NullTime{}, // FIXME We need this
		Duration:      sql.NullInt64{Int64: int64(b.Duration), Valid: b.Duration > 0},
		Stages:        make(map[int]*cache.Stage),
		Jobs:          make(map[int]*cache.Job),
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

	if build.ID, err = strconv.Atoi(b.Number); err != nil {
		return build, err
	}
	build.WebURL = fmt.Sprintf("%s/builds/%d", webURL, b.ID)

	for _, travisJob := range b.Jobs {
		job, err := travisJob.ToInserter(build.AccountID, build.ID)
		if err != nil {
			return build, err
		}

		if travisJob.Stage.Number != 0 {
			stage, exists := build.Stages[travisJob.Stage.Number]
			if !exists {
				s := travisJob.Stage.ToInserter(accountID, build.ID)
				stage = &s
				build.Stages[stage.ID] = stage
			}
			stage.Jobs[job.Key.ID] = &job
		} else {
			build.Jobs[job.Key.ID] = &job
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
	Log          struct {
		Content string
	}
	Config struct {
		Os       string
		Language string
		Name     string
	}
}

func DotDecimal(s string) ([]int, error) {
	numbers := strings.Split(s, ".")
	if len(numbers) == 0 {
		return nil, fmt.Errorf("failed splitting string '%s'", s)
	}

	is := make([]int, 0, len(numbers))
	for _, number := range numbers {
		i, err := strconv.Atoi(number)
		if err != nil {
			return nil, err
		}
		is = append(is, i)
	}

	return is, nil
}

func (j travisJob) ToInserter(accountID string, buildID int) (cache.Job, error) {
	var err error
	jobStartedAt := sql.NullTime{}
	if j.StartedAt != "" {
		if jobStartedAt.Time, err = time.Parse(time.RFC3339, j.StartedAt); err != nil {
			return cache.Job{}, err
		}
		jobStartedAt.Valid = true
	}

	jobFinishedAt := sql.NullTime{}
	if j.FinishedAt != "" {
		if jobFinishedAt.Time, err = time.Parse(time.RFC3339, j.FinishedAt); err != nil {
			return cache.Job{}, err
		}
		jobFinishedAt.Valid = true
	}

	name := j.Config.Name
	if name == "" {
		name = fmt.Sprintf("%s - %s", j.Config.Os, j.Config.Language)
	}

	var duration int64
	if jobStartedAt.Valid && jobFinishedAt.Valid {
		duration = int64(jobFinishedAt.Time.Sub(jobStartedAt.Time).Seconds())
	}

	ids, err := DotDecimal(j.Number)
	if err != nil {
		return cache.Job{}, err
	}
	job := cache.Job{
		Key: cache.JobKey{
			AccountID: accountID,
			BuildID:   buildID,
			StageID:   j.Stage.Number,
			ID:        ids[len(ids)-1],
		},
		State:      FromTravisState(j.State),
		Name:       name,
		CreatedAt:  sql.NullTime{},
		StartedAt:  jobStartedAt,
		FinishedAt: jobFinishedAt,
		Log:        sql.NullString{String: j.Log.Content, Valid: j.Log.Content != ""},
		Duration:   sql.NullInt64{Int64: duration, Valid: duration > 0},
	}

	return job, nil
}

type travisStage struct {
	Number int
	Name   string
	State  string
}

func (s travisStage) ToInserter(accountID string, buildID int) cache.Stage {
	return cache.Stage{
		AccountID: accountID,
		BuildID:   buildID,
		ID:        s.Number,
		Name:      s.Name,
		State:     FromTravisState(s.State),
		Jobs:      make(map[int]*cache.Job),
	}
}

type TravisClient struct {
	baseURL     url.URL
	pusherHost  string
	httpClient  *http.Client
	rateLimiter <-chan time.Time
	token       string
	accountID   string
}

var TravisOrgURL = url.URL{Scheme: "https", Host: "api.travis-ci.org"}
var TravisComURL = url.URL{Scheme: "https", Host: "api.travis-ci.com"}
var TravisPusherHost = "ws.pusherapp.com"

func NewTravisClient(URL url.URL, pusherHost string, token string, accountID string, rateLimit time.Duration) TravisClient {
	return TravisClient{
		baseURL:     URL,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		pusherHost:  pusherHost,
		rateLimiter: time.Tick(rateLimit),
		token:       token,
		accountID:   accountID,
	}
}

func (c TravisClient) AccountID() string {
	return c.accountID
}

// FIXME Pass list of builds already cached with update date
func (c TravisClient) Builds(ctx context.Context, repository cache.Repository, maxAge time.Duration, buildc chan<- cache.Build) error {
	// Create Pusher client
	// FIXME Requests to "/pusher/auth" should be rate limited too
	p, err := c.pusherClient(ctx, repository.RemoteID)
	if err != nil {
		return err
	}
	defer p.Close()

	// Fetch build history and active builds list
	// FIXME Remove hardcoded values
	if err := c.RepositoryBuilds(ctx, repository.URL, 20, 5, buildc); err != nil {
		return err
	}

	// Monitor active builds

	webURL := c.baseURL
	webURL.Host = strings.TrimPrefix(webURL.Host, "api.")
	webURL.Path, err = utils.RepositorySlugFromURL(repository.URL)
	if err != nil {
		return err
	}

	builds := make(map[int]*cache.Build)
	for {
		event, err := p.NextEvent(ctx)
		if err != nil {
			return err
		}

		switch {
		// "build:created", "build:started", "build:finished"...
		case strings.HasPrefix(event.Event, "build:"):
			var s string
			if err := json.Unmarshal(event.Data, &s); err != nil {
				return err
			}
			var data struct {
				Build struct {
					ID int `json:"id"`
				} `json:"build"`
			}
			if err := json.Unmarshal([]byte(s), &data); err != nil {
				return err
			}

			build, err := c.fetchBuild(ctx, repository.URL, data.Build.ID, webURL.String())
			if err != nil {
				return err
			}
			builds[data.Build.ID] = &build

			select {
			case buildc <- build:
			case <-ctx.Done():
				return ctx.Err()
			}
		// "job:queued", "job:received", "job:created", "job:started", "job:finished", "job:log"...
		case strings.HasPrefix(event.Event, "job:"):
			var s string
			if err := json.Unmarshal(event.Data, &s); err != nil {
				return err
			}
			var data struct {
				BuildID int    `json:"build_id"`
				Number  string `json:"number"`
				State   string `json:"state"`
			}

			if err := json.Unmarshal([]byte(s), &data); err != nil {
				return err
			}

			ids, err := DotDecimal(data.Number)
			if err != nil {
				return err
			}

			if _, exists := builds[data.BuildID]; !exists {
				b, err := c.fetchBuild(ctx, repository.URL, data.BuildID, webURL.String())
				if err != nil {
					return err
				}
				builds[data.BuildID] = &b

			}
			build := builds[data.BuildID]

			var job *cache.Job
			var exists bool
			if len(ids) == 2 {
				if job, exists = build.Jobs[ids[1]]; !exists {
					return fmt.Errorf("job %d not found for build (account_id=%s, id=%d)", ids[1], build.AccountID, build.ID)
				}
			} else if len(ids) == 3 {
				stage, exists := build.Stages[ids[1]]
				if !exists {
					return fmt.Errorf("stage %d not found for build (account_id=%s, id=%d)", ids[1], build.AccountID, build.ID)
				}
				if job, exists = stage.Jobs[ids[2]]; !exists {
					return fmt.Errorf("job %d not found for build (account_id=%s, id=%d)", ids[2], build.AccountID, build.ID)
				}
			}
			job.State = FromTravisState(data.State)

			select {
			case buildc <- *build:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return nil
}

func (c TravisClient) Repository(ctx context.Context, repositoryURL string) (cache.Repository, error) {
	slug, err := utils.RepositorySlugFromURL(repositoryURL)
	if err != nil {
		return cache.Repository{}, err
	}

	var reqURL = c.baseURL
	buildPathFormat := "/repo/%s"
	reqURL.Path += fmt.Sprintf(buildPathFormat, slug)
	reqURL.RawPath += fmt.Sprintf(buildPathFormat, url.PathEscape(slug))

	body, err := c.get(ctx, reqURL)
	if err != nil {
		return cache.Repository{}, err
	}

	travisRepo := travisRepository{}
	if err = json.Unmarshal(body.Bytes(), &travisRepo); err != nil {
		return cache.Repository{}, err
	}

	return travisRepo.ToInserter(c.accountID), nil
}

// Rate-limited HTTP GET request with custom headers
func (c TravisClient) get(ctx context.Context, resourceURL url.URL) (*bytes.Buffer, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", resourceURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Travis-API-Version", "3")
	req.Header.Add("Authorization", fmt.Sprintf("token %s", c.token))

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
		var errResp struct {
			ErrorType    string `json:"error_type"`
			ErrorMessage string `json:"error_message"`
		}
		message := ""
		if jsonErr := json.Unmarshal(body.Bytes(), &errResp); jsonErr == nil {
			message = errResp.ErrorMessage
		}
		err = HTTPError{
			Method:  req.Method,
			URL:     req.URL.String(),
			Status:  resp.StatusCode,
			Message: message,
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
	return fmt.Sprintf("%s %s returned an error: status %d (%s)",
		err.Method, err.URL, err.Status, err.Message)
}

func (c TravisClient) fetchBuild(ctx context.Context, repositoryURL string, buildID int, webURL string) (cache.Build, error) {
	buildURL := c.baseURL
	buildURL.Path += fmt.Sprintf("/build/%d", buildID)
	parameters := buildURL.Query()
	parameters.Add("include", "build.jobs,build.commit,job.config")
	buildURL.RawQuery = parameters.Encode()

	body, err := c.get(ctx, buildURL)
	if err != nil {
		return cache.Build{}, err
	}

	var build travisBuild
	err = json.Unmarshal(body.Bytes(), &build)

	if err = c.fetchBuildLogs(ctx, &build); err != nil {
		return cache.Build{}, err
	}

	return build.ToInserters(c.accountID, repositoryURL, webURL)
}

func (c TravisClient) fetchBuilds(ctx context.Context, repositoryURL string, offset int, pageSize int, buildc chan<- cache.Build) error {
	var buildsURL = c.baseURL
	buildPathFormat := "/repo/%v/builds"
	repositorySlug, err := utils.RepositorySlugFromURL(repositoryURL)
	if err != nil {
		return err
	}
	buildsURL.Path += fmt.Sprintf(buildPathFormat, repositorySlug)
	buildsURL.RawPath += fmt.Sprintf(buildPathFormat, url.PathEscape(repositorySlug))
	parameters := buildsURL.Query()
	parameters.Add("limit", strconv.Itoa(pageSize))
	parameters.Add("offset", strconv.Itoa(offset))
	buildsURL.RawQuery = parameters.Encode()

	body, err := c.get(ctx, buildsURL)
	if err != nil {
		return err
	}

	var response struct {
		Builds []travisBuild
	}

	err = json.Unmarshal(body.Bytes(), &response)
	if err != nil {
		return err
	}

	wg := sync.WaitGroup{}
	errc := make(chan error)

	webURL := c.baseURL
	webURL.Host = strings.TrimPrefix(webURL.Host, "api.")
	webURL.Path = repositorySlug

	for _, build := range response.Builds {
		wg.Add(1)
		go func(buildID int) {
			defer wg.Done()
			build, err := c.fetchBuild(ctx, repositoryURL, buildID, webURL.String())
			if err != nil {
				errc <- err
				return
			}
			select {
			case buildc <- build:
			case <-ctx.Done():
				errc <- ctx.Err()
			}
		}(build.ID)
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	// FIXME Improve error reporting
	for e := range errc {
		if err == nil {
			err = e
		}
	}

	return err
}

func (c TravisClient) RepositoryBuilds(ctx context.Context, repositoryURL string, limit int, pageSize int, buildc chan<- cache.Build) error {
	if pageSize <= 0 || limit <= 0 {
		return errors.New("pageSize and limit must be > 0")
	}

	errc := make(chan error)
	var wg sync.WaitGroup

	for offset := 0; offset < limit; offset += pageSize {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			if err := c.fetchBuilds(ctx, repositoryURL, offset, pageSize, buildc); err != nil {
				errc <- err
				return
			}
		}(offset)
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	var err error
	// FIXME Improve error reporting
	for e := range errc {
		if err == nil {
			err = e
		}
	}

	return err
}

func (c TravisClient) fetchBuildLogs(ctx context.Context, build *travisBuild) error {
	jobc := make(chan travisJob)
	errc := make(chan error, len(build.Jobs))

	var wg sync.WaitGroup

	for _, job := range build.Jobs {
		wg.Add(1)
		go func(j travisJob) {
			defer wg.Done()

			jobWithLog, err := c.fetchJobLog(ctx, j)
			if err != nil {
				errc <- err
				return
			}
			jobc <- jobWithLog
		}(job)
	}

	go func() {
		wg.Wait()
		close(jobc)
		close(errc)
	}()

	jobMap := make(map[int]travisJob)
	for job := range jobc {
		jobMap[job.ID] = job
	}

	for jobIndex := range build.Jobs {
		id := build.Jobs[jobIndex].ID
		build.Jobs[jobIndex] = jobMap[id]
	}

	return <-errc
}

func (c TravisClient) fetchJobLog(ctx context.Context, job travisJob) (travisJob, error) {
	var reqURL = c.baseURL
	reqURL.Path += fmt.Sprintf("/job/%d/log", job.ID)

	body, err := c.get(ctx, reqURL)
	if err != nil {
		return travisJob{}, err
	}

	err = json.Unmarshal(body.Bytes(), &job.Log)
	if err != nil {
		return travisJob{}, err
	}

	return job, nil
}

func (c TravisClient) pusherClient(ctx context.Context, repositoryID int) (p utils.PusherClient, err error) {
	pusherKey, private, err := c.fetchPusherConfig(ctx)
	if err != nil {
		return p, err
	}

	wsURL := utils.PusherUrl(c.pusherHost, pusherKey)
	authURL := c.baseURL
	authURL.Path += "/pusher/auth"
	authHeader := map[string]string{
		"Authorization": fmt.Sprintf("token %s", c.token),
	}

	// FIXME Requests to "/pusher/auth" should be rate limited too
	if p, err = utils.NewPusherClient(ctx, wsURL, authURL.String(), authHeader); err != nil {
		return p, err
	}

	// FIXME Meh. There must be a better way.
	if _, err = p.Expect(ctx, utils.ConnectionEstablished); err != nil {
		return p, err
	}

	chanFmt := "repo-%d"
	if private {
		chanFmt = "private-" + chanFmt
	}
	if err := p.Subscribe(ctx, fmt.Sprintf(chanFmt, repositoryID)); err != nil {
		return p, err
	}

	if _, err = p.Expect(ctx, utils.SubscriptionSucceeded); err != nil {
		return p, err
	}

	return p, nil
}

func (c TravisClient) fetchPusherConfig(ctx context.Context) (string, bool, error) {
	body, err := c.get(ctx, c.baseURL)
	if err != nil {
		return "", false, err
	}

	var home struct {
		Config struct {
			Pusher struct {
				Key     string `json:"key"`
				Private bool   `json:"private"`
			} `json:"pusher"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body.Bytes(), &home); err != nil {
		return "", false, err
	}

	return home.Config.Pusher.Key, home.Config.Pusher.Private, err
}
