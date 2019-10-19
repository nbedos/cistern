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
	"io"
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
		RemoteID:      b.ID,
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
		job, err := travisJob.ToInserter(build.AccountID, build.ID, webURL)
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
	Log          string
	Config       struct {
		Os       string
		Language string
		Name     string
	}
}

func (j travisJob) ToInserter(accountID string, buildID int, webURL string) (cache.Job, error) {
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

	jobKey, err := cache.NewJobKey(j.Number, accountID)
	if err != nil {
		return cache.Job{}, err
	}
	job := cache.Job{
		Key:        jobKey,
		State:      FromTravisState(j.State),
		Name:       name,
		CreatedAt:  sql.NullTime{},
		StartedAt:  jobStartedAt,
		FinishedAt: jobFinishedAt,
		Log:        sql.NullString{String: j.Log, Valid: j.Log != ""},
		Duration:   sql.NullInt64{Int64: duration, Valid: duration > 0},
		WebURL:     fmt.Sprintf("%s/jobs/%d", webURL, j.ID),
		RemoteID:   j.ID,
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
	repoChannel := fmt.Sprintf("repo-%d", repository.RemoteID)
	p, err := c.pusherClient(ctx, []string{repoChannel})
	if err != nil {
		return err
	}
	defer p.Close()

	// Fetch build history and active builds list
	// FIXME Remove hardcoded values
	if err := c.RepositoryBuilds(ctx, repository.URL, 20, 5, buildc); err != nil {
		return err
	}

	errc := make(chan error)
	eventc := make(chan travisEvent)
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		errc <- c.events(subCtx, p, eventc)
	}()

	builds := make(map[int]*cache.Build)

eventLoop:
	for {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case errEvent := <-errc:
			if err == nil {
				err = errEvent
			}
			break eventLoop
		case event := <-eventc:
			switch event := event.(type) {
			case travisBuildUpdate:
				var build cache.Build
				if build, err = c.fetchBuild(ctx, repository.URL, event.remoteID); err != nil {
					cancel()
					break
				}
				builds[build.RemoteID] = &build
				select {
				case buildc <- build:
				case <-ctx.Done():
					err = ctx.Err()
				}
			case travisJobUpdate:
				var exists bool
				if _, exists = builds[event.buildRemoteID]; !exists {
					var build cache.Build
					if build, err = c.fetchBuild(ctx, repository.URL, event.buildRemoteID); err != nil {
						cancel()
						break
					}
					builds[build.RemoteID] = &build
				} else {
					if err = builds[event.buildRemoteID].SetJobState(event.jobKey, event.state); err != nil {
						cancel()
						break
					}
				}

				select {
				case buildc <- *builds[event.buildRemoteID]:
				case <-ctx.Done():
					err = ctx.Err()
				}
			}
		}
	}

	return err
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

