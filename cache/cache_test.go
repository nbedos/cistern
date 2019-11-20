package cache

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

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
				Jobs: map[int]*Job{
					3: {ID: 3},
					4: {ID: 4},
				},
			},
		},
		Jobs: map[int]*Job{
			5: {ID: 5},
			6: {ID: 6},
			7: {ID: 7},
		},
	}

	successTestCases := []struct {
		stageID int
		jobID   int
	}{
		{
			stageID: 2,
			jobID:   3,
		},
		{
			stageID: 2,
			jobID:   4,
		},
		{
			stageID: 0,
			jobID:   5,
		},
	}

	for _, testCase := range successTestCases {
		t.Run(fmt.Sprintf("Case %+v", testCase), func(t *testing.T) {
			job, exists := build.Get(testCase.stageID, testCase.jobID)
			if !exists {
				t.Fail()
			}
			if job.ID != testCase.jobID {
				t.Fatalf("expected jobID %d but got %d", testCase.jobID, job.ID)
			}
		})
	}

	failureTestCases := []struct {
		stageID int
		jobID   int
	}{
		{
			stageID: 0,
			jobID:   0,
		},
		{
			stageID: 2,
			jobID:   0,
		},
		{
			stageID: -1,
			jobID:   -1,
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
		c := NewCache(nil)
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

	t.Run("If a build with the same key is already in the cache it must be overwritten", func(t *testing.T) {
		c := NewCache(nil)

		buildFailed := Build{Repository: &repository, ID: "42", State: Failed}
		if err := c.Save(buildFailed); err != nil {
			t.Fatal(err)
		}
		buildPassed := Build{Repository: &repository, ID: "42", State: Passed}
		if err := c.Save(buildPassed); err != nil {
			t.Fatal(err)
		}
		savedBuild, exists := c.fetchBuild(buildPassed.Repository.AccountID, buildPassed.ID)
		if !exists {
			t.Fatal("build was not saved")
		}
		if savedBuild.State != buildPassed.State {
			t.Fatal("build state must be equal")
		}
	})

	t.Run("Pointer to repository must not be nil", func(t *testing.T) {
		c := NewCache(nil)
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
		c := NewCache(nil)
		if err := c.Save(build); err != nil {
			t.Fatal(err)
		}

		job := Job{ID: 43}
		if err := c.SaveJob(repository.AccountID, build.ID, 0, job); err != nil {
			t.Fatal(err)
		}
		savedJob, _ := c.fetchJob(repository.AccountID, build.ID, 0, 43)
		if !reflect.DeepEqual(savedJob, job) {
			t.Fatalf("job (%+v) must be equalf to savedJob (%+v)", job, savedJob)
		}
	})

	t.Run("Save job with stage", func(t *testing.T) {
		c := NewCache(nil)
		if err := c.Save(build); err != nil {
			t.Fatal(err)
		}

		job := Job{ID: 45}
		if err := c.SaveJob(repository.AccountID, build.ID, 43, job); err != nil {
			t.Fatal(err)
		}
		savedJob, exists := c.fetchJob(repository.AccountID, build.ID, 43, 45)
		if !exists {
			t.Fatal("job not found")
		}
		if !reflect.DeepEqual(savedJob, job) {
			t.Logf("savedJob: %+v", savedJob)
			t.Logf("job     : %+v", job)
			t.Fatal("job must be equal to savedJob")
		}
	})

	t.Run("Saving job to non-existent stage must fail", func(t *testing.T) {
		c := NewCache(nil)
		if err := c.Save(build); err != nil {
			t.Fatal(err)
		}

		if err := c.SaveJob(repository.AccountID, build.ID, 404, Job{ID: 45}); err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}

func TestCache_Builds(t *testing.T) {
	repository := Repository{
		AccountID: "testAccount",
	}

	ids := []string{"1", "2", "3", "4"}
	c := NewCache(nil)
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
func (p mockProvider) Builds(ctx context.Context, repositoryURL string, limit int, buildc chan<- Build) error {
	for _, build := range p.builds {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case buildc <- build:
			// Do nothing
		}
	}
	return nil
}
func (p mockProvider) Log(ctx context.Context, repository Repository, jobID int) (string, bool, error) {
	return p.id + "\n", true, nil
}

func (p mockProvider) StreamLog(ctx context.Context, repositoryID int, jobID int, writer io.Writer) error {
	_, err := writer.Write([]byte(p.id + "\n"))
	return err
}

type errProvider struct {
	id  string
	err error
}

func (p errProvider) AccountID() string { return p.id }
func (p errProvider) Builds(ctx context.Context, repositoryURL string, limit int, buildc chan<- Build) error {
	return p.err
}
func (p errProvider) Log(ctx context.Context, repository Repository, jobID int) (string, bool, error) {
	return "", true, nil
}
func (p errProvider) StreamLog(ctx context.Context, repositoryID int, jobID int, writer io.Writer) error {
	return nil
}

func TestCache_UpdateFromProviders(t *testing.T) {
	t.Run("The absence of providers must cause the function to return instantly", func(t *testing.T) {
		c := NewCache(nil)
		updates := make(chan time.Time)
		err := c.UpdateFromProviders(context.Background(), "url", 42, updates)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Builds of each provider must be found in cache afterwards", func(t *testing.T) {
		c := NewCache([]Provider{
			mockProvider{
				id: "provider1",
				builds: []Build{
					{Repository: &Repository{AccountID: "provider1"}, ID: "1"},
					{Repository: &Repository{AccountID: "provider1"}, ID: "2"},
					{Repository: &Repository{AccountID: "provider1"}, ID: "3"},
					{Repository: &Repository{AccountID: "provider1"}, ID: "4"},
				},
			},
			mockProvider{
				id: "provider2",
				builds: []Build{
					{Repository: &Repository{AccountID: "provider2"}, ID: "5"},
					{Repository: &Repository{AccountID: "provider2"}, ID: "6"},
					{Repository: &Repository{AccountID: "provider2"}, ID: "7"},
				},
			},
		})
		updates := make(chan time.Time)
		err := c.UpdateFromProviders(context.Background(), "url", 4, updates)
		if err != nil {
			t.Fatal(err)
		}

		expected := 7
		if len(c.Builds()) != expected {
			t.Fatalf("expected %d builds in cache but found %d", expected, len(c.builds))
		}
	})

	t.Run("Must return ErrProviderNotFound if all providers return this same error", func(t *testing.T) {
		c := NewCache([]Provider{
			errProvider{id: "provider1", err: ErrRepositoryNotFound},
			errProvider{id: "provider2", err: ErrRepositoryNotFound},
			errProvider{id: "provider3", err: ErrRepositoryNotFound},
			errProvider{id: "provider4", err: ErrRepositoryNotFound},
		})
		updates := make(chan time.Time)
		err := c.UpdateFromProviders(context.Background(), "url", 10, updates)
		if err != ErrRepositoryNotFound {
			t.Fatalf("expected %v but got %v", ErrRepositoryNotFound, err)
		}
	})

	t.Run("Must return nil if at least one provider returns nil and all others return ErrProviderNotFound", func(t *testing.T) {
		c := NewCache([]Provider{
			errProvider{id: "provider1", err: ErrRepositoryNotFound},
			errProvider{id: "provider2", err: ErrRepositoryNotFound},
			mockProvider{
				id:     "provider0",
				builds: []Build{},
			},
			errProvider{id: "provider3", err: ErrRepositoryNotFound},
			errProvider{id: "provider4", err: ErrRepositoryNotFound},
		})
		updates := make(chan time.Time)
		err := c.UpdateFromProviders(context.Background(), "url", 10, updates)
		if err != nil {
			t.Fatalf("expected %v but got %v", nil, err)
		}
	})

	t.Run("Errors returned by call to cache.save must be returned", func(t *testing.T) {
		// Builds with nil Repository attribute will cause cache.Save(build) to fail
		c := NewCache([]Provider{
			mockProvider{
				id:     "provider0",
				builds: []Build{{}, {}, {}},
			},
			mockProvider{
				id:     "provider1",
				builds: []Build{{}, {}, {}},
			},
		})
		updates := make(chan time.Time)
		err := c.UpdateFromProviders(context.Background(), "url", 10, updates)
		if err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}

func TestCache_WriteLog(t *testing.T) {
	t.Run("log not saved in cache must be retrieved from provider", func(t *testing.T) {
		c := NewCache([]Provider{
			mockProvider{
				id: "provider1",
			},
			mockProvider{
				id: "provider2",
			},
		})

		builds := []Build{
			{
				Repository: &Repository{AccountID: "provider1"},
				ID:         "1",
				Jobs: map[int]*Job{
					1: {
						ID:    1,
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
		if err := c.WriteLog(context.Background(), "provider1", "1", 0, 1, &buf); err != nil {
			t.Fatal(err)
		}

		// Value return by provider.Log()
		expected := "provider1\n"
		if buf.String() != expected {
			t.Fatalf("expected %q but got %q", expected, buf.String())
		}

	})

	t.Run("log saved in cache must be returned as is", func(t *testing.T) {
		c := NewCache([]Provider{mockProvider{id: "provider1"}})
		builds := []Build{
			{
				Repository: &Repository{AccountID: "provider1"},
				ID:         "1",
				Jobs: map[int]*Job{
					1: {
						ID:    1,
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
		if err := c.WriteLog(context.Background(), "provider1", "1", 0, 1, &buf); err != nil {
			t.Fatal(err)
		}

		expected := builds[0].Jobs[1].Log.String
		if buf.String() != expected {
			t.Fatalf("expected %q but got %q", expected, buf.String())
		}

	})

	t.Run("requesting log of non existent job must return an error", func(t *testing.T) {
		c := NewCache([]Provider{mockProvider{id: "provider1"}})
		build := Build{
			Repository: &Repository{AccountID: "provider1"},
			ID:         "1",
			Jobs: map[int]*Job{
				1: {
					ID:    1,
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
			jobID     int
		}{
			{
				name:      "unknown provider",
				buildID:   "1",
				accountID: "404",
				stageID:   0,
				jobID:     1,
			},
			{
				name:      "unknown build",
				accountID: "provider1",
				buildID:   "2",
				stageID:   0,
				jobID:     1,
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
				jobID:     404,
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
