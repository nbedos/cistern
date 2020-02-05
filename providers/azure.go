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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nbedos/cistern/utils"
)

type AzurePipelinesClient struct {
	baseURL     url.URL
	httpClient  *http.Client
	rateLimiter <-chan time.Time
	token       string
	provider    Provider
	version     string
	mux         *sync.Mutex
}

var azureURL = url.URL{
	Scheme: "https",
	Host:   "dev.azure.com",
}

func NewAzurePipelinesClient(id string, name string, token string, requestsPerSecond float64) AzurePipelinesClient {
	rateLimit := time.Second / 10
	if requestsPerSecond > 0 {
		rateLimit = time.Second / time.Duration(requestsPerSecond)
	}

	return AzurePipelinesClient{
		baseURL:     azureURL,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		rateLimiter: time.Tick(rateLimit),
		token:       token,
		provider: Provider{
			ID:   id,
			Name: name,
		},
		version: "5.1",
		mux:     &sync.Mutex{},
	}
}

func (c AzurePipelinesClient) ID() string {
	return c.provider.ID
}

func (c AzurePipelinesClient) Host() string {
	return c.baseURL.Host
}

func (c AzurePipelinesClient) Name() string {
	return c.provider.Name
}

func (c AzurePipelinesClient) parseAzureWebURL(s string) (string, string, string, error) {
	// https://dev.azure.com/nicolasbedos/5190ee7b-d826-445e-b19e-6dc098be0436/_build/results?buildId=16
	u, err := url.Parse(s)
	if err != nil {
		return "", "", "", err
	}
	if u.Hostname() != c.baseURL.Hostname() {
		return "", "", "", ErrUnknownPipelineURL
	}

	cs := strings.Split(u.EscapedPath(), "/")
	if len(cs) < 5 || cs[3] != "_build" || cs[4] != "results" {
		return "", "", "", ErrUnknownPipelineURL
	}
	owner, repo := cs[1], cs[2]

	buildID := u.Query().Get("buildId")
	if buildID == "" {
		return "", "", "", ErrUnknownPipelineURL
	}

	return owner, repo, buildID, nil
}

func (c AzurePipelinesClient) BuildFromURL(ctx context.Context, u string) (Pipeline, error) {
	owner, repo, buildID, err := c.parseAzureWebURL(u)
	if err != nil {
		return Pipeline{}, err
	}

	return c.fetchPipeline(ctx, owner, repo, buildID)
}

func (c AzurePipelinesClient) Log(ctx context.Context, step Step) (string, error) {
	if step.Log.Key == "" {
		return "", ErrNoLogHere
	}

	u, err := url.Parse(step.Log.Key)
	if err != nil {
		return "", err
	}

	r, err := c.get(ctx, *u)
	if err != nil {
		return "", err
	}
	defer func() {
		if errClose := r.Close(); err == nil {
			err = errClose
		}
	}()

	log, err := ioutil.ReadAll(r)
	if err != nil {
		return "", err
	}

	return string(log), nil
}

type azureBuild struct {
	ID                int        `json:"id"`
	Number            string     `json:"buildNumber"`
	SourceBranch      string     `json:"sourceBranch"`
	SourceVersion     string     `json:"sourceVersion"`
	ValidationResults []struct{} `json:"validationResults"`
	Status            string     `json:"status"`
	Result            string     `json:"result"`
	QueueTime         string     `json:"queuetime"`
	StartTime         string     `json:"startTime"`
	FinishTime        string     `json:"finishTime"`
	Definition        struct {
		Name string `json:"name"`
	} `json:"definition"`
	Links struct {
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
		URL string `json:"url"`
	} `json:"logs"`
	Repository struct {
		ID string `json:"id"`
	} `json:"repository"`
}