func (c TravisClient) WebURL(repositoryURL string) (url.URL, error) {
	var err error
	webURL := c.baseURL
	webURL.Host = strings.TrimPrefix(webURL.Host, "api.")
	webURL.Path, err = utils.RepositorySlugFromURL(repositoryURL)

	return webURL, err
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

func (c TravisClient) fetchBuild(ctx context.Context, repositoryURL string, buildID int) (cache.Build, error) {
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

	// Fetch logs
	jobIDs := make([]int, len(build.Jobs))
	for i, job := range build.Jobs {
		jobIDs[i] = job.ID
	}
	logs, err := c.fetchJobLogs(ctx, jobIDs)
	if err != nil {
		return cache.Build{}, err
	}
	for i := range build.Jobs {
		build.Jobs[i].Log = logs[build.Jobs[i].ID].Content
	}

	webURL, err := c.WebURL(repositoryURL)
	if err != nil {
		return cache.Build{}, err
	}
	return build.ToInserters(c.accountID, repositoryURL, webURL.String())
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
	for _, build := range response.Builds {
		wg.Add(1)
		go func(buildID int) {
			defer wg.Done()
			build, err := c.fetchBuild(ctx, repositoryURL, buildID)
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

type travisLog struct {
	Content  string
	NbrParts int
}

func (c TravisClient) fetchJobLogs(ctx context.Context, jobIDs []int) (map[int]travisLog, error) {
	errc := make(chan error)
	wg := sync.WaitGroup{}
	logs := make(map[int]travisLog)
	logsMux := sync.Mutex{}
	subCtx, cancel := context.WithCancel(ctx)

	for _, ID := range jobIDs {
		wg.Add(1)
		go func(ID int) {
			defer wg.Done()

			var err error
			var log travisLog
			log.NbrParts, log.Content, err = c.fetchJobLog(subCtx, ID)
			if err != nil {
				errc <- err
				return
			}

			logsMux.Lock()
			defer logsMux.Unlock()
			logs[ID] = log
		}(ID)
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	// FIXME Report all errors
	var err error
	for e := range errc {
		if e != nil {
			cancel()
			if err == nil {
				err = e
			}
		}
	}

	return logs, err
}

func (c TravisClient) fetchJobLog(ctx context.Context, jobID int) (int, string, error) {
	var reqURL = c.baseURL
	reqURL.Path += fmt.Sprintf("/job/%d/log", jobID)

	body, err := c.get(ctx, reqURL)
	if err != nil {
		return 0, "", err
	}

	var log struct {
		Content string     `json:"content"`
		Parts   []struct{} `json:"log_parts"`
	}
	err = json.Unmarshal(body.Bytes(), &log)
	if err != nil {
		return 0, "", err
	}

	return len(log.Parts), log.Content, nil
}

func (c TravisClient) StreamLogs(ctx context.Context, writerByJobID map[int]io.WriteCloser) error {
	defer func() {
		for _, w := range writerByJobID {
			_ = w.Close() // FIXME Report error
		}
	}()

	channels := make([]string, 0, len(writerByJobID))
	for id := range writerByJobID {
		channels = append(channels, fmt.Sprintf("job-%d", id))
	}
	p, err := c.pusherClient(ctx, channels)
	if err != nil {
		return err
	}
	defer p.Close()

	// Fetch first part of logs from REST API
	jobIDs := make([]int, 0, len(writerByJobID))
	for ID := range writerByJobID {
		jobIDs = append(jobIDs, ID)
	}
	logsByID, err := c.fetchJobLogs(ctx, jobIDs)
	if err != nil {
		return err
	}
	receivedLogParts := make(map[int]int)
	for ID, log := range logsByID {
		if _, err = writerByJobID[ID].Write([]byte(log.Content)); err != nil {
			return err
		}
		receivedLogParts[ID] = log.NbrParts
	}

	eventc := make(chan travisEvent)
	errc := make(chan error)
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		errc <- c.events(subCtx, p, eventc)
	}()

eventLoop:
	for {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case e := <-errc:
			if err == nil {
				err = e
			}
			break eventLoop
		case event := <-eventc:
			switch event := event.(type) {
			case travisPusherJobLog:
				if event.Number >= receivedLogParts[event.RemoteID] {
					var log string
					log = utils.PostProcess(event.Log)
					if _, err = writerByJobID[event.RemoteID].Write([]byte(log)); err != nil {
						cancel()
						break
					}
					if event.Final {
						if err = writerByJobID[event.RemoteID].Close(); err != nil {
							cancel()
							break
						}
					}
					receivedLogParts[event.RemoteID] += 1
				}
			}
		}
	}

	return err
}

func (c TravisClient) pusherClient(ctx context.Context, channels []string) (p utils.PusherClient, err error) {
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
	if p, err = utils.NewPusherClient(ctx, wsURL, authURL.String(), authHeader, c.rateLimiter); err != nil {
		return p, err
	}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()

	if _, err = p.Expect(ctx, utils.ConnectionEstablished); err != nil {
		return p, err
	}

	chanFmt := "%s"
	if private {
		chanFmt = "private-" + chanFmt
	}
	for _, channel := range channels {
		if err = p.Subscribe(ctx, fmt.Sprintf(chanFmt, channel)); err != nil {
			return p, err
		}
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

type travisEvent interface {
	isTravisEvent()
}

type travisBuildUpdate struct {
	remoteID int
}

func (travisBuildUpdate) isTravisEvent() {}

type travisJobUpdate struct {
	buildRemoteID int
	jobKey        cache.JobKey
	state         cache.State
}

type travisPusherJobLog struct {
	RemoteID int    `json:"id"`
	Log      string `json:"_log"`
	Number   int    `json:"number"`
	Final    bool   `json:"final"`
}

func (travisPusherJobLog) isTravisEvent() {}

func (travisJobUpdate) isTravisEvent() {}

func (c TravisClient) events(ctx context.Context, p utils.PusherClient, teventc chan<- travisEvent) error {
	for {
		event, err := p.NextEvent(ctx)
		if err != nil {
			return err
		}

		var s string
		if err := json.Unmarshal(event.Data, &s); err != nil {
			return err
		}

		switch {
		// "build:created", "build:started", "build:finished"...
		case strings.HasPrefix(event.Event, "build:"):
			var data struct {
				Build struct {
					ID int `json:"id"`
				} `json:"build"`
			}
			if err := json.Unmarshal([]byte(s), &data); err != nil {
				return err
			}

			select {
			case teventc <- travisBuildUpdate{remoteID: data.Build.ID}:
			case <-ctx.Done():
				return ctx.Err()
			}

		case event.Event == "job:log":
			var tevent travisPusherJobLog
			if err := json.Unmarshal([]byte(s), &tevent); err != nil {
				return err
			}
			select {
			case teventc <- tevent:
			case <-ctx.Done():
				return ctx.Err()
			}

		// "job:queued", "job:received", "job:created", "job:started", "job:finished", "job:log"...
		case strings.HasPrefix(event.Event, "job:"):
			var data struct {
				BuildID int    `json:"build_id"`
				Number  string `json:"number"`
				State   string `json:"state"`
			}

			if err := json.Unmarshal([]byte(s), &data); err != nil {
				return err
			}

			jobKey, err := cache.NewJobKey(data.Number, c.accountID)
			if err != nil {
				return err
			}

			tevent := travisJobUpdate{
				buildRemoteID: data.BuildID,
				jobKey:        jobKey,
				state:         FromTravisState(data.State),
			}

			select {
			case teventc <- tevent:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
