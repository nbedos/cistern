package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
)

func TestTravisClientfetchPipeline(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/build/609256446" {
			bs, err := ioutil.ReadFile("test_data/travis_build_609256446.json")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprint(w, string(bs)); err != nil {
				t.Fatal(err)
			}
		}
	}))
	defer ts.Close()

	URL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	client := TravisClient{
		baseURL:     *URL,
		httpClient:  ts.Client(),
		rateLimiter: time.Tick(time.Millisecond),
		token:       "token",
		provider: cache.Provider{
			ID:   "id",
			Name: "name",
		},
		buildsPageSize: 10,
	}

	repository := cache.Repository{
		URL:   "github.com/nbedos/citop",
		Owner: "nbedos",
		Name:  "citop",
	}

	expectedPipeline := cache.Pipeline{
		Repository: &repository,
		GitReference: cache.GitReference{
			SHA:   "c824642cc7c3abf8abc2d522b58a345a98b95b9b",
			Ref:   "feature/travis_improvements",
			IsTag: false,
		},
		Step: cache.Step{
			ID:    "609256446",
			Type:  cache.StepPipeline,
			State: cache.Failed,
			CreatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 14, 26, 21, 506000000, time.UTC),
			},
			StartedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 53, 52, 0, time.UTC),
			},
			FinishedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 54, 18, 0, time.UTC),
			},
			UpdatedAt: time.Date(2019, 11, 8, 20, 54, 19, 108000000, time.UTC),
			Duration: utils.NullDuration{
				Valid:    true,
				Duration: 114 * time.Second,
			},
			WebURL: utils.NullString{
				String: fmt.Sprintf("%s/nbedos/citop/builds/609256446", ts.URL),
				Valid:  true,
			},
		},
	}

	expectedPipeline.Children = []cache.Step{
		{
			ID:    "11290169",
			Type:  cache.StepStage,
			Name:  "Tests",
			State: cache.Failed,
			CreatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 14, 26, 21, 506000000, time.UTC),
			},
			StartedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 53, 52, 0, time.UTC),
			},
			FinishedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 54, 18, 0, time.UTC),
			},
		},
	}
	expectedPipeline.Children[0].Children = []cache.Step{
		{
			ID:    "609256447",
			Type:  cache.StepJob,
			State: cache.Failed,
			Name:  "GoLang 1.13 on Ubuntu Bionic",
			CreatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 14, 26, 21, 506000000, time.UTC),
			},
			StartedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 53, 52, 0, time.UTC),
			},
			FinishedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 54, 18, 0, time.UTC),
			},
			Duration: utils.NullDuration{
				Valid:    true,
				Duration: 26 * time.Second,
			},
			Log: utils.NullString{},
			WebURL: utils.NullString{
				String: fmt.Sprintf("%s/nbedos/citop/jobs/609256447", ts.URL),
				Valid:  true,
			},
			AllowFailure: false,
		},
		{
			ID:    "609256448",
			Type:  cache.StepJob,
			State: cache.Failed,
			Name:  "GoLang 1.12 on Ubuntu Trusty",
			CreatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 14, 26, 21, 509000000, time.UTC),
			},
			StartedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 32, 48, 0, time.UTC),
			},
			FinishedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 33, 18, 0, time.UTC),
			},
			Duration: utils.NullDuration{
				Valid:    true,
				Duration: 30 * time.Second,
			},
			Log: utils.NullString{},
			WebURL: utils.NullString{
				String: fmt.Sprintf("%s/nbedos/citop/jobs/609256448", ts.URL),
				Valid:  true,
			},
			AllowFailure: false,
		},
		{
			ID:    "609256449",
			Type:  cache.StepJob,
			State: cache.Failed,
			Name:  "GoLang 1.13 on macOS 10.14",
			CreatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 14, 26, 21, 512000000, time.UTC),
			},
			StartedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 33, 44, 0, time.UTC),
			},
			FinishedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 34, 15, 0, time.UTC),
			},
			Duration: utils.NullDuration{
				Valid:    true,
				Duration: 31 * time.Second,
			},
			Log: utils.NullString{},
			WebURL: utils.NullString{
				String: fmt.Sprintf("%s/nbedos/citop/jobs/609256449", ts.URL),
				Valid:  true,
			},
			AllowFailure: false,
		},
		{
			ID:    "609256450",
			Type:  cache.StepJob,
			State: cache.Failed,
			Name:  "GoLang 1.12 on macOS 10.13",
			CreatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 14, 26, 21, 514000000, time.UTC),
			},
			StartedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 33, 39, 0, time.UTC),
			},
			FinishedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 20, 34, 06, 0, time.UTC),
			},
			Duration: utils.NullDuration{
				Valid:    true,
				Duration: 27 * time.Second,
			},
			Log: utils.NullString{},
			WebURL: utils.NullString{
				String: fmt.Sprintf("%s/nbedos/citop/jobs/609256450", ts.URL),
				Valid:  true,
			},
			AllowFailure: false,
		},
	}

	pipeline, err := client.fetchPipeline(context.Background(), &repository, "609256446")
	if err != nil {
		t.Fatal(err)
	}

	if diff := expectedPipeline.Diff(pipeline); diff != "" {
		t.Log(diff)
		t.Fatal("invalid pipeline")
	}
}

func TestTravisClientRepository(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/repo/nbedos/citop" {
			bs, err := ioutil.ReadFile("test_data/travis_repo_25564643.json")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprint(w, string(bs)); err != nil {
				t.Fatal(err)
			}
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	URL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	client := TravisClient{
		baseURL:     *URL,
		httpClient:  ts.Client(),
		rateLimiter: time.Tick(time.Millisecond),
		token:       "token",
		provider: cache.Provider{
			ID:   "id",
			Name: "name",
		},
		buildsPageSize: 10,
	}

	t.Run("Get nbedos/citop", func(t *testing.T) {
		repository, err := client.repository(context.Background(), "nbedos/citop")
		if err != nil {
			t.Fatal(err)
		}

		expected := cache.Repository{
			URL:   "https://github.com/nbedos/citop",
			Owner: "nbedos",
			Name:  "citop",
		}

		if diff := cmp.Diff(expected, repository); diff != "" {
			t.Log(diff)
			t.Fail()
		}
	})

	t.Run("ErrUnknownRepositoryURL", func(t *testing.T) {
		_, err := client.repository(context.Background(), "nbedos/does_not_exist")
		if err != cache.ErrUnknownRepositoryURL {
			t.Fatalf("expected %v but got %v", cache.ErrUnknownRepositoryURL, err)
		}
	})
}

func TestParseTravisWebURL(t *testing.T) {
	u := "https://travis-ci.org/nbedos/termtosvg/builds/612815758"

	owner, repo, id, err := parseTravisWebURL(&TravisOrgURL, u)
	if err != nil {
		t.Fatal(err)
	}

	if owner != "nbedos" || repo != "termtosvg" || id != "612815758" {
		t.Fail()
	}
}
