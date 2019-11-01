package providers

import (
	"context"
	"github.com/nbedos/citop/cache"
	"os"
	"testing"
	"time"
)

func Test_RecentRepoBuilds(t *testing.T) {
	token := os.Getenv("TRAVIS_API_TOKEN")
	if token == "" {
		t.Fatal("Environment variable TRAVIS_API_TOKEN is not set")
	}

	client := NewTravisClient(TravisOrgURL, TravisPusherHost, token, "travis", time.Millisecond*time.Duration(50))

	repository := cache.Repository{
		AccountID: "travis",
		URL:       "https://github.com/nbedos/citop",
		Owner:     "nbedos",
		Name:      "citop",
	}
	errc := make(chan error)
	buildc := make(chan cache.Build)
	go func() {
		err := client.RepositoryBuilds(context.Background(), &repository, time.Hour*24*14, buildc)
		close(buildc)
		errc <- err
	}()

	builds := make([]cache.Build, 0)
	for build := range buildc {
		builds = append(builds, build)
	}

	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestTravisclient_Repository(t *testing.T) {
	token := os.Getenv("TRAVIS_API_TOKEN")
	if token == "" {
		t.Fatal("Environment variable TRAVIS_API_TOKEN is not set")
	}

	client := NewTravisClient(TravisOrgURL, TravisPusherHost, token, "travis", time.Millisecond*time.Duration(50))

	t.Run("repository found", func(t *testing.T) {
		repo, err := client.Repository(context.Background(), "https://github.com/nbedos/citop")
		if err != nil {
			t.Fatal(err)
		}
		if repo.Name != "citop" {
			t.Fatalf("invalid name: %s", repo.Name)
		}
	})

	t.Run("repository not found", func(t *testing.T) {
		_, err := client.Repository(context.Background(), "https://github.com/nbedos/citop-404")
		if err != cache.ErrRepositoryNotFound {
			t.Fatal("expected cache.ErrRepositoryNotFound")
		}
	})
}
