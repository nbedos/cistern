package providers

import (
	"context"
	"os"
	"testing"
)

func TestClient(t *testing.T) {
	var token *string
	if env := os.Getenv("GITHUB_API_TOKEN"); env != "" {
		token = &env
	}
	client := NewGitHubClient(context.Background(), token)
	owner := "nbedos"
	repo := "termtosvg"
	sha := "d58600a58bf1738c6529ce3489a546bfa2178e07"
	ctx := context.Background()

	urls := make(chan string)
	errc := make(chan error)
	go func() {
		errc <- client.BuildURLs(ctx, owner, repo, sha, urls)
		close(errc)
	}()

	for url := range urls {
		t.Log(url)
	}

	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}
