package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/cistern/cache"
	"github.com/nbedos/cistern/utils"
)

func setupCircleCITestServer(t *testing.T) (*http.Client, *url.URL, func()) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := ""
		switch r.URL.Path {
		case "/project/gh/nbedos/cistern/36":
			filename = "circle_build.json"
		case "/cistern/log/36":
			filename = "circle_log"
		default:
			w.WriteHeader(404)
			return
		}

		bs, err := ioutil.ReadFile(path.Join("test_data", "circleci", filename))
		if err != nil {
			w.WriteHeader(500)
			fmt.Fprint(w, err.Error())
			return
		}
		if _, err := fmt.Fprint(w, string(bs)); err != nil {
			w.WriteHeader(500)
			fmt.Fprint(w, err.Error())
			return
		}
	}))

	testURL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	return ts.Client(), testURL, func() { ts.Close() }
}

func TestParseCircleCIWebURL(t *testing.T) {
	u := "https://circleci.com/gh/nbedos/cistern/36"
	baseURL := url.URL{
		Scheme: "https",
		Host:   "circleci.com",
	}

	owner, repo, id, err := parseCircleCIWebURL(&baseURL, u)
	if err != nil {
		t.Fatal(err)
	}

	if owner != "nbedos" || repo != "cistern" || id != 36 {
		t.Fail()
	}
}

func TestCircleCIClient_BuildFromURL(t *testing.T) {
	httpClient, testURL, teardown := setupCircleCITestServer(t)
	defer teardown()

	client := CircleCIClient{
		baseURL:     *testURL,
		httpClient:  httpClient,
		rateLimiter: time.Tick(time.Millisecond),
	}

	pipelineURL := testURL.String() + "/gh/nbedos/cistern/36"
	pipeline, err := client.BuildFromURL(context.Background(), pipelineURL)
	if err != nil {
		t.Fatal(err)
	}

	expectedPipeline := cache.Pipeline{
		Number: "",
		GitReference: cache.GitReference{
			SHA:   "210b32c023c9c9668d7e0098bec24e64cfd37bd3",
			Ref:   "master",
			IsTag: false,
		},
		Step: cache.Step{
			ID:    "36",
			Name:  "build",
			Type:  cache.StepPipeline,
			State: cache.Passed,
			CreatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 21, 14, 40, 27, 911000000, time.UTC),
			},
			StartedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 21, 14, 40, 32, 555000000, time.UTC),
			},
			FinishedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 21, 14, 41, 6, 461000000, time.UTC),
			},
			UpdatedAt: time.Date(2019, 11, 21, 14, 41, 6, 461000000, time.UTC),
			Duration: utils.NullDuration{
				Valid:    true,
				Duration: 33*time.Second + 906*time.Millisecond,
			},
			WebURL: utils.NullString{
				Valid:  true,
				String: "https://circleci.com/gh/nbedos/cistern/36",
			},
			Log: cache.Log{},
			Children: []cache.Step{
				{
					ID:    "0.0",
					Name:  "Spin up Environment",
					Type:  3,
					State: "passed",
					StartedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 11, 21, 14, 40, 32, 620000000, time.UTC),
					},
					FinishedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 11, 21, 14, 40, 37, 893000000, time.UTC),
					},
					Duration: utils.NullDuration{
						Valid:    true,
						Duration: 5*time.Second + 273*time.Millisecond,
					},
					WebURL: utils.NullString{
						Valid:  true,
						String: "https://circleci.com/gh/nbedos/cistern/36",
					},
					Log: cache.Log{
						Key: "example.com/logurl",
					},
				},
			},
		},
	}
	if diff := expectedPipeline.Diff(pipeline); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestCircleCIClient_Log(t *testing.T) {
	httpClient, testURL, teardown := setupCircleCITestServer(t)
	defer teardown()

	client := CircleCIClient{
		baseURL:     *testURL,
		httpClient:  httpClient,
		rateLimiter: time.Tick(time.Millisecond),
	}

	step := cache.Step{
		Log: cache.Log{
			Key:     testURL.String() + "/cistern/log/36",
			Content: utils.NullString{},
		},
	}

	log, err := client.Log(context.Background(), step)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(log, "log"); len(diff) > 0 {
		t.Fatal(diff)
	}
}
