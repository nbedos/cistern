package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
)

type AzurePipelinesClient struct {
	baseURL       url.URL
	httpClient    *http.Client
	rateLimiter   <-chan time.Time
	token         string
	provider      cache.Provider
	version       string
	logURLByJobID map[string]url.URL
	mux           *sync.Mutex
}

var azureURL = url.URL{
	Scheme: "https",
	Host:   "dev.azure.com",
}

func NewAzurePipelinesClient(id string, name string, token string, rateLimit time.Duration) AzurePipelinesClient {
	return AzurePipelinesClient{
		baseURL:     azureURL,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		rateLimiter: time.Tick(rateLimit),
		token:       token,
		provider: cache.Provider{
			ID:   id,
			Name: name,
		},
		version:       "5.1",
		logURLByJobID: make(map[string]url.URL),
		mux:           &sync.Mutex{},
	}
}

func (c AzurePipelinesClient) ID() string {
	return c.provider.ID
}

func (c AzurePipelinesClient) parseAzureWebURL(s string) (string, string, string, error) {
	// https://dev.azure.com/nicolasbedos/5190ee7b-d826-445e-b19e-6dc098be0436/_build/results?buildId=16
	u, err := url.Parse(s)
	if err != nil {
		return "", "", "", err
	}
	if u.Hostname() != c.baseURL.Hostname() {
		return "", "", "", cache.ErrUnknownURL
	}

	cs := strings.Split(u.EscapedPath(), "/")
	if len(cs) < 5 || cs[3] != "_build" || cs[4] != "results" {
		return "", "", "", cache.ErrUnknownURL
	}
	owner, repo := cs[1], cs[2]

	buildID := u.Query().Get("buildId")
	if buildID == "" {
		return "", "", "", cache.ErrUnknownURL
	}

	return owner, repo, buildID, nil
}

func (c AzurePipelinesClient) BuildFromURL(ctx context.Context, u string) (cache.Build, error) {
	owner, repo, buildID, err := c.parseAzureWebURL(u)
	if err != nil {
		return cache.Build{}, err
	}

	return c.fetchBuild(ctx, owner, repo, buildID)
}

func (c AzurePipelinesClient) Log(ctx context.Context, repository cache.Repository, jobID string) (string, error) {
	c.mux.Lock()
	logURL, exists := c.logURLByJobID[jobID]
	c.mux.Unlock()
	if !exists {
		return "", cache.ErrNoLogHere
	}

	var err error
	r, err := c.get(ctx, logURL)
	if err != nil {
		return "", err
	}
	defer func() {
		if errClose := r.Close(); err == nil {
			err = errClose
		}
	}()

	buf := bytes.Buffer{}
	if _, err = buf.ReadFrom(r); err != nil {
		return "", err
	}

	return buf.String(), nil
}

type azureBuild struct {
	ID            int    `json:"id"`
	Number        string `json:"buildNumber"`
	SourceBranch  string `json:"sourceBranch"`
	SourceVersion string `json:"sourceVersion"`
	Status        string `json:"status"`
	Result        string `json:"result"`
	QueueTime     string `json:"queuetime"`
	StartTime     string `json:"startTime"`
	FinishTime    string `json:"finishTime"`
	Links         struct {
		Timeline struct {
			Href string `json:"href"`
		} `json:"timeline"`
		Web struct {
			Href string `json:"href"`
		} `json:"web"`
	} `json:"_links"`
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	LastChangedDate string `json:"lastChangedDate"`
	Logs            struct {
		Url string `json:"url"`
	} `json:"logs"`
	Repository struct {
		ID string `json:"id"`
	} `json:"repository"`
}

