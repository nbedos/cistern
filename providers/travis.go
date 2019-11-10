package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
	baseURL        url.URL
	pusherHost     string
	httpClient     *http.Client
	rateLimiter    <-chan time.Time
	buildsPageSize int
	token          string
	accountID      string
}

var TravisOrgURL = url.URL{Scheme: "https", Host: "api.travis-ci.org"}
var TravisComURL = url.URL{Scheme: "https", Host: "api.travis-ci.com"}
var TravisPusherHost = "ws.pusherapp.com"

func NewTravisClient(URL url.URL, pusherHost string, token string, accountID string, rateLimit time.Duration) TravisClient {
	return TravisClient{
		baseURL:        URL,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		pusherHost:     pusherHost,
		rateLimiter:    time.Tick(rateLimit),
		token:          token,
		accountID:      accountID,
		buildsPageSize: 10,
	}
}

func (c TravisClient) AccountID() string {
	return c.accountID
}

func (c TravisClient) Builds(ctx context.Context, repositoryURL string, maxAge time.Duration, buildc chan<- cache.Build) error {
	repository, err := c.repository(ctx, repositoryURL)
	if err != nil {
		return err
	}

	// Create Pusher client
	repoChannel := fmt.Sprintf("repo-%d", repository.ID)
	p, err := c.pusherClient(ctx, []string{repoChannel})
	if err != nil {
		return err
	}
	defer p.Close()

	// Fetch build history and active builds list
	// FIXME Remove hardcoded values
	if err := c.repositoryBuilds(ctx, &repository, maxAge, buildc); err != nil {
		return err
	}

	errc := make(chan error)
	eventc := make(chan travisEvent)
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		errc <- c.events(subCtx, p, eventc)
	}()

	builds := make(map[string]*cache.Build)

eventLoop:
	for {
		select {
		case errEvent := <-errc:
			if err == nil {
				err = errEvent
			}
			break eventLoop
		case event := <-eventc:
			switch event := event.(type) {
			case travisBuildUpdate:
				var build cache.Build
				if build, err = c.fetchBuild(ctx, &repository, event.remoteID); err != nil {
					cancel()
					break
				}
				builds[build.ID] = &build
				select {
				case buildc <- build:
				case <-ctx.Done():
					err = ctx.Err()
				}
			case travisJobUpdate:
				var job cache.Job
				build, exists := builds[event.buildID]
				if exists {
					job, exists = build.Get(event.stageID, event.ID)
				}

				if exists {
					job.State = event.state
				} else {
					var build cache.Build
					if build, err = c.fetchBuild(ctx, &repository, event.buildID); err != nil {
						cancel()
						break
					}
					builds[build.ID] = &build
				}

				select {
				case buildc <- *builds[event.buildID]:
				case <-ctx.Done():
					err = ctx.Err()
				}
			}
		}
	}

	return err
}

