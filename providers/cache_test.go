package providers

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/cistern/utils"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/config"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

func TestCache_Save(t *testing.T) {
	t.Run("Saved build must be returned by pipeline()", func(t *testing.T) {
		c := NewCache(nil, nil, utils.PollingStrategy{})
		p := Pipeline{
			Step: Step{
				ID:    "42",
				State: Failed,
			},
		}
		if _, err := c.SavePipeline(p); err != nil {
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
			ID:    "42",
			State: Failed,
			UpdatedAt: utils.NullTime{
				Valid: true,
				Time:  time.Date(2019, 11, 24, 14, 52, 0, 0, time.UTC),
			},
		},
	}
	newPipeline := Pipeline{
		Step: Step{
			ID:    "42",
			State: Passed,
			UpdatedAt: utils.NullTime{
				Valid: true,
				Time:  oldPipeline.UpdatedAt.Time.Add(time.Second),
			},
		},
	}

	t.Run("existing build must be overwritten if it's older than the current build", func(t *testing.T) {
		c := NewCache(nil, nil, utils.PollingStrategy{})

		if _, err := c.SavePipeline(oldPipeline); err != nil {
			t.Fatal(err)
		}
		if _, err := c.SavePipeline(newPipeline); err != nil {
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
		c := NewCache(nil, nil, utils.PollingStrategy{})

		if _, err := c.SavePipeline(newPipeline); err != nil {
			t.Fatal(err)
		}
		if _, err := c.SavePipeline(oldPipeline); err != ErrObsoleteBuild {
			t.Fatalf("expected %v but got %v", ErrObsoleteBuild, err)
		}
	})
}

func TestCache_Pipeline(t *testing.T) {
	ids := []string{"1", "2", "3", "4"}
	c := NewCache(nil, nil, utils.PollingStrategy{})
	for _, id := range ids {
		p := Pipeline{
			ProviderHost: "host",
			Step: Step{
				ID: id,
			},
		}
		if _, err := c.SavePipeline(p); err != nil {
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
	c := NewCache(nil, nil, utils.PollingStrategy{})

	pipelines := []Pipeline{
		{
			GitReference: GitReference{
				SHA:   "sha1",
				Ref:   "ref1",
				IsTag: false,
			},
			Step: Step{
				ID: "1",

				UpdatedAt: utils.NullTime{
					Valid: true,
					Time:  time.Date(2019, 12, 7, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			GitReference: GitReference{
				SHA:   "sha2",
				Ref:   "ref2",
				IsTag: false,
			},
			Step: Step{
				ID: "1",
				UpdatedAt: utils.NullTime{
					Valid: true,
					Time:  time.Date(2019, 12, 8, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			GitReference: GitReference{
				SHA:   "sha2",
				Ref:   "ref2",
				IsTag: false,
			},
			Step: Step{
				ID: "2",
				UpdatedAt: utils.NullTime{
					Valid: true,
					Time:  time.Date(2019, 12, 9, 0, 0, 0, 0, time.UTC),
				},
			},
		},
	}

	c.SaveCommit("ref1", Commit{Sha: "sha1"})
	c.SaveCommit("ref2", Commit{Sha: "sha2"})
	for _, p := range pipelines {
		if _, err := c.SavePipeline(p); err != nil {
			t.Fatal(err)
		}
	}

	expected := []Pipeline{pipelines[0]}
	buildRef1 := c.Pipelines("ref1")
	if diff := Pipelines(expected).Diff(buildRef1); len(diff) > 0 {
		t.Fatal(diff)
	}

	// Point "ref1" to "sha2"
	c.SaveCommit("ref1", Commit{Sha: "sha2"})
	expected = []Pipeline{pipelines[1], pipelines[2]}
	buildRef1 = c.Pipelines("ref2")
	sortPipelines(buildRef1)
	sortPipelines(expected)
	if diff := Pipelines(expected).Diff(buildRef1); len(diff) > 0 {
		t.Fatal(diff)
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
			Name:  "Name",
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

func TestRemotes(t *testing.T) {
	t.Run("invalid path", func(t *testing.T) {
		_, err := Remotes("invalid path")
		if err != ErrUnknownRepositoryURL {
			t.Fatalf("expected %v but got %v", ErrUnknownRepositoryURL, err)
		}
	})

	t.Run("invalid path in git repository", func(t *testing.T) {
		repositoryPath, _ := createRepository(t, nil)
		defer os.RemoveAll(repositoryPath)

		_, err := Remotes(path.Join(repositoryPath, "invalidpath"))
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
		defer os.RemoveAll(repositoryPath)

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

		urls, err := Remotes(repositoryPath)
		if err != nil {
			t.Fatal(err)
		}

		expectedURLs := map[string][]string{
			"other1": {"pushfetch2", "push1"},
			"origin": {"pushfetch1"},
			"other2": {"pushfetch3", "push2", "push5"},
			"other3": {"push6"},
		}
		if diff := cmp.Diff(expectedURLs, urls); len(diff) > 0 {
			t.Fatal(diff)
		}
	})
}

func TestResolveCommit(t *testing.T) {
	t.Run("invalid path", func(t *testing.T) {
		_, err := ResolveCommit("invalid path", "HEAD")
		if err != ErrUnknownRepositoryURL {
			t.Fatalf("expected %v but got %v", ErrUnknownRepositoryURL, err)
		}
	})

	t.Run("invalid path in git repository", func(t *testing.T) {
		repositoryPath, _ := createRepository(t, nil)
		defer os.RemoveAll(repositoryPath)

		_, err := ResolveCommit(path.Join(repositoryPath, "invalidpath"), "HEAD")
		if err != ErrUnknownRepositoryURL {
			t.Fatalf("expected %v but got %v", ErrUnknownRepositoryURL, err)
		}
	})

	t.Run("commit references", func(t *testing.T) {
		repositoryPath, sha := createRepository(t, nil)
		defer os.RemoveAll(repositoryPath)

		expectedCommit := Commit{
			Sha:      sha,
			Author:   "Name <email>",
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
				commit, err := ResolveCommit(repositoryPath, ref)
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
	statusURL := func(status string) string {
		return fmt.Sprintf("%s/%s/%s", url, ref, status)
	}
	switch p.callNumber++; p.callNumber {
	case 1:
		return []string{statusURL("status0")}, nil
	case 2:
		return []string{statusURL("status0"), statusURL("status1")}, nil
	default:
		return []string{statusURL("status0"), statusURL("status1"), statusURL("status2")}, nil
	}
}

func (p testProvider) Commit(ctx context.Context, repo, sha string) (Commit, error) {
	if !strings.Contains(repo, p.url) {
		return Commit{}, ErrUnknownRepositoryURL
	}
	return Commit{}, nil
}

func (p testProvider) Host() string {
	return ""
}

func (p testProvider) Name() string {
	return ""
}

func (p testProvider) Log(ctx context.Context, step Step) (string, error) {
	return "", nil
}

func (p *testProvider) BuildFromURL(ctx context.Context, u string) (Pipeline, error) {
	p.callNumber++
	if !strings.Contains(u, p.url) {
		return Pipeline{}, ErrUnknownPipelineURL
	}
	if strings.Contains(u, "inactive") {
		switch p.callNumber {
		case 1:
			return Pipeline{Step: Step{State: Pending}}, nil
		case 2:
			return Pipeline{Step: Step{State: Running}}, nil
		default:
			return Pipeline{Step: Step{State: Passed}}, nil
		}
	}

	return Pipeline{Step: Step{State: Running}}, nil
}

func TestCache_monitorRefStatus(t *testing.T) {
	ctx := context.Background()
	p := testProvider{"Provider", "url", 0}
	commitc := make(chan Commit)
	errc := make(chan error)

	rand.Seed(0)
	s := utils.PollingStrategy{
		InitialInterval: time.Millisecond,
		Multiplier:      1.5,
		Randomizer:      0.25,
		MaxInterval:     10 * time.Millisecond,
	}

	go func() {
		err := monitorRefStatuses(ctx, &p, s, "remoteName", "url", "ref", commitc)
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

	statuses := []string{"url/ref/status0", "url/ref/status1", "url/ref/status2"}
	if diff := cmp.Diff(c.Statuses, statuses); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestCache_broadcastMonitorRefStatus(t *testing.T) {
	rand.Seed(0)
	ctx := context.Background()
	c := NewCache(nil, []SourceProvider{
		&testProvider{"origin", "origin", 0},
		&testProvider{"other", "other", 0},
	}, utils.PollingStrategy{
		InitialInterval: time.Millisecond,
		Multiplier:      1.5,
		Randomizer:      0.25,
		MaxInterval:     10 * time.Millisecond,
	})

	commitc := make(chan Commit)
	errc := make(chan error)

	remotes := map[string][]string{
		"origin1": {"origin1.example.com"},
		"origin2": {"origin2.example.com"},
		"other1":  {"other1.example.com"},
	}

	go func() {
		err := c.broadcastMonitorRefStatus(ctx, remotes, "sha", commitc)
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
		"origin1.example.com/sha/status0": {},
		"origin1.example.com/sha/status1": {},
		"origin1.example.com/sha/status2": {},
		"origin2.example.com/sha/status0": {},
		"origin2.example.com/sha/status1": {},
		"origin2.example.com/sha/status2": {},
		"other1.example.com/sha/status0":  {},
		"other1.example.com/sha/status1":  {},
		"other1.example.com/sha/status2":  {},
	}
	if diff := cmp.Diff(statuses, expectedStatuses); len(diff) > 0 {
		t.Fatal(diff)
	}
}

func TestCache_monitorPipeline(t *testing.T) {
	t.Run("monitorPipeline must save pipeline in cache and return once the pipeline becomes inactive", func(t *testing.T) {
		rand.Seed(0)
		ctx := context.Background()
		c := NewCache([]CIProvider{
			&testProvider{"ci", "ci.example.com", 0},
		}, nil, utils.PollingStrategy{
			InitialInterval: time.Millisecond,
			Multiplier:      1.5,
			Randomizer:      0.25,
			MaxInterval:     10 * time.Millisecond,
		})

		err := c.monitorPipeline(ctx, "ci", "ci.example.com/pipelines/inactive", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := c.Pipeline(PipelineKey{}); !exists {
			t.Fatal("pipeline was not saved in cache")
		}
	})

	t.Run("monitorPipeline must return ErrUnknownPipelineURL if the provider cannot handle the URL", func(t *testing.T) {
		rand.Seed(0)
		ctx := context.Background()
		c := NewCache([]CIProvider{
			&testProvider{"ci", "ci.example.com", 0},
		}, nil, utils.PollingStrategy{
			InitialInterval: time.Millisecond,
			Multiplier:      1.5,
			Randomizer:      0.25,
			MaxInterval:     10 * time.Millisecond,
		})

		err := c.monitorPipeline(ctx, "ci", "bad.url.example.com", nil)
		if err != ErrUnknownPipelineURL {
			t.Fatalf("expected %v but got %v", ErrUnknownPipelineURL, err)
		}
	})

	t.Run("monitorPipeline must be cancellable", func(t *testing.T) {
		rand.Seed(0)
		ctx, cancel := context.WithCancel(context.Background())
		c := NewCache([]CIProvider{
			&testProvider{"ci", "ci.example.com", 0},
		}, nil, utils.PollingStrategy{
			InitialInterval: time.Minute,
			Multiplier:      1.5,
			Randomizer:      0.25,
			MaxInterval:     time.Hour,
		})

		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		started := time.Now()
		err := c.monitorPipeline(ctx, "ci", "ci.example.com/pipelines/active", nil)
		if err != context.Canceled {
			t.Fatalf("expected %v but got %v", context.Canceled, err)
		}

		if elapsed := time.Now().Sub(started); elapsed > c.pollStrat.InitialInterval {
			t.Fatalf("call lasted %v but was expected to last less than %v", elapsed, c.pollStrat.InitialInterval)
		}
	})
}

func TestCache_broadcastMonitorPipeline(t *testing.T) {
	t.Run("broadcastMonitorPipeline must save the pipeline in cache and return once the pipeline becomes inactive", func(t *testing.T) {
		rand.Seed(0)
		ctx := context.Background()
		c := NewCache([]CIProvider{
			&testProvider{"ci1", "ci1.example.com", 0},
			&testProvider{"ci2", "ci2.example.com", 0},
			&testProvider{"ci3", "ci3.example.com", 0},
		}, nil, utils.PollingStrategy{
			InitialInterval: time.Millisecond,
			Multiplier:      1.5,
			Randomizer:      0.25,
			MaxInterval:     10 * time.Millisecond,
		})

		err := c.broadcastMonitorPipeline(ctx, "ci1.example.com/pipelines/inactive", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := c.Pipeline(PipelineKey{}); !exists {
			t.Fatal("pipeline was not saved in cache")
		}
	})

	t.Run("broadcastMonitorPipeline must return ErrUnknownPipelineURL if no provider can handle the URL", func(t *testing.T) {
		rand.Seed(0)
		ctx := context.Background()
		c := NewCache([]CIProvider{
			&testProvider{"ci1", "ci1.example.com", 0},
			&testProvider{"ci2", "ci2.example.com", 0},
			&testProvider{"ci3", "ci3.example.com", 0},
		}, nil, utils.PollingStrategy{
			InitialInterval: time.Millisecond,
			Multiplier:      1.5,
			Randomizer:      0.25,
			MaxInterval:     10 * time.Millisecond,
		})

		err := c.broadcastMonitorPipeline(ctx, "bad.url.example.com", nil)
		if err != ErrUnknownPipelineURL {
			t.Fatalf("expected %v but got %v", ErrUnknownPipelineURL, err)
		}
	})

	t.Run("broadcastMonitorPipeline must be cancellable", func(t *testing.T) {
		rand.Seed(0)
		ctx, cancel := context.WithCancel(context.Background())
		c := NewCache([]CIProvider{
			&testProvider{"ci1", "ci1.example.com", 0},
			&testProvider{"ci2", "ci2.example.com", 0},
			&testProvider{"ci3", "ci3.example.com", 0},
		}, nil, utils.PollingStrategy{
			InitialInterval: time.Minute,
			Multiplier:      1.5,
			Randomizer:      0.25,
			MaxInterval:     time.Hour,
		})

		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		started := time.Now()
		err := c.broadcastMonitorPipeline(ctx, "ci1.example.com/pipelines/active", nil)
		if err != context.Canceled {
			t.Fatalf("expected %v but got %v", context.Canceled, err)
		}

		if elapsed := time.Now().Sub(started); elapsed > c.pollStrat.InitialInterval {
			t.Fatalf("call lasted %v but was expected to last less than %v", elapsed, c.pollStrat.InitialInterval)
		}
	})
}

func TestCache_MonitorPipelines(t *testing.T) {
	t.Run("MonitorPipelines must save the pipeline in cache and return once the pipeline becomes inactive and MaxInterval is exceeded", func(t *testing.T) {
		rand.Seed(0)
		ctx := context.Background()
		ciProviders := []CIProvider{
			&testProvider{"provider", "provider.example.com", 0},
		}
		sourceProviders := []SourceProvider{
			&testProvider{"provider", "provider.example.com", 0},
		}
		c := NewCache(ciProviders, sourceProviders, utils.PollingStrategy{
			InitialInterval: time.Millisecond,
			Multiplier:      1.5,
			Randomizer:      0.25,
			MaxInterval:     2 * time.Millisecond,
		})

		remotes := map[string][]string{
			"provider": {"provider.example.com"},
		}
		ref := Ref{
			Name:   "inactive",
			Commit: Commit{},
		}
		err := c.MonitorPipelines(ctx, remotes, ref, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := c.Pipeline(PipelineKey{}); !exists {
			t.Fatal("pipeline was not saved in cache")
		}
	})

	t.Run("MonitorPipelines must be cancellable", func(t *testing.T) {
		rand.Seed(0)
		ctx, cancel := context.WithCancel(context.Background())
		ciProviders := []CIProvider{
			&testProvider{"provider", "provider.example.com", 0},
		}
		sourceProviders := []SourceProvider{
			&testProvider{"provider", "provider.example.com", 0},
		}
		c := NewCache(ciProviders, sourceProviders, utils.PollingStrategy{
			InitialInterval: time.Minute,
			Multiplier:      1.5,
			Randomizer:      0.25,
			MaxInterval:     time.Hour,
		})

		remotes := map[string][]string{
			"provider": {"provider.example.com"},
		}
		ref := Ref{
			Name:   "active",
			Commit: Commit{},
		}

		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		started := time.Now()
		err := c.MonitorPipelines(ctx, remotes, ref, nil)
		if err != context.Canceled {
			t.Fatalf("expected %v but got %v", context.Canceled, err)
		}
		if elapsed := time.Now().Sub(started); elapsed > c.pollStrat.InitialInterval {
			t.Fatalf("call lasted %v but was expected to last less than %v", elapsed, c.pollStrat.InitialInterval)
		}
	})
}
