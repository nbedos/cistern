package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/cistern/cache"
	"github.com/nbedos/cistern/utils"
)

func Setup() (AzurePipelinesClient, func(), error) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := ""
		switch {
		case r.Method == "GET" && r.URL.Path == "/owner/repo/_apis/build/builds":
			filename = "azure_build_16.json"
		case r.Method == "GET" && r.URL.Path == "/owner/repo/_apis/build/builds/16/Timeline":
			filename = "azure_build_16_timeline.json"
		case r.Method == "GET" && r.URL.Path == "/owner/repo/_apis/build/builds/16/logs/1234":
			filename = "azure_build_16_job_log.txt"
		default:
			w.WriteHeader(404)
			return
		}

		bs, err := ioutil.ReadFile(path.Join("test_data", "azure", filename))
		if err != nil {
			w.WriteHeader(500)
			fmt.Fprint(w, err.Error())
			return
		}

		// Rewrite URLs in the file to match the scheme and host of the query
		s := strings.Replace(string(bs), "https://example.com", "http://"+r.Host, -1)
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
		version: "5.1",
		mux:     &sync.Mutex{},
	}

	teardown := func() {
		testServer.Close()
	}
	return client, teardown, nil
}

var expectedPipeline = cache.Pipeline{
	Number: "20191204.3",
	GitReference: cache.GitReference{
		SHA:   "5e4d496d63086609cb3c03aa0ee4e032e4b6b08b",
		Ref:   "azure-pipelines",
		IsTag: false,
	},
	Step: cache.Step{
		ID:    "16",
		Name:  "owner.repo (1)",
		Type:  cache.StepPipeline,
		State: cache.Failed,
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
		WebURL: utils.NullString{
			String: "http://HOST/owner/repo/_build/results?buildId=16",
			Valid:  true,
		},
		Children: []cache.Step{
			{
				ID:    "8bfbeaae-4c8e-5f12-f154-edd305817000",
				Type:  cache.StepStage,
				Name:  "tests",
				State: "failed",
				StartedAt: utils.NullTime{
					Valid: true,
					Time:  time.Date(2019, 12, 4, 13, 9, 56, 653333300, time.UTC),
				},
				FinishedAt: utils.NullTime{
					Valid: true,
					Time:  time.Date(2019, 12, 4, 13, 11, 34, 60000000, time.UTC),
				},
				Duration: utils.NullDuration{
					Valid:    true,
					Duration: time.Minute + 37*time.Second + 406666700*time.Nanosecond,
				},
				WebURL: utils.NullString{
					String: "http://HOST/owner/repo/_build/results?buildId=16",
					Valid:  true,
				},
				Children: []cache.Step{
					{
						ID:    "a1fe9f00-6aac-5c3d-c3c6-290a6d3ec2ef",
						Type:  cache.StepJob,
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
							Time:  time.Date(2019, 12, 4, 13, 11, 33, 790000000, time.UTC),
						},
						Duration: utils.NullDuration{
							Valid:    true,
							Duration: time.Minute + 33*time.Second + 76666700*time.Nanosecond,
						},
						WebURL: utils.NullString{
							String: "http://HOST/owner/repo/_build/results?buildId=16",
							Valid:  true,
						},
					},
					{
						ID:    "e305bc7f-849a-5981-f9f4-d079b0b7f451",
						Type:  cache.StepJob,
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
							Time:  time.Date(2019, 12, 4, 13, 11, 24, 590000000, time.UTC),
						},
						Duration: utils.NullDuration{
							Valid:    true,
							Duration: time.Minute + 27*time.Second + 936666700*time.Nanosecond,
						},
						WebURL: utils.NullString{
							String: "http://HOST/owner/repo/_build/results?buildId=16",
							Valid:  true,
						},
					},
					{
						ID:    "aa83c9de-d200-5148-7d44-5e08a0dd6659",
						Type:  cache.StepJob,
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
						WebURL: utils.NullString{
							String: "http://HOST/owner/repo/_build/results?buildId=16",
							Valid:  true,
						},
						Children: []cache.Step{
							{
								ID:    "fd63e659-60cf-51c7-a63d-0111af4550dd",
								Name:  "Set up the Go workspace",
								Type:  cache.StepTask,
								State: "passed",
								StartedAt: utils.NullTime{
									Valid: true,
									Time:  time.Date(2019, 12, 4, 13, 10, 3, 153333300, time.UTC),
								},
								FinishedAt: utils.NullTime{
									Valid: true,
									Time:  time.Date(2019, 12, 4, 13, 10, 3, 870000000, time.UTC),
								},
								Duration: utils.NullDuration{
									Valid:    true,
									Duration: 716*time.Millisecond + 666*time.Microsecond + 700*time.Nanosecond,
								},
								WebURL: utils.NullString{
									String: "http://HOST/owner/repo/_build/results?buildId=16",
									Valid:  true,
								},
							},
							{
								ID:    "bebceb1b-138c-57de-594c-688f96e7a793",
								Name:  "Build",
								Type:  cache.StepTask,
								State: "failed",
								StartedAt: utils.NullTime{
									Valid: true,
									Time:  time.Date(2019, 12, 4, 13, 10, 3, 870000000, time.UTC),
								},
								FinishedAt: utils.NullTime{
									Valid: true,
									Time:  time.Date(2019, 12, 4, 13, 10, 4, 230000000, time.UTC),
								},
								Duration: utils.NullDuration{
									Valid:    true,
									Duration: 360 * time.Millisecond,
								},
								WebURL: utils.NullString{
									String: "http://HOST/owner/repo/_build/results?buildId=16",
									Valid:  true,
								},
							},
						},
					},
				},
			},
		},
	},
}

func setWebURLs(p cache.Pipeline, webURL utils.NullString) cache.Pipeline {
	p.Step = p.Step.Map(func(s cache.Step) cache.Step {
		s.WebURL = webURL
		return s
	})

	return p
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
	pipeline, err := client.fetchPipeline(ctx, "owner", "repo", "16")
	if err != nil {
		t.Fatal(err)
	}

	expectedPipeline := setWebURLs(expectedPipeline, utils.NullString{
		String: "http://" + client.baseURL.Host + "/owner/repo/_build/results?buildId=16",
		Valid:  true,
	})
	if diff := expectedPipeline.Diff(pipeline); len(diff) > 0 {
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
	pipeline, err := client.BuildFromURL(ctx, webURL)
	if err != nil {
		t.Fatal(err)
	}

	expectedPipeline := setWebURLs(expectedPipeline, utils.NullString{
		String: webURL,
		Valid:  true,
	})
	if diff := expectedPipeline.Diff(pipeline); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestAzurePipelinesClient_Log(t *testing.T) {
	client, teardown, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()

	logURL := url.URL{
		Scheme: client.baseURL.Scheme,
		Host:   client.baseURL.Host,
		Path:   "/owner/repo/_apis/build/builds/16/logs/1234",
	}
	ctx := context.Background()
	job := cache.Step{
		ID:   "1234",
		Type: cache.StepJob,
		Log: cache.Log{
			Key: logURL.String(),
		},
	}
	log, err := client.Log(ctx, job)
	if err != nil {
		t.Fatal(err)
	}

	log = strings.Trim(log, "\r\n")
	if diff := cmp.Diff(log, "log"); len(diff) > 0 {
		t.Fatal(diff)
	}
}
