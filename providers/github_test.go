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

	urls, err := client.BuildURLs(context.Background(), owner, repo, sha)
	if err != nil {
		t.Fatal(err)
	}

	for _, u := range urls {
		t.Log(u)
	}
}
