package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-github/v28/github"
)

func setupGitHubTestServer() (*http.Client, string, func()) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := ""
		switch r.URL.Path {
		case "/repos/nbedos/termtosvg/commits/d58600a58bf1738c6529ce3489a546bfa2178e07/check-runs":
			filename = "github_check_runs.json"
		case "/repos/nbedos/termtosvg/commits/d58600a58bf1738c6529ce3489a546bfa2178e07/statuses":
			filename = "github_statuses.json"
		case "/repos/nbedos/termtosvg/commits/d58600a58bf1738c6529ce3489a546bfa2178e07":
			filename = "github_commit.json"
		case "/repos/nbedos/termtosvg/commits/d58600a58bf1738c6529ce3489a546bfa2178e07/branches-where-head":
			filename = "github_branches.json"
		case "/repos/nbedos/termtosvg/tags":
			filename = "github_tags.json"
		default:
			w.WriteHeader(404)
			return
		}

		bs, err := ioutil.ReadFile(path.Join("test_data", "github", filename))
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

	return ts.Client(), ts.URL, func() { ts.Close() }
}

func TestRefStatuses(t *testing.T) {
	httpClient, serverURL, teardown := setupGitHubTestServer()
	defer teardown()

	c, err := github.NewEnterpriseClient(serverURL, serverURL, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	client := GitHubClient{
		client: c,
	}

	sha := "d58600a58bf1738c6529ce3489a546bfa2178e07"
	urls, err := client.RefStatuses(context.Background(), serverURL+"/nbedos/termtosvg", "", sha)
	if err != nil {
		t.Fatal(err)
	}

	expectedURLs := []string{
		"https://circleci.com/gh/nbedos/cistern/36",
		"https://ci.appveyor.com/project/nbedos/cistern/builds/29024796",
		"https://travis-ci.com/owner/repository/builds/123654789",
		"https://travis-ci.org/nbedos/cistern/builds/615087280",
		"https://gitlab.com/nbedos/cistern/pipelines/97604657",
	}

	sort.Strings(urls)
	sort.Strings(expectedURLs)
	if diff := cmp.Diff(urls, expectedURLs); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestCommit(t *testing.T) {
	httpClient, serverURL, teardown := setupGitHubTestServer()
	defer teardown()

	c, err := github.NewEnterpriseClient(serverURL, serverURL, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	client := GitHubClient{
		client: c,
	}

	repoURL := serverURL + "/nbedos/termtosvg"
	commit, err := client.Commit(context.Background(), repoURL, "d58600a58bf1738c6529ce3489a546bfa2178e07")
	if err != nil {
		t.Fatal(err)
	}

	expectedCommit := Commit{
		Sha:      "d58600a58bf1738c6529ce3489a546bfa2178e07",
		Author:   "nbedos <nicolas.bedos@gmail.com>",
		Date:     time.Date(2019, 11, 16, 14, 59, 32, 0, time.UTC),
		Message:  "Bump version to 1.0.0",
		Branches: []string{"master"},
		Tags:     []string{"1.0.0"},
	}

	if diff := cmp.Diff(expectedCommit, commit); len(diff) > 0 {
		t.Fatal(diff)
	}
}
