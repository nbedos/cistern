package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
)

func Setup() (AzurePipelinesClient, func(), error) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := ""
		switch {
		case r.Method == "GET" && r.URL.Path == "/owner/repo/_apis/build/builds":
			filename = "test_data/azure_build_16.json"
		case r.Method == "GET" && r.URL.Path == "/owner/repo/_apis/build/builds/16/Timeline":
			filename = "test_data/azure_build_16_timeline.json"
		case r.Method == "GET" && r.URL.Path == "/owner/repo/_apis/build/builds/16/logs/1234":
			filename = "test_data/azure_build_16_job_log.txt"
		default:
			w.WriteHeader(404)
			return
		}

		bs, err := ioutil.ReadFile(filename)
		if err != nil {
			w.WriteHeader(500)
			fmt.Fprint(w, err.Error())
			return
		}

		// Rewrite URLs in the file to match the scheme and host of the query
		s := strings.ReplaceAll(string(bs), "https://example.com", "http://"+r.Host)
		if _, err := fmt.Fprint(w, s); err != nil {
			w.WriteHeader(500)
			fmt.Fprint(w, err.Error())
			return
		}
	}))

	baseURL, err := url.Parse(testServer.URL)
	if err != nil {
		return AzurePipelinesClient{}, nil, err
	}

	client := AzurePipelinesClient{
		baseURL:     *baseURL,
		httpClient:  testServer.Client(),
		rateLimiter: time.Tick(time.Millisecond),
		token:       "",
		provider: cache.Provider{
			ID:   "azure",
			Name: "azure",
		},
		version:       "5.1",
		logURLByJobID: map[string]url.URL{},
		mux:           &sync.Mutex{},
	}

	teardown := func() {
		testServer.Close()
	}
	return client, teardown, nil
}

var expectedBuild = cache.Build{
	Repository: &cache.Repository{
		Provider: cache.Provider{
			ID:   "azure",
			Name: "azure",
		},
		Owner: "owner",
		Name:  "repo",
	},
	ID:              "16",
	Sha:             "5e4d496d63086609cb3c03aa0ee4e032e4b6b08b",
	Ref:             "azure-pipelines",
	RepoBuildNumber: "20191204.3",
	State:           cache.Failed,
	CreatedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 12, 4, 13, 9, 34, 734161200, time.UTC),
	},
	StartedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 12, 4, 13, 9, 52, 764105000, time.UTC),
	},
	FinishedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 12, 4, 13, 11, 34, 339701300, time.UTC),
	},
	UpdatedAt: time.Date(2019, 12, 4, 13, 11, 34, 487000000, time.UTC),
	Duration: utils.NullDuration{
		Valid:    true,
		Duration: time.Minute + 41*time.Second + 575596300*time.Nanosecond,
	},
	WebURL: "http://HOST/owner/repo/_build/results?buildId=16",
	Stages: map[int]*cache.Stage{
		1: {
			ID:    1,
			Name:  "tests",
			State: "failed",
			Jobs: []*cache.Job{
				{
					ID:    "05f50c00-03d1-5f30-b292-f8c1b53561cb",
					State: "failed",
					Name:  "Ubuntu_16_04",
					CreatedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 9, 34, 734161200, time.UTC),
					},
					StartedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 10, 0, 713333300, time.UTC),
					},
					FinishedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 11, 30, 296666700, time.UTC),
					},
					Duration: utils.NullDuration{
						Valid:    true,
						Duration: time.Minute + 29*time.Second + 583333400*time.Nanosecond,
					},
				},
				{
					ID:    "ff10d40d-f057-5007-e152-c3ec22cd43f4",
					State: "failed",
					Name:  "Ubuntu_18_04",
					CreatedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 9, 34, 734161200, time.UTC),
					},
					StartedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 9, 56, 653333300, time.UTC),
					},
					FinishedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 11, 17, 26666700, time.UTC),
					},
					Duration: utils.NullDuration{
						Valid:    true,
						Duration: time.Minute + 20*time.Second + 373333400*time.Nanosecond,
					},
				},
				{
					ID:    "3d7e5cc9-b1ff-5c85-9fc2-b7644452fdf5",
					State: "failed",
					Name:  "macOS_10_13",
					CreatedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 9, 34, 734161200, time.UTC),
					},
					StartedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 10, 0, 693333300, time.UTC),
					},
					FinishedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 10, 27, 10000000, time.UTC),
					},
					Duration: utils.NullDuration{
						Valid:    true,
						Duration: 26*time.Second + 316666700*time.Nanosecond,
					},
				},
				{
					ID:    "aa83c9de-d200-5148-7d44-5e08a0dd6659",
					State: "failed",
					Name:  "macoOS_10_14",
					CreatedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 9, 34, 734161200, time.UTC),
					},
					StartedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 9, 59, 943333300, time.UTC),
					},
					FinishedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 4, 13, 10, 4, 753333300, time.UTC),
					},
					Duration: utils.NullDuration{
						Valid:    true,
						Duration: 4*time.Second + 810*time.Millisecond,
					},
				},
			},
		},
	},
}

func TestAzurePipelinesClient_parseAzureWebURL(t *testing.T) {
	webURL := "https://dev.azure.com/owner/repo/_build/results?buildId=16"
	client := NewAzurePipelinesClient("azure", "azure", "", time.Second)
	owner, repo, id, err := client.parseAzureWebURL(webURL)
	if err != nil || owner != "owner" || repo != "repo" || id != "16" {
		t.Fatalf("invalid result")
	}
}

func TestAzurePipelinesClient_fetchBuild(t *testing.T) {
	client, teardown, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()

	ctx := context.Background()
	build, err := client.fetchBuild(ctx, "owner", "repo", "16")
	if err != nil {
		t.Fatal(err)
	}

	expectedBuild := expectedBuild
	expectedBuild.WebURL = strings.ReplaceAll(expectedBuild.WebURL, "HOST", client.baseURL.Host)
	if diff := cmp.Diff(expectedBuild, build); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestAzurePipelinesClient_BuildFromURL(t *testing.T) {
	client, teardown, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()

	webURL := "http://" + client.baseURL.Host + "/owner/repo/_build/results?buildId=16"
	ctx := context.Background()
	build, err := client.BuildFromURL(ctx, webURL)
	if err != nil {
		t.Fatal(err)
	}

	expectedBuild := expectedBuild
	expectedBuild.WebURL = strings.ReplaceAll(expectedBuild.WebURL, "HOST", client.baseURL.Host)
	if diff := cmp.Diff(expectedBuild, build); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestAzurePipelinesClient_Log(t *testing.T) {
	client, teardown, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()

	client.logURLByJobID["1234"] = url.URL{
		Scheme: client.baseURL.Scheme,
		Host:   client.baseURL.Host,
		Path:   "/owner/repo/_apis/build/builds/16/logs/1234",
	}

	ctx := context.Background()
	log, err := client.Log(ctx, cache.Repository{}, "1234")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(log, "log\n"); len(diff) > 0 {
		t.Fatal(diff)
	}
}