func (b azureBuild) toCacheBuild(p cache.Provider) (cache.Build, error) {
	cs := strings.Split(b.Repository.ID, "/")
	if len(cs) != 2 {
		return cache.Build{}, fmt.Errorf("invalid repository slug: %s", b.Repository.ID)
	}
	owner, repo := cs[0], cs[1]

	var ref string
	var isTag bool
	switch {
	case strings.HasPrefix(b.SourceBranch, "refs/heads/"):
		ref = strings.TrimPrefix(b.SourceBranch, "refs/heads/")
		isTag = false
	case strings.HasPrefix(b.SourceBranch, "refs/tags/"):
		ref = strings.TrimPrefix(b.SourceBranch, "refs/tags/")
		isTag = true
	}

	build := cache.Build{
		Repository: &cache.Repository{
			Provider: p,
			ID:       0,
			URL:      "",
			Owner:    owner,
			Name:     repo,
		},
		ID: strconv.Itoa(b.ID),
		Commit: cache.Commit{
			Sha:     b.SourceVersion,
			Message: "",
			Date:    utils.NullTime{},
		},
		Ref:             ref,
		IsTag:           isTag,
		RepoBuildNumber: b.Number,
		State:           fromAzureState(b.Result, b.Status),
		Duration:        utils.NullDuration{},
		WebURL:          b.Links.Web.Href,
		Stages:          map[int]*cache.Stage{},
	}

	var err error
	build.CreatedAt, err = utils.NullTimeFromString(b.QueueTime)
	if err != nil {
		return cache.Build{}, err
	}
	build.StartedAt, err = utils.NullTimeFromString(b.StartTime)
	if err != nil {
		return cache.Build{}, err
	}
	build.FinishedAt, err = utils.NullTimeFromString(b.FinishTime)
	if err != nil {
		return cache.Build{}, err
	}
	if build.UpdatedAt, err = time.Parse(time.RFC3339, b.LastChangedDate); err != nil {
		return cache.Build{}, err
	}
	build.Duration = utils.NullSub(build.FinishedAt, build.StartedAt)

	return build, nil
}

func (c AzurePipelinesClient) fetchBuild(ctx context.Context, owner string, repo string, id string) (cache.Build, error) {
	u := c.baseURL
	u.Path += fmt.Sprintf("/%s/%s/_apis/build/builds", owner, repo)
	params := u.Query()
	params.Add("buildIds", id)
	u.RawQuery = params.Encode()

	builds := struct {
		Value []azureBuild `json:"value"`
	}{}
	if err := c.getJSON(ctx, u, &builds); err != nil {
		return cache.Build{}, err
	}
	if len(builds.Value) != 1 {
		return cache.Build{}, errors.New("expected a single build in response")
	}

	azureBuild := builds.Value[0]
	build, err := azureBuild.toCacheBuild(c.provider)
	if err != nil {
		return cache.Build{}, err
	}

	stages, err := c.getTimeline(ctx, azureBuild.Links.Timeline.Href)
	if err != nil {
		return cache.Build{}, err
	}

	for _, stage := range stages {
		for _, job := range stage.Jobs {
			job.CreatedAt = build.CreatedAt
		}
	}

	// If there is a single stage named "__default" just remove it and populate build.Jobs with
	// the jobs of the stage
	isDefaultStage := false
	if len(stages) == 1 {
		for _, stage := range stages {
			isDefaultStage = stage.Name == "__default"
		}
	}
	if isDefaultStage {
		for _, stage := range stages {
			build.Jobs = stage.Jobs
		}
	} else {
		build.Stages = stages
	}

	return build, err
}

func (c AzurePipelinesClient) getTimeline(ctx context.Context, u string) (map[int]*cache.Stage, error) {
	timelineURL, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	var timeline struct {
		Records       []azureRecord `json:"records"`
		ID            string        `json:"id"`
		LastChangedOn string        `json:"lastChangedOn"`
	}
	if err := c.getJSON(ctx, *timelineURL, &timeline); err != nil {
		return nil, err
	}

	recordsByID := make(map[string]*azureRecord)

	for _, record := range timeline.Records {
		switch strings.ToLower(record.Type) {
		case "stage", "phase", "job":
			record := record // kill me now
			recordsByID[record.ID] = &record
		}

		if strings.ToLower(record.Type) == "job" && record.Log.URL != "" {
			u, err := url.Parse(record.Log.URL)
			if err != nil {
				return nil, err
			}
			c.mux.Lock()
			c.logURLByJobID[record.ID] = *u
			c.mux.Unlock()
		}
	}

	// Build tree structure from flat list and ID -> parentIDs links
	topLevelRecords := make([]*azureRecord, 0)
	for _, record := range recordsByID {
		if record.ParentID != "" {
			parent, exists := recordsByID[record.ParentID]
			if !exists {
				return nil, errors.New("ParentID not found")
			}
			parent.children = append(parent.children, record)
			sortByOrder(parent.children) // meh.
		} else {
			topLevelRecords = append(topLevelRecords, record)
		}
	}

	// At this point we have a tree structure with the following hierarchy of 'record.Type's :
	//    Stage -> Phase -> Job -> Task
	// For now we ignore Tasks. Phases that contain jobs are redundant with the jobs themselves
	// so we ignore them too. Phase that have no child are turned into a cache.Job.
	// (this is consistent with the way jobs are shown on the Azure website)
	stages := make(map[int]*cache.Stage)
	for _, record := range topLevelRecords {
		stage, err := record.ToCacheStage()
		if err != nil {
			return nil, err
		}
		stages[stage.ID] = &stage
	}

	return stages, nil
}

