package providers

import (
	"citop/cache"
	"encoding/json"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"io/ioutil"
	"net/http"
	"sync"
)

func updateErr(f func() error, err *error) {
	if e := f(); err == nil {
		err = &e
	}
}

type travisCommit struct {
	Id         int    `json:"id"`
	Sha        string `json:"sha"`
	Ref        string `json:"ref"`
	Message    string `json:"message"`
	CompareUrl string `json:"compare_url"`
}

type travisConfig struct {
	Name     string
	Os       string
	Distrib  string
	Language string
	Env      string
	Json     string
}

type travisJob struct {
	Id     int          `json:"id"`
	State  string       `json:"state"`
	Config travisConfig `json:"config"`
	Number string       `json:"number"`
	Log    string
}

type travisStage struct {
	Id     int         `json:"id"`
	Number int         `json:"number"`
	Name   string      `json:"name"`
	State  string      `json:"state"`
	Jobs   []travisJob `json:"jobs"`
}

type travisBuild struct {
	Id              int           `json:"id"`
	State           string        `json:"state"`
	Repository      string        `json:"repository.name"`
	RepoBuildNumber string        `json:"number"`
	Commit          travisCommit  `json:"commit"`
	Stages          []travisStage `json:"stages"`
}

type travisJsonResponse struct {
	Builds []travisBuild `json:"builds"`
}

type Travis struct {
	BaseUrl   string
	Token string
}

func (c *travisConfig) UnmarshalJSON(bytes []byte) (err error) {
	var m = make(map[string]interface{})
	if err = json.Unmarshal(bytes, &m); err != nil {
		return
	}

	// TODO: Simplify all of this with reflection for example
	switch name := m["name"].(type) {
	case string:
		c.Name = name
	}

	switch config_os := m["os"].(type) {
	case string:
		c.Os = config_os
	}

	switch distrib := m["dist"].(type) {
	case string:
		c.Distrib = distrib
	}

	switch env := m["env"].(type) {
	case string:
		c.Env = env
	}

	switch language := m["language"].(type) {
	case string:
		switch version := m[language].(type) {
		case string:
			c.Language = fmt.Sprintf("%s %s", language, version)
		default:
			c.Language = language
		}
	}

	c.Json = string(bytes)

	return
}

func localToDb(builds travisJsonResponse) (cacheBuilds []cache.Build) {
	for _, build := range builds.Builds {
		var dbStages []cache.Stage
		for _, stage := range build.Stages {
			var dbJobs []cache.Job
			for _, job := range stage.Jobs {
				dbJob := cache.Job{
					Id:    job.Id,
					State: job.State,
					Config: cache.Config{
						Name:     job.Config.Name,
						Os:       job.Config.Os,
						Distrib:  job.Config.Distrib,
						Language: job.Config.Language,
						Env:      job.Config.Env,
						Json:     job.Config.Json,
					},
					Number: job.Number,
					Log:    job.Log,
				}
				dbJobs = append(dbJobs, dbJob)
			}
			dbStage := cache.Stage{
				Id:     stage.Id,
				Number: stage.Number,
				Name:   stage.Name,
				State:  stage.State,
				Jobs:   dbJobs,
			}
			dbStages = append(dbStages, dbStage)
		}
		dbBuild := cache.Build{
			Id:              build.Id,
			State:           build.State,
			Repository:      build.Repository,
			RepoBuildNumber: build.RepoBuildNumber,
			Commit: cache.Commit{
				Id:         build.Commit.Id,
				Sha:        build.Commit.Sha,
				Ref:        build.Commit.Ref,
				Message:    build.Commit.Message,
				CompareUrl: build.Commit.CompareUrl,
			},
			Stages: dbStages,
		}
		cacheBuilds = append(cacheBuilds, dbBuild)
	}

	return cacheBuilds
}

func (t Travis) FetchBuilds(count int) (builds []cache.Build, err error) {
	client := &http.Client{}

	req, err := http.NewRequest("GET", "https://api.travis-ci.org/builds", nil)
	if err != nil {
		return builds, err
	}
	req.Header.Add("Travis-API-Version", "3")
	req.Header.Add("Authorization", fmt.Sprintf("token %s", t.Token))

	query := req.URL.Query()
	query.Add("include", "build.stages,stage.jobs,job.config")
	query.Add("limit", "20")
	req.URL.RawQuery = query.Encode()

	response, err := client.Do(req)
	if err != nil {
		return builds, err
	}
	defer func() {
		if errClose := response.Body.Close; err == nil && errClose() != nil {
			err = errClose()
		}
	}()

	data, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return builds, err
	}

	localBuilds := travisJsonResponse{}
	err = json.Unmarshal(data, &localBuilds)
	if err != nil {
		return builds, err
	}
	var errMutex = sync.Mutex{}
	var wg = sync.WaitGroup{}
	for build_idx, build := range localBuilds.Builds {
		for stage_idx, stage := range build.Stages {
			for job_idx, job := range stage.Jobs {
				wg.Add(1)
				go fetchTravisLog(client, t.Token, job.Id, &localBuilds.Builds[build_idx].Stages[stage_idx].Jobs[job_idx], &errMutex, &err, &wg)
			}
		}
	}

	// FIXME There is no need for a sync here, once we have all data for a build it should be inserted in the database
	wg.Wait()
	if err != nil {
		return builds, err
	}

	builds = localToDb(localBuilds)
	return builds, err
}

func fetchTravisLog(client *http.Client, token string, jobId int, job *travisJob, mutex *sync.Mutex, sharedErr *error, wg *sync.WaitGroup) {
	defer wg.Done()

	url := fmt.Sprintf("https://api.travis-ci.org/job/%d/log", jobId)
	var err error

	// TODO: Could this be written more idiomatically by replacing these variables by a single LogErr?
	req, err := http.NewRequest("GET", url, nil)
	if err == nil {
		req.Header.Add("Travis-API-Version", "3")
		req.Header.Add("Authorization", fmt.Sprintf("token %s", token))

		response, err := client.Do(req)
		if err == nil {
			// TODO Check if it's sometimes worth retrying the request
			defer updateErr(response.Body.Close, &err)
			bytes, err := ioutil.ReadAll(response.Body)
			if err == nil {
				(*job).Log = string(bytes)
			}
		}
	}

	if err != nil {
		mutex.Lock()
		if *sharedErr != nil {
			sharedErr = &err
		}
		mutex.Unlock()
	}
}
