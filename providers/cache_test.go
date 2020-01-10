package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v3"
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
	t.Run("Saved build must be returned by pipeline()", func(t *testing.T) {
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

func TestCache_Pipeline(t *testing.T) {
	ids := []string{"1", "2", "3", "4"}
	c := NewCache(nil, nil)
	for _, id := range ids {
		p := Pipeline{
			ProviderHost: "host",
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

func TestCache_Pipelines(t *testing.T) {
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
		if err := c.SavePipeline("ref2", p); err != nil {
			t.Fatal(err)
		}
	}

	expected := []Pipeline{pipelines[1], pipelines[2]}
	buildRef2 := c.Pipelines("ref2")
	sortPipelines(expected)
	sortPipelines(buildRef2)

	if diff := Pipelines(expected).Diff(buildRef2); len(diff) > 0 {
		t.Fatal(diff)
	}

	// Build with ID 1 must have moved from ref1 to ref2
	if len(c.Pipelines("ref1")) != 0 {
		t.Fatalf("expected empty list but got %+v", c.Pipelines("ref1"))
	}
}

func sortPipelines(pipelines []Pipeline) {
	sort.Slice(pipelines, func(i, j int) bool {
		return pipelines[i].ID < pipelines[j].ID
	})
}

func createRepository(t *testing.T, remotes []config.RemoteConfig) (string, string) {
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
			Name:  "nName",
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

func TestRemotesAndCommit(t *testing.T) {
	t.Run("invalid path", func(t *testing.T) {
		_, _, err := RemotesAndCommit("invalid path", "HEAD")
		if err != ErrUnknownRepositoryURL {
			t.Fatalf("expected %v but got %v", ErrUnknownRepositoryURL, err)
		}
	})

	t.Run("invalid path in git repository", func(t *testing.T) {
		repositoryPath, _ := createRepository(t, nil)
		defer os.RemoveAll(repositoryPath)

		_, _, err := RemotesAndCommit(path.Join(repositoryPath, "invalidpath"), "HEAD")
		if err != ErrUnknownRepositoryURL {
			t.Fatalf("expected %v but got %v", ErrUnknownRepositoryURL, err)
		}
	})

	t.Run("remote URLs", func(t *testing.T) {
		remotes := []config.RemoteConfig{
			{
				Name:  "origin",
				URLs:  []string{"pushfetch1"},
				Fetch: nil,
			},
			{
				Name:  "other1",
				URLs:  []string{"pushfetch2", "push1"},
				Fetch: nil,
			},
			{
				Name:  "other2",
				URLs:  []string{"pushfetch3", "push2", "push3"},
				Fetch: nil,
			},
			{
				Name:  "other3",
				URLs:  []string{"pushfetch3", "push4"},
				Fetch: nil,
			},
		}
		repositoryPath, _ := createRepository(t, remotes)
		//defer os.RemoveAll(repositoryPath)

		// Setup insteadOf configuration
		cmd := exec.Command("git", "config", "url.push5.insteadOf", "push3")
		cmd.Dir = repositoryPath
		if _, err := cmd.Output(); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("git", "config", "url.push6.pushInsteadOf", "push4")
		cmd.Dir = repositoryPath
		if _, err := cmd.Output(); err != nil {
			t.Fatal(err)
		}

		urls, _, err := RemotesAndCommit(repositoryPath, "HEAD")
		if err != nil {
			t.Fatal(err)
		}

		expectedURLs := []string{
			"push1",
			"push2",
			"push5",
			"push6",
			"pushfetch1",
			"pushfetch2",
			"pushfetch3",
		}
		sort.Strings(urls)
		sort.Strings(expectedURLs)
		if diff := cmp.Diff(expectedURLs, urls); len(diff) > 0 {
			t.Fatal(diff)
		}
	})

	t.Run("commit references", func(t *testing.T) {
		repositoryPath, sha := createRepository(t, nil)
		defer os.RemoveAll(repositoryPath)

		expectedCommit := Commit{
			Sha:      sha,
			Author:   "nName <email>",
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
				_, commit, err := RemotesAndCommit(repositoryPath, ref)
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

type testProvider struct {
	id         string
	url        string
	callNumber int
}

func (p testProvider) ID() string { return p.id }

func (p *testProvider) RefStatuses(ctx context.Context, url, ref, sha string) ([]string, error) {
	if !strings.Contains(url, p.url) {
		return nil, ErrUnknownRepositoryURL
	}
	switch p.callNumber++; p.callNumber {
	case 1:
		return []string{url + "_status0"}, nil
	case 2:
		return []string{url + "_status0", url + "_status1"}, nil
	default:
		return []string{url + "_status0", url + "_status1", url + "_status2"}, nil
	}
}

func (p testProvider) Commit(ctx context.Context, repo, sha string) (Commit, error) {
	if !strings.Contains(repo, p.url) {
		return Commit{}, ErrUnknownRepositoryURL
	}
	return Commit{}, nil
}

func TestCache_monitorRefStatus(t *testing.T) {
	ctx := context.Background()
	p := testProvider{"Provider", "url", 0}
	commitc := make(chan Commit)
	errc := make(chan error)

	b := backoff.ExponentialBackOff{
		InitialInterval:     time.Millisecond,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         10 * time.Millisecond,
		MaxElapsedTime:      10 * time.Millisecond,
		Clock:               backoff.SystemClock,
	}

	go func() {
		err := monitorRefStatuses(ctx, &p, b, "url", "ref", commitc)
		close(commitc)
		errc <- err
		close(errc)
	}()

	var c Commit
	for c = range commitc {
	}

	if err := <-errc; err != nil {
		t.Fatal(err)
	}

	statuses := []string{"url_status0", "url_status1", "url_status2"}
	if diff := cmp.Diff(c.Statuses, statuses); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestCache_broadcastMonitorRefStatus(t *testing.T) {
	ctx := context.Background()
	c := NewCache(nil, []SourceProvider{
		&testProvider{"origin", "origin", 0},
		&testProvider{"other", "other", 0},
	})

	repositoryPath, sha := createRepository(t, []config.RemoteConfig{
		{
			Name: "origin",
			URLs: []string{"origin1", "origin2"},
		},
		{
			Name: "other",
			URLs: []string{"other1"},
		},
	})
	defer os.RemoveAll(repositoryPath)

	commitc := make(chan Commit)
	errc := make(chan error)
	b := backoff.ExponentialBackOff{
		InitialInterval:     time.Millisecond,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         10 * time.Millisecond,
		MaxElapsedTime:      10 * time.Millisecond,
		Clock:               backoff.SystemClock,
	}

	go func() {
		err := c.broadcastMonitorRefStatus(ctx, repositoryPath, sha, commitc, b)
		close(commitc)
		errc <- err
		close(errc)
	}()

	statuses := make(map[string]struct{}, 0)
	for commit := range commitc {
		for _, status := range commit.Statuses {
			statuses[status] = struct{}{}
		}
	}

	if err := <-errc; err != nil {
		t.Fatal(err)
	}

	expectedStatuses := map[string]struct{}{
		"origin1_status0": {},
		"origin1_status1": {},
		"origin1_status2": {},
		"origin2_status0": {},
		"origin2_status1": {},
		"origin2_status2": {},
		"other1_status0":  {},
		"other1_status1":  {},
		"other1_status2":  {},
	}
	if diff := cmp.Diff(statuses, expectedStatuses); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestConfiguration_TokenFromProcess(t *testing.T) {
	t.Run("token from environment variables", func(t* testing.T){
		envVar := "CITOP_TEST_TOKEN_FROM_ENV_VAR"
		if err := os.Setenv(envVar, "token"); err != nil {
			t.Fatal(err)
		}

		token, err := token("", []string{"sh", "-c", "echo $" + envVar})
		if err != nil {
			t.Fatal(err)
		}
		if token != "token" {
			t.Fatalf("expected %q but got %q", "token", token)
		}
	})
}