type azureRecord struct {
	ID           string `json:"id"`
	ParentID     string `json:"parentId"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	StartTime    string `json:"startTime"`
	FinishTime   string `json:"finishTime"`
	State        string `json:"state"`
	Result       string `json:"result"`
	LastModified string `json:"lastModified"`
	Order        int    `json:"order"`
	Log          struct {
		URL string `json:"url"`
	} `json:"log"`
	children []*azureRecord
}

func sortByOrder(records []*azureRecord) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].Order < records[j].Order
	})
}

func (r azureRecord) ToCacheStage() (cache.Stage, error) {
	if t := strings.ToLower(r.Type); t != "stage" {
		return cache.Stage{}, fmt.Errorf("expected record of type 'stage' but got %q", t)
	}

	if r.Order == 0 {
		return cache.Stage{}, errors.New("record order for stage cannot be zero")
	}

	stageJobs := make([]*cache.Job, 0)
	for _, record := range r.children {
		jobs, err := record.ToCacheJobs()
		if err != nil {
			return cache.Stage{}, err
		}
		stageJobs = append(stageJobs, jobs...)
	}

	return cache.Stage{
		ID:    r.Order,
		Name:  r.Name,
		State: fromAzureState(r.Result, r.State),
		Jobs:  stageJobs,
	}, nil
}

func (r azureRecord) ToCacheJobs() ([]*cache.Job, error) {
	if t := strings.ToLower(r.Type); t != "phase" {
		return nil, fmt.Errorf("expected record of type 'phase' but got %q", t)
	}

	// Treat a phase without children (=phase that has not started) as a single job.
	// After starting the phase may have multiple children but we don't have that information
	// yet.
	records := []*azureRecord{&r}
	if len(r.children) != 0 {
		// If a phase has children, ignore the phase itself and work with its children which are
		// jobs
		records = r.children
	}

	jobs := make([]*cache.Job, 0)
	for _, record := range records {
		job, err := record.ToCacheJob()
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, &job)
	}

	return jobs, nil
}

func (r azureRecord) ToCacheJob() (cache.Job, error) {
	job := cache.Job{
		ID:           r.ID,
		State:        fromAzureState(r.Result, r.State),
		Name:         r.Name,
		Log:          utils.NullString{},
		WebURL:       "",
		AllowFailure: false,
	}

	var err error
	job.StartedAt, err = utils.NullTimeFromString(r.StartTime)
	if err != nil {
		return cache.Job{}, err
	}
	job.FinishedAt, err = utils.NullTimeFromString(r.FinishTime)
	if err != nil {
		return cache.Job{}, err
	}
	job.Duration = utils.NullSub(job.FinishedAt, job.StartedAt)

	return job, nil
}

func (c AzurePipelinesClient) getJSON(ctx context.Context, u url.URL, v interface{}) error {
	var err error
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

func (c AzurePipelinesClient) get(ctx context.Context, u url.URL) (io.ReadCloser, error) {
	if u.Hostname() != c.baseURL.Hostname() {
		return nil, fmt.Errorf("expected URL host to be %q but got %q", u.Hostname(), c.baseURL.Hostname())
	}
	params := u.Query()
	params.Add("api-version", c.version)
	u.RawQuery = params.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.WithContext(ctx)

	if c.token != "" {
		req.SetBasicAuth("", c.token)
	}

	select {
	case <-c.rateLimiter:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		buf := bytes.Buffer{}
		buf.ReadFrom(resp.Body)
		resp.Body.Close()
		return nil, HTTPError{
			Method:  req.Method,
			URL:     u.String(),
			Status:  resp.StatusCode,
			Message: buf.String(),
		}
	}

	return resp.Body, nil
}

func fromAzureState(result string, status string) cache.State {
	switch result {
	case "canceled", "abandoned":
		return cache.Canceled
	case "partiallySucceeded", "succeededWithIssues", "failed":
		return cache.Failed
	case "skipped":
		return cache.Skipped
	case "succeeded":
		return cache.Passed
	case "none", "":
		switch status {
		case "inProgress", "cancelling":
			return cache.Running
		case "notStarted", "postponed", "pending":
			return cache.Pending
		case "completed", "none", "":
			// Do we ever take this path?
			return cache.Unknown
		}
	}

	return cache.Unknown
}