func (b azureBuild) toPipeline() (Pipeline, error) {
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

	pipeline := Pipeline{
		Number: b.Number,
		GitReference: GitReference{
			SHA:   b.SourceVersion,
			Ref:   ref,
			IsTag: isTag,
		},
		Step: Step{
			ID:    strconv.Itoa(b.ID),
			Name:  b.Definition.Name,
			Type:  StepPipeline,
			State: fromAzureState(b.Result, b.Status),
			WebURL: utils.NullString{
				Valid:  b.Links.Web.Href != "",
				String: b.Links.Web.Href,
			},
		},
	}

	var err error
	pipeline.CreatedAt, err = utils.NullTimeFromString(b.QueueTime)
	if err != nil {
		return Pipeline{}, err
	}
	pipeline.StartedAt, err = utils.NullTimeFromString(b.StartTime)
	if err != nil {
		return Pipeline{}, err
	}
	pipeline.FinishedAt, err = utils.NullTimeFromString(b.FinishTime)
	if err != nil {
		return Pipeline{}, err
	}
	if pipeline.UpdatedAt, err = utils.NullTimeFromString(b.LastChangedDate); err != nil {
		return Pipeline{}, err
	}
	pipeline.Duration = utils.NullSub(pipeline.FinishedAt, pipeline.StartedAt)

	return pipeline, nil
}

func (c AzurePipelinesClient) fetchPipeline(ctx context.Context, owner string, repo string, id string) (Pipeline, error) {
	u := c.baseURL
	u.Path += fmt.Sprintf("/%s/%s/_apis/build/builds", owner, repo)
	params := u.Query()
	params.Add("buildIds", id)
	u.RawQuery = params.Encode()

	builds := struct {
		Value []azureBuild `json:"value"`
	}{}
	if err := c.getJSON(ctx, u, &builds); err != nil {
		return Pipeline{}, err
	}

	if len(builds.Value) == 0 {
		return Pipeline{}, ErrUnknownPipelineURL
	} else if len(builds.Value) > 1 {
		return Pipeline{}, errors.New("expected a single build in response")
	}

	azureBuild := builds.Value[0]
	pipeline, err := azureBuild.toPipeline()
	if err != nil {
		return Pipeline{}, err
	}
	// FIXME Show the error message to the user
	// Fetching the timeline of a pipeline that was misconfigured will
	// return EOF so check ValidationResults first
	if len(azureBuild.ValidationResults) > 0 {
		return pipeline, nil
	}
	if !pipeline.WebURL.Valid {
		return pipeline, fmt.Errorf("missing web URL for pipeline #%s", pipeline.ID)
	}

	webURL, err := url.Parse(pipeline.WebURL.String)
	if err != nil {
		return Pipeline{}, err
	}
	if webURL == nil {
		return pipeline, fmt.Errorf("missing web URL for pipeline #%s", pipeline.ID)
	}

	stages, err := c.fetchStages(ctx, azureBuild.Links.Timeline.Href, *webURL)
	if err != nil {
		return Pipeline{}, err
	}

	for i := range stages {
		stages[i].CreatedAt = pipeline.CreatedAt
		for j := range stages[i].Children {
			stages[i].Children[j].CreatedAt = pipeline.CreatedAt
			for k := range stages[i].Children[j].Children {
				stages[i].Children[j].Children[k].CreatedAt = pipeline.CreatedAt
			}
		}
	}

	// If there is a single stage named "__default" just remove it and populate pipeline.Children
	// with the jobs of the stage
	isDefaultStage := false
	if len(stages) == 1 {
		for _, stage := range stages {
			isDefaultStage = stage.Name == "__default"
		}
	}
	if isDefaultStage {
		for _, stage := range stages {
			pipeline.Children = stage.Children
		}
	} else {
		pipeline.Children = stages
	}

	return pipeline, err
}

