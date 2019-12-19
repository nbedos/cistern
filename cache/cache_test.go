package cache

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/config"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

func TestAggregateStatuses(t *testing.T) {
	testCases := []struct {
		name   string
		steps  []Step
		result State
	}{
		{
			name:   "Empty list",
			steps:  []Step{},
			result: Unknown,
		},
		{
			name: "Jobs: No allowed failure",
			steps: []Step{
				{
					AllowFailure: false,
					State:        Passed,
				},
				{
					AllowFailure: false,
					State:        Failed,
				},
				{
					AllowFailure: false,
					State:        Passed,
				},
			},
			result: Failed,
		},
		{
			name: "Jobs: Allowed failure",
			steps: []Step{
				{
					AllowFailure: false,
					State:        Passed,
				},
				{
					AllowFailure: true,
					State:        Failed,
				},
				{
					AllowFailure: false,
					State:        Passed,
				},
			},
			result: Passed,
		},
		{
			name: "Builds",
			steps: []Step{
				{
					State: Passed,
				},
				{
					State: Failed,
				},
				{
					State: Passed,
				},
			},
			result: Failed,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if agg := Aggregate(testCase.steps); agg.State != testCase.result {
				t.Fatalf("expected %q but got %q", testCase.result, agg.State)
			}
		})
	}
}

func TestCache_Save(t *testing.T) {
	t.Run("Saved build must be returned by Pipeline()", func(t *testing.T) {
		c := NewCache(nil, nil)
		p := Pipeline{
			Step: Step{
				ID:    "42",
				State: Failed,
			},
		}
		if err := c.SavePipeline("", p); err != nil {
			t.Fatal(err)
		}
		savedPipeline, exists := c.Pipeline(p.Key())
		if !exists {
			t.Fatal("pipeline was not saved")
		}

		if diff := savedPipeline.Diff(p); len(diff) > 0 {
			t.Fatal(diff)
		}
	})

	oldPipeline := Pipeline{
		Step: Step{
			ID:        "42",
			State:     Failed,
			UpdatedAt: time.Date(2019, 11, 24, 14, 52, 0, 0, time.UTC),
		},
	}
	newPipeline := Pipeline{
		Step: Step{
			ID:        "42",
			State:     Passed,
			UpdatedAt: oldPipeline.UpdatedAt.Add(time.Second),
		},
	}

	t.Run("existing build must be overwritten if it's older than the current build", func(t *testing.T) {
		c := NewCache(nil, nil)

		if err := c.SavePipeline("", oldPipeline); err != nil {
			t.Fatal(err)
		}
		if err := c.SavePipeline("", newPipeline); err != nil {
			t.Fatal(err)
		}
		savedPipeline, exists := c.Pipeline(oldPipeline.Key())
		if !exists {
			t.Fatal("build was not saved")
		}

		if diff := savedPipeline.Diff(newPipeline); len(diff) > 0 {
			t.Fatal(diff)
		}
	})

	t.Run("cache.SavePipeline must return ErrObsoleteBuild if the build to save is older than the one in cache", func(t *testing.T) {
		c := NewCache(nil, nil)

		if err := c.SavePipeline("", newPipeline); err != nil {
			t.Fatal(err)
		}
		if err := c.SavePipeline("", oldPipeline); err != ErrObsoleteBuild {
			t.Fatalf("expected %v but got %v", ErrObsoleteBuild, err)
		}
	})
}

func TestCache_Builds(t *testing.T) {
	ids := []string{"1", "2", "3", "4"}
	c := NewCache(nil, nil)
	for _, id := range ids {
		p := Pipeline{
			providerHost: "host",
			Step: Step{
				ID: id,
			},
		}
		if err := c.SavePipeline("", p); err != nil {
			t.Fatal(err)
		}
	}

	for _, id := range ids {
		key := PipelineKey{
			ProviderHost: "host",
			ID:           id,
		}
		_, exists := c.Pipeline(key)
		if !exists {
			t.Fatalf("build not found: %+v", key)
		}
	}
}

