package cache

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/citop/utils"
)

func TestAggregateStatuses(t *testing.T) {
	testCases := []struct {
		name      string
		statusers []Statuser
		result    State
	}{
		{
			name:      "Empty list",
			statusers: []Statuser{},
			result:    Unknown,
		},
		{
			name: "Jobs: No allowed failure",
			statusers: []Statuser{
				Job{
					AllowFailure: false,
					State:        Passed,
				},
				Job{
					AllowFailure: false,
					State:        Failed,
				},
				Job{
					AllowFailure: false,
					State:        Passed,
				},
			},
			result: Failed,
		},
		{
			name: "Jobs: Allowed failure",
			statusers: []Statuser{
				Job{
					AllowFailure: false,
					State:        Passed,
				},
				Job{
					AllowFailure: true,
					State:        Failed,
				},
				Job{
					AllowFailure: false,
					State:        Passed,
				},
			},
			result: Passed,
		},
		{
			name: "Builds",
			statusers: []Statuser{
				Build{
					State: Passed,
				},
				Build{
					State: Failed,
				},
				Build{
					State: Passed,
				},
			},
			result: Failed,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if state := AggregateStatuses(testCase.statusers); state != testCase.result {
				t.Fatalf("expected %q but got %q", testCase.result, state)
			}
		})
	}
}

func TestBuild_Get(t *testing.T) {
	build := Build{
		Stages: map[int]*Stage{
			1: {ID: 1},
			2: {
				ID: 2,
				Jobs: []*Job{
					{ID: "3"},
					{ID: "4"},
				},
			},
		},
		Jobs: []*Job{
			{ID: "5"},
			{ID: "6"},
			{ID: "7"},
		},
	}

	successTestCases := []struct {
		stageID int
		jobID   string
	}{
		{
			stageID: 2,
			jobID:   "3",
		},
		{
			stageID: 2,
			jobID:   "4",
		},
		{
			stageID: 0,
			jobID:   "5",
		},
	}

	for _, testCase := range successTestCases {
		t.Run(fmt.Sprintf("Case %+v", testCase), func(t *testing.T) {
			job, exists := build.Get(testCase.stageID, testCase.jobID)
			if !exists {
				t.Fail()
			}
			if job.ID != testCase.jobID {
				t.Fatalf("expected jobID %s but got %s", testCase.jobID, job.ID)
			}
		})
	}

	failureTestCases := []struct {
		stageID int
		jobID   string
	}{
		{
			stageID: 0,
			jobID:   "0",
		},
		{
			stageID: 2,
			jobID:   "0",
		},
		{
			stageID: -1,
			jobID:   "-1",
		},
	}

	for _, testCase := range failureTestCases {
		t.Run(fmt.Sprintf("Case %+v", testCase), func(t *testing.T) {
			_, exists := build.Get(testCase.stageID, testCase.jobID)
			if exists {
				t.Fatalf("expected to not find job %+v", testCase)
			}
		})
	}
}

