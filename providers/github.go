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

func (c GitHubClient) BuildURLs(ctx context.Context, owner string, repo string, sha string) ([]string, error) {
	previousURLs := make(map[string]struct{})
	statuses, _, err := c.client.Repositories.ListStatuses(ctx, owner, repo, sha, nil)
	if err != nil {
		switch err := err.(type) {
		case *github.ErrorResponse:
			if err.Response.StatusCode == 404 {
				return nil, cache.ErrRepositoryNotFound
			}
		default:
			return nil, err
		}
	}

	for _, status := range statuses {
		if status.TargetURL == nil {
			continue
		}
		if _, exists := previousURLs[*status.TargetURL]; exists {
			continue
		}
	}

	urls := make([]string, 0, len(previousURLs))
	for u := range previousURLs {
		urls = append(urls, u)
	}

	return urls, nil
}