/*
type mockProvider struct {
	id     string
	builds []Build
}

func (p mockProvider) ID() string { return p.id }
func (p mockProvider) Log(ctx context.Context, repository Repository, jobID string) (string, error) {
	return "log\n", nil
}
func (p mockProvider) BuildFromURL(ctx context.Context, u string) (Pipeline, error) {
	return Pipeline{}, nil
}

func TestCache_WriteLog(t *testing.T) {
	t.Run("log not saved in cache must be retrieved from provider", func(t *testing.T) {
		c := NewCache([]CIProvider{
			mockProvider{
				id: "provider1",
			},
			mockProvider{
				id: "provider2",
			},
		}, nil)

		pipelines := []Pipeline{
			{
				Repository: &Repository{
					Provider: Provider{
						ID: "provider1",
					},
				},
				Step: Step{
					ID: "1",
					Children: []Step{
						{
							ID:    "1",
							State: Passed,
							Log: utils.NullString{
								Valid: false,
							},
						},
					},
				},
			},
			{
				Repository: &Repository{
					Provider: Provider{
						ID: "provider1",
					},
				},
				Step: Step{
					ID: "2",
				},
			},
			{
				Repository: &Repository{
					Provider: Provider{
						ID: "provider1",
					},
				},
				Step: Step{
					ID: "3",
				},
			},
			{
				Repository: &Repository{
					Provider: Provider{
						ID: "provider1",
					},
				},
				Step: Step{
					ID: "4",
				},
			},
		}

		for _, p := range pipelines {
			if err := c.SavePipeline("", p); err != nil {
				t.Fatal(err)
			}
		}

		buf := bytes.Buffer{}
		key := PipelineKey{
			providerHost: "provider1",
			ID:         "1",
		}
		if err := c.ActiveRowLog(context.Background(), key, []string{"0", "1"}, &buf); err != nil {
			t.Fatal(err)
		}

		// Value return by provider.Log()
		expected := "log\n"
		if buf.String() != expected {
			t.Fatalf("expected %q but got %q", expected, buf.String())
		}

	})

	t.Run("log saved in cache must be returned as is", func(t *testing.T) {
		c := NewCache([]CIProvider{mockProvider{id: "provider1"}}, nil)
		pipelines := []Pipeline{
			{
				Repository: &Repository{
					Provider: Provider{
						ID: "provider1",
					},
				},
				Step: Step{
					ID: "1",
					Children: []Step{
						{
							ID:    "1",
							State: Passed,
							Log: utils.NullString{
								Valid:  true,
								String: "log1\n",
							},
						},
					},
				},
			},
			{
				Repository: &Repository{
					Provider: Provider{
						ID: "provider1",
					},
				},
				Step: Step{
					ID: "2",
				},
			},
			{
				Repository: &Repository{
					Provider: Provider{
						ID: "provider1",
					},
				},
				Step: Step{
					ID: "3",
				},
			},
			{
				Repository: &Repository{
					Provider: Provider{
						ID: "provider1",
					},
				},
				Step: Step{
					ID: "4",
				},
			},
		}

		for _, p := range pipelines {
			if err := c.SavePipeline("", p); err != nil {
				t.Fatal(err)
			}
		}

		buf := bytes.Buffer{}
		if err := c.ActiveRowLog(context.Background(), "provider1", "1", 0, "1", &buf); err != nil {
			t.Fatal(err)
		}

		expected := builds[0].Jobs[0].Log.String
		if buf.String() != expected {
			t.Fatalf("expected %q but got %q", expected, buf.String())
		}

	})

	t.Run("requesting log of non existent job must return an error", func(t *testing.T) {
		c := NewCache([]CIProvider{mockProvider{id: "provider1"}}, nil)
		build := Build{
			Repository: &Repository{
				Provider: Provider{
					ID: "provider1",
				},
			},
			ID: "1",
			Jobs: []*Job{
				{
					ID:    "1",
					State: Passed,
					Log: utils.NullString{
						Valid:  true,
						String: "log1\n",
					},
				},
			},
		}
		if err := c.SavePipeline("", build); err != nil {
			t.Fatal(err)
		}

		testCases := []struct {
			name      string
			accountID string
			buildID   string
			stageID   int
			jobID     string
		}{
			{
				name:      "unknown provider",
				buildID:   "1",
				accountID: "404",
				stageID:   0,
				jobID:     "1",
			},
			{
				name:      "unknown build",
				accountID: "provider1",
				buildID:   "2",
				stageID:   0,
				jobID:     "1",
			},
			{
				name:      "unknown stage",
				accountID: "provider1",
				buildID:   "1",
				stageID:   1,
			},
			{
				name:      "unknown job",
				accountID: "provider1",
				buildID:   "1",
				jobID:     "404",
			},
		}

		for _, testCase := range testCases {
			err := c.ActiveRowLog(context.Background(), testCase.accountID, testCase.buildID, testCase.stageID, testCase.jobID, nil)
			if err == nil {
				t.Fatal("expected error but got nil")
			}
		}
	})
}*/

