package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
)

func TestTravisClientfetchBuild(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/build/609256446" {
			bs, err := ioutil.ReadFile("test_data/build_609256446.json")
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
		baseURL:              *URL,
		httpClient:           ts.Client(),
		rateLimiter:          time.Tick(time.Millisecond),
		token:                "token",
		accountID:            "travis",
		buildsPageSize:       10,
		mux:                  &sync.Mutex{},
		updateTimePerBuildID: make(map[string]time.Time),
	}

	repository := cache.Repository{
		AccountID: "account",
		ID:        42,
		URL:       "github.com/nbedos/citop",
		Owner:     "nbedos",
		Name:      "citop",
	}

	build, err := client.fetchBuild(context.Background(), &repository, "609256446")
	if err != nil {
		t.Fatal(err)
	}

	expectedBuild := cache.Build{
		Repository: &repository,
		ID:         "609256446",
		Commit: cache.Commit{
			Sha:     "c824642cc7c3abf8abc2d522b58a345a98b95b9b",
			Message: "Order builds unambiguously",
			Date: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 8, 14, 26, 18, 0, time.UTC),
			},
		},
		Ref:             "feature/travis_improvements",
		IsTag:           false,
		RepoBuildNumber: "72",
		State:           cache.Failed,
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
		WebURL: fmt.Sprintf("%s/nbedos/citop/builds/609256446", ts.URL),
		Jobs:   map[int]*cache.Job{},
	}

	expectedBuild.Stages = map[int]*cache.Stage{
		11290169: {
			ID:    11290169,
			Name:  "Tests",
			State: cache.Failed,
			Jobs:  nil,
		},
	}
	expectedBuild.Stages[11290169].Jobs = map[int]*cache.Job{
		609256447: {
			ID:    609256447,
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
			Log:          utils.NullString{},
			WebURL:       fmt.Sprintf("%s/nbedos/citop/jobs/609256447", ts.URL),
			AllowFailure: false,
		},
		609256448: {
			ID:    609256448,
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
			Log:          utils.NullString{},
			WebURL:       fmt.Sprintf("%s/nbedos/citop/jobs/609256448", ts.URL),
			AllowFailure: false,
		},
		609256449: {
			ID:    609256449,
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
			Log:          utils.NullString{},
			WebURL:       fmt.Sprintf("%s/nbedos/citop/jobs/609256449", ts.URL),
			AllowFailure: false,
		},
		609256450: {
			ID:    609256450,
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
			Log:          utils.NullString{},
			WebURL:       fmt.Sprintf("%s/nbedos/citop/jobs/609256450", ts.URL),
			AllowFailure: false,
		},
	}

	if diff := cmp.Diff(expectedBuild, build); diff != "" {
		t.Log(diff)
		t.Fatal("invalid build")
	}
}

func TestTravisClientRepository(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/repo/nbedos/citop" {
			bs, err := ioutil.ReadFile("test_data/repo_25564643.json")
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
		baseURL:              *URL,
		httpClient:           ts.Client(),
		rateLimiter:          time.Tick(time.Millisecond),
		token:                "token",
		accountID:            "travis",
		buildsPageSize:       10,
		mux:                  &sync.Mutex{},
		updateTimePerBuildID: make(map[string]time.Time),
	}

	t.Run("Get nbedos/citop", func(t *testing.T) {
		repository, err := client.repository(context.Background(), "nbedos/citop")
		if err != nil {
			t.Fatal(err)
		}

		expected := cache.Repository{
			AccountID: "travis",
			ID:        25564643,
			URL:       "https://github.com/nbedos/citop",
			Owner:     "nbedos",
			Name:      "citop",
		}

		if diff := cmp.Diff(expected, repository); diff != "" {
			t.Log(diff)
			t.Fail()
		}
	})

	t.Run("ErrRepositoryNotFound", func(t *testing.T) {
		_, err := client.repository(context.Background(), "nbedos/does_not_exist")
		if err != cache.ErrRepositoryNotFound {
			t.Fatalf("expected %v but got %v", cache.ErrRepositoryNotFound, err)
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
