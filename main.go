package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/providers"
	"github.com/nbedos/citop/tui"
	"github.com/nbedos/citop/utils"
)

const usage = "Usage: citop [repository_URL]"

func main() {
	signal.Ignore(syscall.SIGINT)
	// FIXME Do not ignore SIGTSTP/SIGCONT
	signal.Ignore(syscall.SIGTSTP)

	var repository string
	var commit utils.Commit
	switch len(os.Args) {
	case 1:
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		repository, commit, err = utils.GitOriginURL(cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	case 2:
		repository = os.Args[1]
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	var travisToken, gitlabToken, circleCIToken, githubToken, appVeyorToken string
	tokens := map[string]*string{
		"TRAVIS_API_TOKEN":   &travisToken,
		"GITLAB_API_TOKEN":   &gitlabToken,
		"CIRCLECI_API_TOKEN": &circleCIToken,
		"GITHUB_API_TOKEN":   &githubToken,
		"APPVEYOR_API_TOKEN": &appVeyorToken,
	}
	for envVar, token := range tokens {
		if *token = os.Getenv(envVar); *token == "" {
			err := errors.New("environment variable CIRCLECI_API_TOKEN is not set")
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}

	gitlab := providers.NewGitLabClient("gitlab", gitlabToken, time.Second/10)
	CIProviders := []cache.CIProvider{
		providers.NewTravisClient("travis", travisToken, providers.TravisOrgURL, time.Second/20),
		gitlab,
		providers.NewCircleCIClient("circleci", circleCIToken, providers.CircleCIURL, time.Second/10),
		providers.NewAppVeyorClient("appveyor", appVeyorToken, time.Second/10),
	}

	SourceProviders := []cache.SourceProvider{
		providers.NewGitHubClient(context.Background(), &githubToken),
		gitlab,
	}

	ctx := context.Background()
	if err := tui.RunApplication(ctx, tcell.NewScreen, repository, commit, CIProviders, SourceProviders, time.Local); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