func TestCache_BuildsByRef(t *testing.T) {
	c := NewCache(nil, nil)

	pipelines := []Pipeline{
		{
			GitReference: GitReference{
				Ref:   "ref1",
				IsTag: false,
			},
			Step: Step{
				ID: "1",

				UpdatedAt: time.Date(2019, 12, 7, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			GitReference: GitReference{
				Ref:   "ref2",
				IsTag: false,
			},
			Step: Step{
				ID:        "1",
				UpdatedAt: time.Date(2019, 12, 8, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			GitReference: GitReference{
				Ref:   "ref2",
				IsTag: false,
			},
			Step: Step{
				ID:        "2",
				UpdatedAt: time.Date(2019, 12, 9, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	for _, p := range pipelines {
		if err := c.SavePipeline("", p); err != nil {
			t.Fatal(err)
		}
	}

	expected := []Pipeline{pipelines[1], pipelines[2]}
	buildRef2 := c.Pipelines()
	sortPipelines(expected)
	sortPipelines(buildRef2)

	if diff := cmp.Diff(expected, buildRef2, cmp.AllowUnexported(Pipeline{})); len(diff) > 0 {
		t.Fatal(diff)
	}

	// Build with ID 1 must have moved from ref1 to ref2
	if len(c.PipelinesByRef("ref1")) != 0 {
		t.Fatalf("expected empty list but got %+v", c.PipelinesByRef("ref1"))
	}
}

func sortPipelines(pipelines []Pipeline) {
	sort.Slice(pipelines, func(i, j int) bool {
		return pipelines[i].ID < pipelines[j].ID
	})
}

func TestGitOriginURL(t *testing.T) {
	setup := func(t *testing.T, remotes []config.RemoteConfig) (string, string) {
		tmpDir, err := ioutil.TempDir("", "")
		if err != nil {
			t.Fatal(err)
		}
		repo, err := git.PlainInit(tmpDir, false)
		if err != nil {
			t.Fatal(err)
		}

		for _, remoteConfig := range remotes {
			if _, err := repo.CreateRemote(&remoteConfig); err != nil {
				t.Fatal(err)
			}
		}

		// Populate repository with single commit
		w, err := repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		if err := ioutil.WriteFile(path.Join(tmpDir, "file.txt"), []byte("abcd"), os.ModeAppend); err != nil {
			t.Fatal(err)
		}
		sha, err := w.Commit("message", &git.CommitOptions{
			Author: &object.Signature{
				Name:  "name",
				Email: "email",
				When:  time.Date(2019, 19, 12, 21, 49, 0, 0, time.UTC),
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := repo.CreateTag("0.1.0", sha, nil); err != nil {
			t.Fatal(err)
		}

		return tmpDir, sha.String()
	}

	t.Run("invalid path", func(t *testing.T) {
		_, _, err := GitOriginURL("invalid path", "HEAD")
		if err != ErrUnknownRepositoryURL {
			t.Fatalf("expected %v but got %v", ErrUnknownRepositoryURL, err)
		}
	})

	t.Run("invalid path in git repository", func(t *testing.T) {
		repositoryPath, _ := setup(t, nil)
		defer os.RemoveAll(repositoryPath)

		_, _, err := GitOriginURL(path.Join(repositoryPath, "invalidpath"), "HEAD")
		if err != ErrUnknownRepositoryURL {
			t.Fatalf("expected %v but got %v", ErrUnknownRepositoryURL, err)
		}
	})

	t.Run("remote URLs", func(t *testing.T) {
		remotes := []config.RemoteConfig{
			{
				Name:  "origin",
				URLs:  []string{"url1", "url2"},
				Fetch: nil,
			},
			{
				Name:  "other",
				URLs:  []string{"url3", "url4"},
				Fetch: nil,
			},
		}
		repositoryPath, _ := setup(t, remotes)
		defer os.RemoveAll(repositoryPath)

		urls, _, err := GitOriginURL(repositoryPath, "HEAD")
		if err != nil {
			t.Fatal(err)
		}

		sort.Strings(urls)
		if diff := cmp.Diff(urls, []string{"url1", "url2", "url3", "url4"}); len(diff) > 0 {
			t.Fatal(diff)
		}
	})

	t.Run("commit references", func(t *testing.T) {
		repositoryPath, sha := setup(t, nil)
		defer os.RemoveAll(repositoryPath)

		expectedCommit := Commit{
			Sha:      sha,
			Author:   "name <email>",
			Date:     time.Date(2019, 19, 12, 21, 49, 0, 0, time.UTC),
			Message:  "message",
			Branches: []string{"master"},
			Tags:     []string{"0.1.0"},
			Head:     "master",
		}

		references := []string{
			sha,      // Complete hash
			sha[:7],  // Abbreviated hash
			"master", // Branch
			"0.1.0",  // Tag
			"HEAD",
		}

		for _, ref := range references {
			t.Run(fmt.Sprintf("reference %q", ref), func(t *testing.T) {
				_, commit, err := GitOriginURL(repositoryPath, ref)
				if err != nil {
					t.Fatal(err)
				}

				if diff := cmp.Diff(expectedCommit, commit); len(diff) > 0 {
					t.Fatal(diff)
				}
			})
		}
	})

}
