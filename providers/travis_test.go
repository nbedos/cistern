package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
)

func TestTravisClientfetchBuild(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/build/609256446" {
			bs, err := ioutil.ReadFile("data/build_609256446.json")
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
		baseURL:        *URL,
		httpClient:     ts.Client(),
		pusherHost:     "",
		rateLimiter:    time.Tick(time.Millisecond),
		token:          "token",
		accountID:      "travis",
		buildsPageSize: 10,
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
			bs, err := ioutil.ReadFile("data/repo_25564643.json")
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
		baseURL:        *URL,
		httpClient:     ts.Client(),
		pusherHost:     "",
		rateLimiter:    time.Tick(time.Millisecond),
		token:          "token",
		accountID:      "travis",
		buildsPageSize: 10,
	}

	t.Run("Get nbedos/citop", func(t *testing.T) {
		repository, err := client.repository(context.Background(), "github.com/nbedos/citop")
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
		_, err := client.repository(context.Background(), "github.com/nbedos/does_not_exist")
		if err != cache.ErrRepositoryNotFound {
			t.Fatalf("expected %v but got %v", cache.ErrRepositoryNotFound, err)
		}
	})
}

func TestTravisClientFetchBuilds(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method == "GET" && r.URL.Path == "/repo/nbedos/citop/builds" {
			filenameFmt := "data/repo_25564643_builds?offset=%s&limit=%s.json"
			filename := fmt.Sprintf(filenameFmt, q.Get("offset"), q.Get("limit"))
			bs, err := ioutil.ReadFile(filename)
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
		baseURL:        *URL,
		httpClient:     ts.Client(),
		pusherHost:     "",
		rateLimiter:    time.Tick(time.Millisecond),
		token:          "token",
		accountID:      "travis",
		buildsPageSize: 10,
	}

	repository := cache.Repository{
		AccountID: "account",
		ID:        42,
		URL:       "github.com/nbedos/citop",
		Owner:     "nbedos",
		Name:      "citop",
	}

	builds, err := client.fetchBuilds(context.Background(), &repository, 10, 10)
	if err != nil {
		t.Fatal(err)
	}

	ids := make(map[int]struct{}, len(builds))
	for _, build := range builds {
		ids[build.ID] = struct{}{}
	}

	expectedIDs := map[int]struct{}{
		607359944: {},
		607356631: {},
		607060646: {},
		607059776: {},
		606103420: {},
		605628645: {},
		599340942: {},
		599339545: {},
		598815592: {},
		598749982: {},
	}

	if diff := cmp.Diff(expectedIDs, ids); diff != "" {
		t.Log(diff)
		t.Fatal("invalid id")
	}
}

func TestTravisClientRepositoryBuilds(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var filename string
		switch {
		case r.Method == "GET" && r.URL.Path == "/repo/nbedos/citop/builds":
			filenameFmt := "data/repo_25564643_builds?offset=%s&limit=%s.json"
			q := r.URL.Query()
			filename = fmt.Sprintf(filenameFmt, q.Get("offset"), q.Get("limit"))

		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/build/"):
			buildID := strings.TrimPrefix(r.URL.Path, "/build/")
			filename = fmt.Sprintf("data/build_%s.json", buildID)
		default:
			w.WriteHeader(404)
			return
		}

		bs, err := ioutil.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(w, string(bs)); err != nil {
			t.Fatal(err)
		}

	}))
	defer ts.Close()

	URL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	client := TravisClient{
		baseURL:        *URL,
		httpClient:     ts.Client(),
		pusherHost:     "",
		rateLimiter:    time.Tick(time.Millisecond),
		token:          "token",
		accountID:      "travis",
		buildsPageSize: 10,
	}

	repository := cache.Repository{
		AccountID: "account",
		ID:        42,
		URL:       "github.com/nbedos/citop",
		Owner:     "nbedos",
		Name:      "citop",
	}

	now := time.Now()
	maxAge := now.Sub(time.Date(2019, 10, 14, 0, 0, 0, 0, time.UTC))
	buildc := make(chan cache.Build)
	var errRepositoryBuild error = nil
	go func() {
		errRepositoryBuild = client.repositoryBuilds(context.Background(), &repository, maxAge, buildc)
		close(buildc)
	}()

	for build := range buildc {
		if build.StartedAt.Valid {
			if age := now.Sub(build.StartedAt.Time); age > maxAge {
				t.Fatalf("build is older than %v: %v", maxAge, age)
			}
		}
	}

	if errRepositoryBuild != nil {
		t.Fatal(errRepositoryBuild)
	}
}
