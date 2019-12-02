package providers

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/go-github/v28/github"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
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

func (c GitHubClient) Commit(ctx context.Context, owner string, repo string, sha string) (utils.Commit, error) {
	repoCommit, _, err := c.client.Repositories.GetCommit(ctx, owner, repo, sha)
	if err != nil {
		return utils.Commit{}, err
	}

	githubCommit := repoCommit.Commit
	commit := utils.Commit{
		Sha:     repoCommit.GetSHA(),
		Author:  fmt.Sprintf("%s <%s>", githubCommit.GetAuthor().GetName(), githubCommit.GetAuthor().GetEmail()),
		Date:    githubCommit.GetAuthor().GetDate(),
		Message: githubCommit.GetMessage(),
	}

	branches, _, err := c.client.Repositories.ListBranchesHeadCommit(ctx, owner, repo, commit.Sha)
	if err != nil {
		return utils.Commit{}, err
	}
	for _, branch := range branches {
		commit.Branches = append(commit.Branches, branch.GetName())
	}

	opt := github.ListOptions{}
	for {
		tags, resp, err := c.client.Repositories.ListTags(ctx, owner, repo, &opt)
		if err != nil {
			return utils.Commit{}, err
		}

		for _, tag := range tags {
			if tag.GetCommit().GetSHA() == commit.Sha {
				commit.Tags = append(commit.Branches, tag.GetName())
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return commit, nil
}

func (c GitHubClient) BuildURLs(ctx context.Context, owner string, repo string, sha string) ([]string, error) {
	errc := make(chan error)

	previousURLs := make(map[string]struct{})
	mux := sync.Mutex{}

	go func() {
		opt := github.ListOptions{}
		for {
			statuses, resp, err := c.client.Repositories.ListStatuses(ctx, owner, repo, sha, &opt)
			if err != nil {
				errc <- err
				return
			}
			for _, status := range statuses {
				if status.TargetURL == nil {
					continue
				}
				mux.Lock()
				previousURLs[*status.TargetURL] = struct{}{}
				mux.Unlock()
			}

			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
		errc <- nil
	}()

	go func() {
		opt := github.ListCheckRunsOptions{}
		for {
			runs, resp, err := c.client.Checks.ListCheckRunsForRef(ctx, owner, repo, sha, &opt)
			if err != nil {
				errc <- err
				return
			}

			for _, run := range runs.CheckRuns {
				if run == nil || run.DetailsURL == nil {
					continue
				}
				mux.Lock()
				previousURLs[*run.DetailsURL] = struct{}{}
				mux.Unlock()
			}

			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
		errc <- nil
	}()

	var err error
	for i := 0; i < 2; i++ {
		if e := <-errc; err == nil {
			switch errResp := e.(type) {
			case *github.ErrorResponse:
				switch errResp.Response.StatusCode {
				case 404:
					e = cache.ErrRepositoryNotFound
				case 422:
					// Do not fail if the remote has no knowledge of a commit associated to the
					// specified SHA, simply return an empty url list
					e = nil
				}
			}
			err = e
		}
	}

	urls := make([]string, 0, len(previousURLs))
	for u := range previousURLs {
		urls = append(urls, u)
	}

	return urls, err
}
