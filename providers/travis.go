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
	case "created":
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

func (b travisBuild) ToInserters(accountID string, repositoryURL string, webURL string) ([]cache.Inserter, error) {
	inserters := make([]cache.Inserter, 0)

	commit, err := b.Commit.ToInserter(accountID, repositoryURL)
	if err != nil {
		return inserters, err
	}
	build, err := b.ToInserter(accountID, repositoryURL, webURL)
	if err != nil {
		return inserters, err
	}

	jobInserters := make([]cache.Inserter, 0, len(b.Jobs))
	stages := map[int]cache.Stage{}
	jobNumbers := make(map[int]int)
	for _, travisJob := range b.Jobs {
		if travisJob.Stage.Number != 0 {
			stage := travisJob.Stage.ToInserter(accountID, build.ID)
			stages[stage.ID] = stage
		}

		jobNumbers[travisJob.Stage.Number]++
		job, err := travisJob.ToInserter(build.AccountID, build.ID, jobNumbers[travisJob.Stage.Number])
		if err != nil {
			return inserters, err
		}

		jobInserters = append(jobInserters, job)
	}

	// The following lines must be ordered according to database constraints
	inserters = append(inserters, commit)
	inserters = append(inserters, build)
	for _, stage := range stages {
		inserters = append(inserters, stage)
	}
	inserters = append(inserters, jobInserters...)

	return inserters, nil
}