func TestCache_Save(t *testing.T) {
	repository := Repository{
		AccountID: "testAccount",
	}

	t.Run("Saved build must be returned by fetchBuild()", func(t *testing.T) {
		c := NewCache(nil, nil)
		build := Build{Repository: &repository, ID: "42", State: Failed}
		if err := c.Save(build); err != nil {
			t.Fatal(err)
		}
		savedBuild, exists := c.fetchBuild(build.Repository.AccountID, build.ID)
		if !exists {
			t.Fatal("build was not saved")
		}
		if savedBuild.State != build.State {
			t.Fatal("build state differ")
		}
	})

	buildOld := Build{
		Repository: &repository,
		ID:         "42", State: Failed,
		UpdatedAt: time.Date(2019, 11, 24, 14, 52, 0, 0, time.UTC),
	}
	buildNew := Build{
		Repository: &repository,
		ID:         "42",
		State:      Passed,
		UpdatedAt:  buildOld.UpdatedAt.Add(time.Second),
	}

	t.Run("existing build must be overwritten if it's older than the current build", func(t *testing.T) {
		c := NewCache(nil, nil)

		if err := c.Save(buildOld); err != nil {
			t.Fatal(err)
		}
		if err := c.Save(buildNew); err != nil {
			t.Fatal(err)
		}
		savedBuild, exists := c.fetchBuild(buildOld.Repository.AccountID, buildNew.ID)
		if !exists {
			t.Fatal("build was not saved")
		}
		if savedBuild.State != buildNew.State {
			t.Fatal("build state must be equal")
		}
	})

	t.Run("cache.Save must return ErrOlderBuild if the build to save is older than the one in cache", func(t *testing.T) {
		c := NewCache(nil, nil)

		if err := c.Save(buildNew); err != nil {
			t.Fatal(err)
		}
		if err := c.Save(buildOld); err != ErrOlderBuild {
			t.Fatalf("expected %v but got %v", ErrOlderBuild, err)
		}
	})

	t.Run("Pointer to repository must not be nil", func(t *testing.T) {
		c := NewCache(nil, nil)
		build := Build{Repository: nil, ID: "42", State: Passed}
		if err := c.Save(build); err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}

func TestCache_SaveJob(t *testing.T) {
	repository := Repository{
		AccountID: "testAccount",
	}

	build := Build{
		Repository: &repository,
		ID:         "42",
		State:      Failed,
		Stages: map[int]*Stage{
			43: {
				ID: 43,
			},
		},
	}

	t.Run("Save job without stage", func(t *testing.T) {
		c := NewCache(nil, nil)
		if err := c.Save(build); err != nil {
			t.Fatal(err)
		}

		job := Job{ID: "43"}
		if err := c.SaveJob(repository.AccountID, build.ID, 0, job); err != nil {
			t.Fatal(err)
		}
		savedJob, _ := c.fetchJob(repository.AccountID, build.ID, 0, "43")
		if diff := cmp.Diff(savedJob, job); len(diff) > 0 {
			t.Fatal(diff)
		}
	})

	t.Run("Save job with stage", func(t *testing.T) {
		c := NewCache(nil, nil)
		if err := c.Save(build); err != nil {
			t.Fatal(err)
		}

		job := Job{ID: "45"}
		if err := c.SaveJob(repository.AccountID, build.ID, 43, job); err != nil {
			t.Fatal(err)
		}
		savedJob, exists := c.fetchJob(repository.AccountID, build.ID, 43, "45")
		if !exists {
			t.Fatal("job not found")
		}
		if diff := cmp.Diff(savedJob, job); len(diff) > 0 {
			t.Fatal(diff)
		}
	})

	t.Run("Saving job to non-existent stage must fail", func(t *testing.T) {
		c := NewCache(nil, nil)
		if err := c.Save(build); err != nil {
			t.Fatal(err)
		}

		if err := c.SaveJob(repository.AccountID, build.ID, 404, Job{ID: "45"}); err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}

func TestCache_Builds(t *testing.T) {
	repository := Repository{
		AccountID: "testAccount",
	}

	ids := []string{"1", "2", "3", "4"}
	c := NewCache(nil, nil)
	for _, id := range ids {
		if err := c.Save(Build{Repository: &repository, ID: id}); err != nil {
			t.Fatal(err)
		}
	}

	for _, id := range ids {
		_, exists := c.fetchBuild(repository.AccountID, id)
		if !exists {
			t.Fatal("build not found")
		}
	}
}

type mockProvider struct {
	id     string
	builds []Build
}

func (p mockProvider) AccountID() string { return p.id }
func (p mockProvider) Log(ctx context.Context, repository Repository, jobID string) (string, error) {
	return p.id + "\n", nil
}
func (p mockProvider) BuildFromURL(ctx context.Context, u string) (Build, error) {
	return Build{}, nil
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

		builds := []Build{
			{
				Repository: &Repository{AccountID: "provider1"},
				ID:         "1",
				Jobs: []*Job{
					{
						ID:    "1",
						State: Passed,
						Log: utils.NullString{
							Valid: false,
						},
					},
				},
			},
			{Repository: &Repository{AccountID: "provider1"}, ID: "2"},
			{Repository: &Repository{AccountID: "provider1"}, ID: "3"},
			{Repository: &Repository{AccountID: "provider1"}, ID: "4"},
		}

		for _, build := range builds {
			if err := c.Save(build); err != nil {
				t.Fatal(err)
			}
		}

		buf := bytes.Buffer{}
		if err := c.WriteLog(context.Background(), "provider1", "1", 0, "1", &buf); err != nil {
			t.Fatal(err)
		}

		// Value return by provider.Log()
		expected := "provider1\n"
		if buf.String() != expected {
			t.Fatalf("expected %q but got %q", expected, buf.String())
		}

	})

	t.Run("log saved in cache must be returned as is", func(t *testing.T) {
		c := NewCache([]CIProvider{mockProvider{id: "provider1"}}, nil)
		builds := []Build{
			{
				Repository: &Repository{AccountID: "provider1"},
				ID:         "1",
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
			},
			{Repository: &Repository{AccountID: "provider1"}, ID: "2"},
			{Repository: &Repository{AccountID: "provider1"}, ID: "3"},
			{Repository: &Repository{AccountID: "provider1"}, ID: "4"},
		}

		for _, build := range builds {
			if err := c.Save(build); err != nil {
				t.Fatal(err)
			}
		}

		buf := bytes.Buffer{}
		if err := c.WriteLog(context.Background(), "provider1", "1", 0, "1", &buf); err != nil {
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
			Repository: &Repository{AccountID: "provider1"},
			ID:         "1",
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
		if err := c.Save(build); err != nil {
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
			err := c.WriteLog(context.Background(), testCase.accountID, testCase.buildID, testCase.stageID, testCase.jobID, nil)
			if err == nil {
				t.Fatal("expected error but got nil")
			}
		}
	})
}
