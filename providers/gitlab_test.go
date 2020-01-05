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
	"github.com/nbedos/cistern/cache"
	"github.com/nbedos/cistern/utils"
	"github.com/xanzy/go-gitlab"
)

func TestParsePipelineURL(t *testing.T) {
	c, err := NewGitLabClient("gitlab", "gitlab", "", "", 1000)
	if err != nil {
		t.Fatal(err)
	}

	slug, id, err := c.parsePipelineURL("https://gitlab.com/nbedos/cistern/pipelines/97604657")
	if err != nil {
		t.Fatal(err)
	}

	if slug != "nbedos/cistern" || id != 97604657 {
		t.Fail()
	}
}

func setupGitLabTestServer(t *testing.T) (GitLabClient, string, func()) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := ""
		switch r.URL.Path {
		case "/api/v4/projects/nbedos/cistern/pipelines/103230300":
			filename = "gitlab_pipeline.json"
		case "/api/v4/projects/nbedos/cistern/pipelines/103230300/jobs":
			w.Header().Add("X-Total-Pages", "1")
			filename = "gitlab_jobs.json"
		case "/api/v4/projects/nbedos/cistern/jobs/42/trace":
			filename = "gitlab_log"
		case "/api/v4/projects/owner/repo/repository/commits/master":
			filename = "gitlab_commit.json"
		case "/api/v4/projects/owner/repo/repository/commits/a24840cf94b395af69da4a1001d32e3694637e20/refs":
			filename = "gitlab_refs.json"
		case "/api/v4/projects/nbedos/cistern/pipelines":
			filename = "gitlab_pipelines.json"
		case "/api/v4/projects/nbedos/cistern/repository/commits/a24840cf94b395af69da4a1001d32e3694637e20/statuses":
		default:
			w.WriteHeader(404)
			return
		}

		bs, err := ioutil.ReadFile(path.Join("test_data", "gitlab", filename))
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

	gitlabClient := gitlab.NewClient(ts.Client(), "token")
	if err := gitlabClient.SetBaseURL(ts.URL); err != nil {
		t.Fatal(err)
	}

	client := GitLabClient{
		remote:      gitlabClient,
		rateLimiter: time.Tick(time.Millisecond),
	}

	return client, ts.URL, func() { ts.Close() }
}

func TestGitLabClient_BuildFromURL(t *testing.T) {
	client, testURL, teardown := setupGitLabTestServer(t)
	defer teardown()

	pipelineURL := testURL + "/nbedos/cistern/pipelines/103230300"
	pipeline, err := client.BuildFromURL(context.Background(), pipelineURL)
	if err != nil {
		t.Fatal(err)
	}
	expectedPipeline := cache.Pipeline{
		GitReference: cache.GitReference{
			SHA: "6645b9ba15963e480be7763d68d9c275760d555e",
			Ref: "master",
		},
		Step: cache.Step{
			ID:           "103230300",
			Name:         "",
			Type:         cache.StepPipeline,
			State:        cache.Passed,
			AllowFailure: false,
			CreatedAt: time.Date(2019, 12, 15, 21, 46, 40, 694000000, time.UTC),
			StartedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 12, 15, 21, 46, 41, 214000000, time.UTC),
			},
			FinishedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 12, 15, 21, 48, 13, 72000000, time.UTC),
			},
			UpdatedAt: time.Date(2019, 12, 15, 21, 48, 13, 77000000, time.UTC),
			Duration: utils.NullDuration{
				Valid:    true,
				Duration: time.Minute + 31*time.Second,
			},
			WebURL: utils.NullString{
				Valid:  true,
				String: "https://gitlab.com/nbedos/cistern/pipelines/103230300",
			},
			Children: []cache.Step{
				{
					ID:    "1",
					Name:  "test",
					Type:  1,
					State: "passed",
					CreatedAt: time.Date(2019, 12, 15, 21, 46, 40, 706000000, time.UTC),
					StartedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 15, 21, 46, 41, 151000000, time.UTC),
					},
					FinishedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 15, 21, 48, 13, 5000000, time.UTC),
					},
					Duration: utils.NullDuration{
						Valid:    true,
						Duration: time.Minute + 31*time.Second,
					},
					WebURL: utils.NullString{
						Valid:  true,
						String: "https://gitlab.com/nbedos/cistern/pipelines/103230300",
					},
					Children: []cache.Step{
						{
							ID:    "379869167",
							Name:  "golang 1.13",
							Type:  2,
							State: "passed",
							CreatedAt: time.Date(2019, 12, 15, 21, 46, 40, 706000000, time.UTC),
							StartedAt: utils.NullTime{
								Valid: true,
								Time:  time.Date(2019, 12, 15, 21, 46, 41, 151000000, time.UTC),
							},
							FinishedAt: utils.NullTime{
								Valid: true,
								Time:  time.Date(2019, 12, 15, 21, 48, 13, 5000000, time.UTC),
							},
							Duration: utils.NullDuration{
								Valid:    true,
								Duration: time.Minute + 31*time.Second,
							},
							WebURL: utils.NullString{Valid: true, String: "https://gitlab.com/nbedos/cistern/-/jobs/379869167"},
							Log:    cache.Log{Key: "nbedos/cistern"},
						},
					},
				},
			},
		},
	}
	if diff := expectedPipeline.Diff(pipeline); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestGitLabClient_Log(t *testing.T) {
	client, _, teardown := setupGitLabTestServer(t)
	defer teardown()

	step := cache.Step{
		ID: "42",
		Log: cache.Log{
			Key: "nbedos/cistern",
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

func TestGitLabClient_Commit(t *testing.T) {
	t.Run("existing reference", func(t *testing.T) {
		client, testURL, teardown := setupGitLabTestServer(t)
		defer teardown()

		commit, err := client.Commit(context.Background(), testURL+"/owner/repo", "master")
		if err != nil {
			t.Fatal(err)
		}

		expectedCommit := cache.Commit{
			Sha:      "a24840cf94b395af69da4a1001d32e3694637e20",
			Author:   "nbedos <nicolas.bedos@gmail.com>",
			Date:     time.Date(2019, 12, 16, 18, 6, 43, 0, time.UTC),
			Message:  "Fix typos\n",
			Branches: []string{"master"},
			Tags:     nil,
			Head:     "",
			Statuses: nil,
		}

		if diff := cmp.Diff(expectedCommit, commit); len(diff) > 0 {
			t.Fatal(diff)
		}
	})

	t.Run("non existing commit", func(t *testing.T) {
		client, testURL, teardown := setupGitLabTestServer(t)
		defer teardown()

		_, err := client.Commit(context.Background(), testURL+"/owner/repo", "0000000")
		if err != cache.ErrUnknownGitReference {
			t.Fatal(err)
		}
	})
}

func TestGitLabClient_RefStatuses(t *testing.T) {
	client, testURL, teardown := setupGitLabTestServer(t)
	defer teardown()

	statuses, err := client.RefStatuses(context.Background(), testURL+"/nbedos/cistern", "", "a24840cf94b395af69da4a1001d32e3694637e20")
	if err != nil {
		t.Fatal(err)
	}

	expectedStatuses := []string{
		"https://gitlab.com/nbedos/cistern/pipelines/103494597",
	}
	sort.Strings(expectedStatuses)
	sort.Strings(statuses)
	if diff := cmp.Diff(expectedStatuses, statuses); len(diff) > 0 {
		t.Fatal(diff)
	}
}
