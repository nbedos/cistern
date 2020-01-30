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
	"github.com/nbedos/cistern/utils"
	"github.com/xanzy/go-gitlab"
)

func TestParsePipelineURL(t *testing.T) {
	testCases := []struct {
		name         string
		url          string
		expectedSlug string
		expectedID   int
	}{
		{
			name:         "repository path without namespace",
			url:          "https://gitlab.com/nbedos/cistern/pipelines/97604657",
			expectedSlug: "nbedos/cistern",
			expectedID:   97604657,
		},
		{
			name:         "repository path with namespace (issue #16)",
			url:          "https://gitlab.com/namespace/nbedos/cistern/pipelines/97604657",
			expectedSlug: "namespace/nbedos/cistern",
			expectedID:   97604657,
		},
		{
			name:         "repository path with long namespace (issue #16)",
			url:          "https://gitlab.com/long/namespace/nbedos/cistern/pipelines/97604657",
			expectedSlug: "long/namespace/nbedos/cistern",
			expectedID:   97604657,
		},
	}

	for _, testCase := range testCases {
		c, err := NewGitLabClient("gitlab", "gitlab", "", "", 1000, "")
		if err != nil {
			t.Fatal(err)
		}

		slug, id, err := c.parsePipelineURL(testCase.url)
		if err != nil {
			t.Fatal(err)
		}

		if slug != testCase.expectedSlug {
			t.Fatalf("expected slug %q but got %q", testCase.expectedSlug, slug)
		}

		if id != testCase.expectedID {
			t.Fatalf("expected id %d but got %d", testCase.expectedID, id)
		}
	}
}

func setupGitLabTestServer() (GitLabClient, string, func(), error) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := ""

		switch r.URL.Path {
		case "/api/v4/projects/long/namespace/nbedos/cistern/pipelines/103230300":
			filename = "gitlab_pipeline.json"
		case "/api/v4/projects/long/namespace/nbedos/cistern/pipelines/103230300/jobs":
			w.Header().Add("X-Total-Pages", "1")
			filename = "gitlab_jobs.json"
		case "/api/v4/projects/long/namespace/nbedos/cistern/jobs/42/trace":
			filename = "gitlab_log"
		case "/api/v4/projects/long/namespace/owner/repo/repository/commits/master":
			filename = "gitlab_commit.json"
		case "/api/v4/projects/long/namespace/owner/repo/repository/commits/a24840cf94b395af69da4a1001d32e3694637e20/refs":
			filename = "gitlab_refs.json"
		case "/api/v4/projects/long/namespace/nbedos/cistern/pipelines":
			filename = "gitlab_pipelines.json"
		case "/api/v4/projects/long/namespace/nbedos/cistern/repository/commits/a24840cf94b395af69da4a1001d32e3694637e20/statuses":
			filename = "gitlab_statuses.json"
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
		ts.Close()
		return GitLabClient{}, "", nil, err
	}

	client := GitLabClient{
		remote:      gitlabClient,
		rateLimiter: time.Tick(time.Millisecond),
		sshHostname: "ssh.gitlab.com",
	}

	return client, ts.URL, func() { ts.Close() }, nil
}

func TestGitLabClient_BuildFromURL(t *testing.T) {
	client, testURL, teardown, err := setupGitLabTestServer()
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()

	pipelineURL := testURL + "/long/namespace/nbedos/cistern/pipelines/103230300"
	pipeline, err := client.BuildFromURL(context.Background(), pipelineURL)
	if err != nil {
		t.Fatal(err)
	}
	expectedPipeline := Pipeline{
		GitReference: GitReference{
			SHA: "6645b9ba15963e480be7763d68d9c275760d555e",
			Ref: "master",
		},
		Step: Step{
			ID:           "103230300",
			Name:         "",
			Type:         StepPipeline,
			State:        Passed,
			AllowFailure: false,
			CreatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 12, 15, 21, 46, 40, 694000000, time.UTC),
			},
			StartedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 12, 15, 21, 46, 41, 214000000, time.UTC),
			},
			FinishedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 12, 15, 21, 48, 13, 72000000, time.UTC),
			},
			UpdatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 12, 15, 21, 48, 13, 77000000, time.UTC),
			},
			Duration: utils.NullDuration{
				Valid:    true,
				Duration: time.Minute + 31*time.Second,
			},
			WebURL: utils.NullString{
				Valid:  true,
				String: "https://gitlab.com/long/namespace/nbedos/cistern/pipelines/103230300",
			},
			Children: []Step{
				{
					ID:    "1",
					Name:  "test",
					Type:  1,
					State: "passed",
					CreatedAt: utils.NullTime{
						Valid: true,
						Time:  time.Date(2019, 12, 15, 21, 46, 40, 706000000, time.UTC),
					},
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
						String: "https://gitlab.com/long/namespace/nbedos/cistern/pipelines/103230300",
					},
					Children: []Step{
						{
							ID:    "379869167",
							Name:  "golang 1.13",
							Type:  2,
							State: "passed",
							CreatedAt: utils.NullTime{
								Valid: true,
								Time:  time.Date(2019, 12, 15, 21, 46, 40, 706000000, time.UTC),
							},
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
							WebURL: utils.NullString{Valid: true, String: "https://gitlab.com/long/namespace/nbedos/cistern/-/jobs/379869167"},
							Log:    Log{Key: "long/namespace/nbedos/cistern"},
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
	client, _, teardown, err := setupGitLabTestServer()
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()

	step := Step{
		ID: "42",
		Log: Log{
			Key: "long/namespace/nbedos/cistern",
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
		client, testURL, teardown, err := setupGitLabTestServer()
		if err != nil {
			t.Fatal(err)
		}
		defer teardown()

		expectedCommit := Commit{
			Sha:      "a24840cf94b395af69da4a1001d32e3694637e20",
			Author:   "nbedos <nicolas.bedos@gmail.com>",
			Date:     time.Date(2019, 12, 16, 18, 6, 43, 0, time.UTC),
			Message:  "Fix typos\n",
			Branches: []string{"master"},
			Tags:     nil,
			Head:     "",
			Statuses: nil,
		}

		for _, repoURL := range []string{testURL, client.sshHostname} {
			t.Run(repoURL, func(t *testing.T) {
				commit, err := client.Commit(context.Background(), repoURL+"/long/namespace/owner/repo", "master")
				if err != nil {
					t.Fatal(err)
				}

				if diff := cmp.Diff(expectedCommit, commit); len(diff) > 0 {
					t.Fatal(diff)
				}
			})
		}
	})

	t.Run("non existing commit", func(t *testing.T) {
		client, testURL, teardown, err := setupGitLabTestServer()
		if err != nil {
			t.Fatal(err)
		}
		defer teardown()

		_, err = client.Commit(context.Background(), testURL+"/long/namespace/owner/repo", "0000000")
		if err != ErrUnknownGitReference {
			t.Fatal(err)
		}
	})
}

func TestGitLabClient_RefStatuses(t *testing.T) {
	client, testURL, teardown, err := setupGitLabTestServer()
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()

	statuses, err := client.RefStatuses(context.Background(), testURL+"/long/namespace/nbedos/cistern", "", "a24840cf94b395af69da4a1001d32e3694637e20")
	if err != nil {
		t.Fatal(err)
	}

	expectedStatuses := []string{
		"https://gitlab.com/long/namespace/nbedos/cistern/pipelines/103494597",
	}
	sort.Strings(expectedStatuses)
	sort.Strings(statuses)
	if diff := cmp.Diff(expectedStatuses, statuses); len(diff) > 0 {
		t.Fatal(diff)
	}
}