func (c TravisClient) Log(ctx context.Context, repository cache.Repository, jobID int) (string, error) {
	_, log, err := c.fetchJobLog(ctx, jobID)
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

	body, err := c.get(ctx, reqURL)
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
func (c TravisClient) get(ctx context.Context, resourceURL url.URL) (*bytes.Buffer, error) {
	req, err := http.NewRequest("GET", resourceURL.String(), nil)
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
	return fmt.Sprintf("%q %s returned an error: status %d (%s)",
		err.Method, err.URL, err.Status, err.Message)
}

func (c TravisClient) fetchBuild(ctx context.Context, repository *cache.Repository, buildID string) (cache.Build, error) {
	buildURL := c.baseURL
	buildURL.Path += fmt.Sprintf("/build/%s", buildID)
	parameters := buildURL.Query()
	parameters.Add("include", "build.jobs,build.commit,job.config")
	buildURL.RawQuery = parameters.Encode()

	body, err := c.get(ctx, buildURL)
	if err != nil {
		return cache.Build{}, err
	}

	var build travisBuild
	err = json.Unmarshal(body.Bytes(), &build)

	webURL, err := c.webURL(*repository)
	if err != nil {
		return cache.Build{}, err
	}
	return build.toCacheBuild(c.accountID, repository, webURL.String())
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

	body, err := c.get(ctx, buildsURL)
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

func (c TravisClient) repositoryBuilds(ctx context.Context, repository *cache.Repository, maxAge time.Duration, buildc chan<- cache.Build) error {
	wg := sync.WaitGroup{}
	errc := make(chan error)
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg.Add(1)
	go func() {
		defer wg.Done()

		lastPage := false
		for offset := 0; !lastPage; offset += c.buildsPageSize {
			builds, err := c.fetchBuilds(subCtx, repository, offset, c.buildsPageSize)
			if err != nil {
				errc <- err
				return
			}
			if len(builds) == 0 {
				lastPage = true
			}

			for _, build := range builds {
				date := build.StartedAt
				if date == "" {
					date = build.UpdatedAt
				}
				t, err := utils.NullTimeFromString(date)
				if err == nil && t.Valid && time.Since(t.Time) > maxAge {
					lastPage = true
					continue
				}

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
				}(strconv.Itoa(build.ID))
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

func (c TravisClient) StreamLog(ctx context.Context, repositoryID int, jobID int, writer io.Writer) error {
	var err error

	p, err := c.pusherClient(ctx, []string{fmt.Sprintf("job-%d", jobID)})
	if err != nil {
		return err
	}
	defer p.Close()

	nbrParts, content, err := c.fetchJobLog(ctx, jobID)
	log := utils.PostProcess(content)
	if _, err = writer.Write([]byte(log)); err != nil {
		return err
	}

	errEvent := make(chan error)
	eventc := make(chan travisEvent)
	subCtx, cancel := context.WithCancel(ctx)

	go func() {
		errEvent <- c.events(subCtx, p, eventc)
	}()

	errSet := false
	for event := range eventc {
		switch event := event.(type) {
		case travisPusherJobLog:
			// FIXME Check that log parts are received in the right order.
			if event.Number >= nbrParts {
				// FIXME Use utils.ANSIStripper
				log := utils.PostProcess(event.Log)
				if _, err = writer.Write([]byte(log)); err != nil || event.Final {
					errSet = true
					cancel()
					break
				}
				nbrParts++
			}
		}
	}
	if e := <-errEvent; !errSet {
		err = e
	}

	return err
}

func (c TravisClient) pusherClient(ctx context.Context, channels []string) (p utils.PusherClient, err error) {
	pusherKey, private, err := c.fetchPusherConfig(ctx)
	if err != nil {
		return p, err
	}

	wsURL := utils.PusherURL(c.pusherHost, pusherKey)
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
			p.Close() // FIXME Return error
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
	remoteID string
}

func (travisBuildUpdate) isTravisEvent() {}

type travisJobUpdate struct {
	accountID string
	buildID   string
	stageID   int
	ID        int
	state     cache.State
}

func (travisJobUpdate) isTravisEvent() {}

type travisPusherJobLog struct {
	RemoteID int    `json:"id"`
	Log      string `json:"_log"`
	Number   int    `json:"number"`
	Final    bool   `json:"final"`
}

func (travisPusherJobLog) isTravisEvent() {}

// FIXME Isolate transformation from pusher event to travis event for easier testing
func (c TravisClient) events(ctx context.Context, p utils.PusherClient, teventc chan<- travisEvent) error {
	defer close(teventc)

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
			case teventc <- travisBuildUpdate{remoteID: strconv.Itoa(data.Build.ID)}:
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
				ID      int    `json:"id"`
				BuildID int    `json:"build_id"`
				State   string `json:"state"`
				Stage   *struct {
					ID int `json:"id"`
				} `json:"stage"`
			}

			if err := json.Unmarshal([]byte(s), &data); err != nil {
				return err
			}

			stageID := 0
			if data.Stage != nil {
				stageID = data.Stage.ID
			}

			tevent := travisJobUpdate{
				accountID: c.accountID,
				buildID:   strconv.Itoa(data.BuildID),
				stageID:   stageID,
				ID:        data.ID,
				state:     fromTravisState(data.State),
			}

			select {
			case teventc <- tevent:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
