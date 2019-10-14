package providers

import (
	"context"
	"github.com/nbedos/citop/cache"
	"os"
	"testing"
	"time"
)

var citopURL = "https://github.com/nbedos/citop"

func Test_RecentRepoBuilds(t *testing.T) {
	token := os.Getenv("TRAVIS_API_TOKEN")
	if token == "" {
		t.Fatal("Environment variable TRAVIS_API_TOKEN is not set")
	}

	client := NewTravisClient(TravisOrgURL, TravisPusherHost, token, "travis", time.Millisecond*time.Duration(50))

	errc := make(chan error)
	c := make(chan []cache.Inserter)
	go func() {
		err := client.RepositoryBuilds(context.Background(), citopURL, 20, 5, c)
		close(c)
		errc <- err
	}()

	inserters := make([]cache.Inserter, 0)
	for is := range c {
		inserters = append(inserters, is...)
	}

	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestTravisclient_fetchRepository(t *testing.T) {
	token := os.Getenv("TRAVIS_API_TOKEN")
	if token == "" {
		t.Fatal("Environment variable TRAVIS_API_TOKEN is not set")
	}

	client := NewTravisClient(TravisOrgURL, TravisPusherHost, token, "travis", time.Millisecond*time.Duration(50))

	t.Run("repository found", func(t *testing.T) {
		repo, err := client.fetchRepository(context.Background(), "nbedos/citop")
		if err != nil {
			t.Fatal(err)
		}
		if repo.Name != "citop" {
			t.Fatalf("invalid name: %s", repo.Name)
		}
	})

	t.Run("repository not found", func(t *testing.T) {
		_, err := client.fetchRepository(context.Background(), "nbedos/citop-404")
		switch e := err.(type) {
		case HTTPError:
			if e.Status != 404 {
				t.Fatalf("expected status 404 but got %d", e.Status)
			}
		default:
			t.Fatal("expected HTTPError")
		}
	})
}

/*
func Test_Monitor(t *testing.T) {
	token := os.Getenv("TRAVIS_API_TOKEN")
	if token == "" {
		t.Fatal("Environment variable TRAVIS_API_TOKEN is not set")
	}

	client := NewTravisClient(TravisOrgURL, TravisPusherHost, token, "travis", time.Millisecond*time.Duration(50))

	repositoryID := "25564643"

	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
	c := make(chan []cache.Inserter)
	errc := make(chan error)

	go func() {
		errc <- client.Monitor(ctx, c, repositoryID, 20, 5)
	}()

forever:
	for {
		select {
		case <-c:
			t.Log("received inserters")
		case err := <-errc:
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				break forever
			case err == nil:
				break forever
			default:
				t.Fatalf("received error %v", err)
			}
		}
	}
}*/
