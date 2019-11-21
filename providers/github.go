package providers

import (
	"context"
	"net/http"

	"github.com/google/go-github/v28/github"
	"github.com/nbedos/citop/cache"
	"golang.org/x/oauth2"
)

type GitHubClient struct {
	client *github.Client
}

func NewGitHubClient(ctx context.Context, token *string) GitHubClient {
	var httpClient *http.Client

	if token != nil {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: *token},
		)
		httpClient = oauth2.NewClient(ctx, ts)
	}

	return GitHubClient{
		client: github.NewClient(httpClient),
	}
}

func (c GitHubClient) BuildURLs(ctx context.Context, owner string, repo string, sha string, urls chan<- string) error {
	defer close(urls)
	statuses, _, err := c.client.Repositories.ListStatuses(ctx, owner, repo, sha, nil)
	if err != nil {
		if err, ok := err.(*github.ErrorResponse); ok && err.Response.StatusCode == 404 {
			return cache.ErrRepositoryNotFound
		}
		return err
	}

	found := make(map[string]struct{})
	for _, status := range statuses {
		if status.TargetURL == nil {
			continue
		}
		if _, exists := found[*status.TargetURL]; !exists {
			select {
			case urls <- *status.TargetURL:
				found[*status.TargetURL] = struct{}{}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return nil
}