func (b travisBuild) ToInserter(accountID string, repositoryURL string, webURL string) (cache.Build, error) {
	var err error

	build := cache.Build{
		AccountID:     accountID,
		RepositoryURL: repositoryURL,
		CommitID:      b.Commit.ID,
		IsTag:         b.Tag.Name != "",
		State:         FromTravisState(b.State),
		CreatedAt:     sql.NullTime{}, // FIXME We need this
		Duration:      sql.NullInt64{Int64: int64(b.Duration), Valid: b.Duration > 0},
	}

	if build.StartedAt, err = cache.NullTimeFromString(b.StartedAt); err != nil {
		return build, err
	}

	if build.FinishedAt, err = cache.NullTimeFromString(b.FinishedAt); err != nil {
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

	return build, nil
}

type travisBuilds struct {
	Builds []travisBuild
}

func (b travisBuilds) ToInserter(accountID string, webURL string) ([]cache.Inserter, error) {
	inserters := make([]cache.Inserter, 0)
	for _, travisBuild := range b.Builds {
		repository := travisBuild.Repository.ToInserter(accountID)
		commit, err := travisBuild.Commit.ToInserter(accountID, repository.URL)
		if err != nil {
			return inserters, err
		}
		build, err := travisBuild.ToInserter(accountID, repository.URL, webURL)
		if err != nil {
			return inserters, err
		}

		jobInserters := make([]cache.Inserter, 0, len(travisBuild.Jobs))
		stages := map[int]cache.Stage{}
		jobNumbers := make(map[int]int)
		for _, travisJob := range travisBuild.Jobs {
			if travisJob.Stage.Number != 0 {
				stage := travisJob.Stage.ToInserter(accountID, build.ID)
				stages[stage.ID] = stage
			}

			jobNumbers[travisJob.Stage.Number]++
			job, err := travisJob.ToInserter(build.AccountID, build.ID, jobNumbers[travisJob.Stage.Number])
			if err != nil {
				return inserters, err
			}

			jobInserters = append(jobInserters, job)
		}

		// The following lines must be order according to database constraints
		inserters = append(inserters, repository)
		inserters = append(inserters, commit)
		inserters = append(inserters, build)
		for _, stage := range stages {
			inserters = append(inserters, stage)
		}
		inserters = append(inserters, jobInserters...)

	}

	return inserters, nil
}

type travisJob struct {
	ID           int
	State        string
	StartedAt    string `json:"started_at"`
	CreatedAt    string `json:"created_at"`
	FinishedAt   string `json:"finished_at"`
	Stage        travisStage
	AllowFailure bool `json:"allow_failure"`
	Log          struct {
		Content string
	}
	Config struct {
		Os       string
		Language string
		Name     string
	}
}

func (j travisJob) ToInserter(accountID string, buildID int, number int) (cache.Job, error) {
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

	job := cache.Job{
		Key: cache.JobKey{
			AccountID: accountID,
			BuildID:   buildID,
			StageID:   j.Stage.Number,
			ID:        number,
		},
		State:      FromTravisState(j.State),
		Name:       name,
		CreatedAt:  sql.NullTime{},
		StartedAt:  jobStartedAt,
		FinishedAt: jobFinishedAt,
		Log:        j.Log.Content,
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

func (t TravisClient) pusherClient(ctx context.Context, repositoryID int) (p utils.PusherClient, err error) {
	pusherKey, private, err := t.fetchPusherConfig(ctx)
	if err != nil {
		return p, err
	}

	wsURL := utils.PusherUrl(t.pusherHost, pusherKey)
	authURL := t.baseURL
	authURL.Path += "/pusher/auth"
	authHeader := map[string]string{
		"Authorization": fmt.Sprintf("token %s", t.token),
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

func (t TravisClient) AccountID() string {
	return t.accountID
}

// FIXME Pass list of builds already cached with update date
func (t TravisClient) Builds(ctx context.Context, repository cache.Repository, maxAge time.Duration, inserters chan<- []cache.Inserter) error {
	// Get repository object
	if repository.RemoteID == 0 {
		id, err := RepositorySlugFromURL(repository.URL)
		if err != nil {
			return err
		}
		repository, err = t.fetchRepository(ctx, id)
		if err != nil {
			return err
		}
		select {
		case inserters <- []cache.Inserter{repository}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Create Pusher client
	// FIXME Requests to "/pusher/auth" should be rate limited too
	p, err := t.pusherClient(ctx, repository.RemoteID)
	if err != nil {
		return err
	}
	defer p.Close()

	// Fetch build history and active builds list
	// FIXME Remove hardcoded values
	if err := t.RepositoryBuilds(ctx, repository.URL, 20, 5, inserters); err != nil {
		return err
	}

	// Monitor active builds
	for {
		event, err := p.NextEvent(ctx)
		if err != nil {
			return err
		}
		//fmt.Printf("received event %s (%s)\n", event.Event, string(event.Data))

		switch event.Event {
		case "build:created":
			// FetchBuild
		case "build:started":
			// Status change
		case "build:finished":
			// Status change
		case "job:queued":
			// Ignored
		case "job:received":
			// Ignored
		case "job:created":
			// FetchJob?
		case "job:started":
			// Status change
		case "job:finished":
			// Fetch logs
		case "job:log":
			// Append logs
		}
	}

	return nil
}

// Rate-limited HTTP GET request with custom headers
func (t TravisClient) get(ctx context.Context, resourceURL url.URL) (*bytes.Buffer, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", resourceURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Travis-API-Version", "3")
	req.Header.Add("Authorization", fmt.Sprintf("token %s", t.token))

	select {
	case <-t.rateLimiter:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	resp, err := t.httpClient.Do(req)
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

func (t TravisClient) fetchRepository(ctx context.Context, id string) (cache.Repository, error) {
	var reqURL = t.baseURL
	buildPathFormat := "/repo/%s"
	reqURL.Path += fmt.Sprintf(buildPathFormat, id)
	reqURL.RawPath += fmt.Sprintf(buildPathFormat, url.PathEscape(id))

	body, err := t.get(ctx, reqURL)
	if err != nil {
		return cache.Repository{}, err
	}

	travisRepo := travisRepository{}
	if err = json.Unmarshal(body.Bytes(), &travisRepo); err != nil {
		return cache.Repository{}, err
	}

	return travisRepo.ToInserter(t.accountID), nil
}

func (t TravisClient) fetchBuild(ctx context.Context, repositoryURL string, buildID int, webURL string) ([]cache.Inserter, error) {
	buildURL := t.baseURL
	buildURL.Path += fmt.Sprintf("/build/%d", buildID)
	parameters := url.Values{}
	parameters.Add("include", "build.jobs,build.commit,job.config")
	buildURL.RawQuery = parameters.Encode()

	body, err := t.get(ctx, buildURL)
	if err != nil {
		return []cache.Inserter{}, err
	}

	var build travisBuild
	err = json.Unmarshal(body.Bytes(), &build)

	if err = t.fetchBuildLogs(ctx, &build); err != nil {
		return []cache.Inserter{}, err
	}

	return build.ToInserters(t.accountID, repositoryURL, webURL)
}

func RepositorySlugFromURL(repositoryURL string) (string, error) {
	URL, err := url.Parse(repositoryURL)
	if err != nil {
		return "", err
	}

	return strings.Trim(URL.Path, "/"), nil
}

func (t TravisClient) fetchBuilds(ctx context.Context, repositoryURL string, offset int, pageSize int, c chan<- []cache.Inserter) error {
	var buildsURL = t.baseURL
	buildPathFormat := "/repo/%v/builds"
	repositorySlug, err := RepositorySlugFromURL(repositoryURL)
	if err != nil {
		return err
	}
	buildsURL.Path += fmt.Sprintf(buildPathFormat, repositorySlug)
	buildsURL.RawPath += fmt.Sprintf(buildPathFormat, url.PathEscape(repositorySlug))
	parameters := url.Values{}
	parameters.Add("limit", strconv.Itoa(pageSize))
	parameters.Add("offset", strconv.Itoa(offset))
	buildsURL.RawQuery = parameters.Encode()

	body, err := t.get(ctx, buildsURL)
	if err != nil {
		return err
	}

	var builds travisBuilds
	err = json.Unmarshal(body.Bytes(), &builds)
	if err != nil {
		return err
	}

	wg := sync.WaitGroup{}
	errc := make(chan error)

	webURL := t.baseURL
	webURL.Host = strings.TrimPrefix(webURL.Host, "api.")
	webURL.Path = repositorySlug

	for _, build := range builds.Builds {
		wg.Add(1)
		go func(buildID int) {
			defer wg.Done()
			inserters, err := t.fetchBuild(ctx, repositoryURL, buildID, webURL.String())
			if err != nil {
				errc <- err
				return
			}
			select {
			case c <- inserters:
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}

		}(build.ID)
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	// FIXME Improve error reporting
	for e := range errc {
		if err != nil {
			err = e
		}
	}

	return err
}

func (t TravisClient) RepositoryBuilds(ctx context.Context, repositoryURL string, limit int, pageSize int, c chan<- []cache.Inserter) error {
	if pageSize <= 0 || limit <= 0 {
		return errors.New("pageSize and limit must be > 0")
	}

	errc := make(chan error)
	var wg sync.WaitGroup

	for offset := 0; offset < limit; offset += pageSize {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			if err := t.fetchBuilds(ctx, repositoryURL, offset, pageSize, c); err != nil {
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

func (t TravisClient) fetchBuildLogs(ctx context.Context, build *travisBuild) error {
	c := make(chan travisJob)
	errc := make(chan error, len(build.Jobs))

	var wg sync.WaitGroup

	for _, job := range build.Jobs {
		wg.Add(1)
		go func(j travisJob) {
			defer wg.Done()

			jobWithLog, err := t.fetchJobLog(ctx, j)
			if err != nil {
				errc <- err
				return
			}
			c <- jobWithLog
		}(job)
	}

	go func() {
		wg.Wait()
		close(c)
		close(errc)
	}()

	jobMap := make(map[int]travisJob)
	for job := range c {
		jobMap[job.ID] = job
	}

	for jobIndex := range build.Jobs {
		id := build.Jobs[jobIndex].ID
		build.Jobs[jobIndex] = jobMap[id]
	}

	return <-errc
}

func (t TravisClient) fetchJobLog(ctx context.Context, job travisJob) (travisJob, error) {
	var reqURL = t.baseURL
	reqURL.Path += fmt.Sprintf("/job/%d/log", job.ID)

	body, err := t.get(ctx, reqURL)
	if err != nil {
		return travisJob{}, err
	}

	err = json.Unmarshal(body.Bytes(), &job.Log)
	if err != nil {
		return travisJob{}, err
	}

	return job, nil
}

func (t TravisClient) fetchPusherConfig(ctx context.Context) (string, bool, error) {
	body, err := t.get(ctx, t.baseURL)
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
