package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-github/v28/github"
)

func TestClient(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := ""
		switch r.URL.Path {
		case "/repos/nbedos/termtosvg/commits/d58600a58bf1738c6529ce3489a546bfa2178e07/check-runs":
			filename = "github_check_runs.json"
		case "/repos/nbedos/termtosvg/commits/d58600a58bf1738c6529ce3489a546bfa2178e07/statuses":
			filename = "github_statuses.json"
		default:
			w.WriteHeader(404)
			return
		}

		bs, err := ioutil.ReadFile(fmt.Sprintf("test_data/%s", filename))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(w, string(bs)); err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	tsu, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	c, err := github.NewEnterpriseClient(tsu.String(), "example.com", ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	client := GitHubClient{
		client: c,
	}
	owner := "nbedos"
	repo := "termtosvg"
	sha := "d58600a58bf1738c6529ce3489a546bfa2178e07"
	urls, err := client.BuildURLs(context.Background(), owner, repo, sha)
	if err != nil {
		t.Fatal(err)
	}

	expectedURLs := []string{
		"https://circleci.com/gh/nbedos/citop/36",
		"https://ci.appveyor.com/project/nbedos/citop/builds/29024796",
		"https://travis-ci.com/owner/repository/builds/123654789",
		"https://travis-ci.org/nbedos/citop/builds/615087280",
		"https://gitlab.com/nbedos/citop/pipelines/97604657",
	}

	sort.Strings(urls)
	sort.Strings(expectedURLs)
	if diff := cmp.Diff(urls, expectedURLs); len(diff) > 0 {
		t.Fatal(diff)
	}
}