func (c AzurePipelinesClient) fetchStages(ctx context.Context, u string, webURL url.URL) ([]Step, error) {
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
		case "job", "task", "phase", "stage":
			record := record // kill me now
			recordsByID[record.ID] = &record
		}
	}

	// Build tree structure from flat list and ID -> parentIDs links
	topLevelRecords := make([]*azureRecord, 0)
	for _, record := range recordsByID {
		if record.ParentID != "" {
			parent, exists := recordsByID[record.ParentID]
			if !exists {
				return nil, fmt.Errorf("ParentID not found: %q", record.ParentID)
			}
			parent.children = append(parent.children, record)
		} else {
			topLevelRecords = append(topLevelRecords, record)
		}
	}

	// Sort stages
	sort.Slice(topLevelRecords, func(i, j int) bool {
		return topLevelRecords[i].Order < topLevelRecords[j].Order
	})
	// Sort jobs and tasks
	for _, record := range recordsByID {
		sort.Slice(record.children, func(i, j int) bool {
			return record.children[i].Order < record.children[j].Order
		})
	}

	// At this point we have a tree structure with the following hierarchy of record.type_ :
	//    Stage -> Phase -> Job -> Task
	// For now we ignore tasks. Phases that contain jobs are redundant with the jobs themselves
	// so we ignore them too. Phase that have no child are turned into a providers.Job.
	// (this is consistent with the way jobs are shown on the Azure website)
	steps := make([]Step, 0, len(topLevelRecords))
	for _, record := range topLevelRecords {
		stages, err := record.toSteps(webURL)
		if err != nil {
			return nil, err
		}
		steps = append(steps, stages...)
	}

	return steps, nil
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

func (r azureRecord) toSteps(webURL url.URL) ([]Step, error) {
	// A phase is a special case since it may or may not have children
	// A phase without any children is treated as if it were a job
	// A phase with children is ignored, but its children, which are jobs, are returned
	// (this is consistent with the way jobs are shown on the Azure website)
	if t := strings.ToLower(r.Type); t == "phase" {
		var records []*azureRecord
		if len(r.children) == 0 {
			j := r
			j.Type = "job"
			records = []*azureRecord{&j}
		} else {
			records = r.children
		}

		allJobs := make([]Step, 0)
		for _, record := range records {
			jobs, err := record.toSteps(webURL)
			if err != nil {
				return nil, err
			}
			allJobs = append(allJobs, jobs...)
		}

		return allJobs, nil
	}

	step := Step{
		ID:    r.ID,
		State: fromAzureState(r.Result, r.State),
		Name:  r.Name,
		Log: Log{
			Key: r.Log.URL,
		},
		AllowFailure: false,
	}

	switch strings.ToLower(r.Type) {
	case "stage":
		step.Type = StepStage
		query := webURL.Query()
		query.Add("s", r.ID)
		query.Add("view", "logs")
		webURL.RawQuery = query.Encode()

	case "job":
		step.Type = StepJob
		query := webURL.Query()
		query.Del("s")
		query.Add("j", r.ID)
		webURL.RawQuery = query.Encode()

	case "task":
		step.Type = StepTask
		query := webURL.Query()
		query.Add("t", r.ID)
		webURL.RawQuery = query.Encode()

	default:
		return nil, fmt.Errorf("unknown record type: %q", r.Type)
	}

	step.WebURL = utils.NullString{
		Valid:  true,
		String: webURL.String(),
	}

	var err error
	step.StartedAt, err = utils.NullTimeFromString(r.StartTime)
	if err != nil {
		return nil, err
	}
	step.FinishedAt, err = utils.NullTimeFromString(r.FinishTime)
	if err != nil {
		return nil, err
	}
	step.Duration = utils.NullSub(step.FinishedAt, step.StartedAt)

	for _, childRecord := range r.children {
		tasks, err := childRecord.toSteps(webURL)
		if err != nil {
			return nil, err
		}
		step.Children = append(step.Children, tasks...)
	}

	return []Step{step}, nil
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
		return nil, fmt.Errorf("expected url host to be %q but got %q", u.Hostname(), c.baseURL.Hostname())
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

	// 401 Unauthorized
	if resp.StatusCode == 401 {
		return nil, ErrUnknownPipelineURL
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

	return resp.Body, nil
}

func fromAzureState(result string, status string) State {
	switch result {
	case "canceled", "abandoned":
		return Canceled
	case "partiallySucceeded", "succeededWithIssues", "failed":
		return Failed
	case "skipped":
		return Skipped
	case "succeeded":
		return Passed
	case "none", "":
		switch status {
		case "inProgress", "cancelling":
			return Running
		case "notStarted", "postponed", "pending":
			return Pending
		case "completed", "none", "":
			// Do we ever take this path?
			return Unknown
		}
	}

	return Unknown
}
