package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/google/go-github/v28/github"
	"github.com/nbedos/cistern/cache"
	"github.com/nbedos/cistern/utils"
	"golang.org/x/oauth2"
)

type GitHubClient struct {
	id     string
	client *github.Client
}

func NewGitHubClient(ctx context.Context, id string, token *string) GitHubClient {
	var httpClient *http.Client

	if token != nil && *token != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: *token},
		)
		httpClient = oauth2.NewClient(ctx, ts)
	}

	return GitHubClient{
		id:     id,
		client: github.NewClient(httpClient),
	}
}

func (c GitHubClient) ID() string {
	return c.id
}

func (c GitHubClient) parseRepositoryURL(url string) (string, string, error) {
	host, owner, repo, err := utils.RepoHostOwnerAndName(url)
	expectedHost := strings.TrimPrefix(c.client.BaseURL.Hostname(), "api.")
	if err != nil || !strings.Contains(host, expectedHost) {
		return "", "", cache.ErrUnknownRepositoryURL
	}

	return owner, repo, nil
}

func (c GitHubClient) Commit(ctx context.Context, repo string, ref string) (cache.Commit, error) {
	owner, repo, err := c.parseRepositoryURL(repo)
	if err != nil {
		return cache.Commit{}, cache.ErrUnknownRepositoryURL
	}

	owner = url.PathEscape(owner)
	repo = url.PathEscape(repo)
	ref = url.PathEscape(ref)

	repoCommit, _, err := c.client.Repositories.GetCommit(ctx, owner, repo, ref)
	if err != nil {
		if e, ok := err.(*github.ErrorResponse); ok {
			switch e.Response.StatusCode {
			case 404:
				err = cache.ErrUnknownRepositoryURL
			case 422:
				err = cache.ErrUnknownGitReference
			}
		}
		return cache.Commit{}, err
	}

	githubCommit := repoCommit.Commit
	commit := cache.Commit{
		Sha:     repoCommit.GetSHA(),
		Author:  fmt.Sprintf("%s <%s>", githubCommit.GetAuthor().GetName(), githubCommit.GetAuthor().GetEmail()),
		Date:    githubCommit.GetAuthor().GetDate(),
		Message: githubCommit.GetMessage(),
	}

	branches, _, err := c.client.Repositories.ListBranchesHeadCommit(ctx, owner, repo, commit.Sha)
	if err != nil {
		return cache.Commit{}, err
	}
	for _, branch := range branches {
		commit.Branches = append(commit.Branches, branch.GetName())
	}

	opt := github.ListOptions{}
	for {
		tags, resp, err := c.client.Repositories.ListTags(ctx, owner, repo, &opt)
		if err != nil {
			return cache.Commit{}, err
		}

		for _, tag := range tags {
			if tag.GetCommit().GetSHA() == commit.Sha {
				commit.Tags = append(commit.Tags, tag.GetName())
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return commit, nil
}

func (c GitHubClient) RefStatuses(ctx context.Context, u string, ref string, sha string) ([]string, error) {
	owner, repo, err := c.parseRepositoryURL(u)
	if err != nil {
		return nil, err
	}

	if sha != "" {
		ref = sha
	}

	owner = url.PathEscape(owner)
	repo = url.PathEscape(repo)
	ref = url.PathEscape(ref)

	errc := make(chan error)
	previousURLs := make(map[string]struct{})
	mux := sync.Mutex{}

	go func() {
		opt := github.ListOptions{}
		for {
			statuses, resp, err := c.client.Repositories.ListStatuses(ctx, owner, repo, ref, &opt)
			if err != nil {
				errc <- err
				return
			}
			for _, status := range statuses {
				if status.TargetURL == nil || *status.TargetURL == "" {
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
			runs, resp, err := c.client.Checks.ListCheckRunsForRef(ctx, owner, repo, ref, &opt)
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

	for i := 0; i < 2; i++ {
		if e := <-errc; err == nil {
			switch errResp := e.(type) {
			case *github.ErrorResponse:
				switch errResp.Response.StatusCode {
				case 404:
					e = cache.ErrUnknownRepositoryURL
				case 422:
					// Do not fail if the remote has no knowledge of a commit associated to the
					// specified SHA, simply return an empty url list
					// FIXME We want to report this and let the caller decide
					//  whether they want to inform the user
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
