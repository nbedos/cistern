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
	switch len(os.Args) {
	case 1:
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		repository, err = utils.GitOriginURL(cwd)
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

	var travisToken, gitlabToken, circleCIToken string
	tokens := map[string]*string{
		"TRAVIS_API_TOKEN":   &travisToken,
		"GITLAB_API_TOKEN":   &gitlabToken,
		"CIRCLECI_API_TOKEN": &circleCIToken,
	}
	for envVar, token := range tokens {
		if *token = os.Getenv(envVar); *token == "" {
			err := errors.New("environment variable CIRCLECI_API_TOKEN is not set")
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}

	CIProviders := []cache.Provider{
		providers.NewTravisClient(
			providers.TravisOrgURL,
			travisToken,
			"travis",
			50*time.Millisecond),

		providers.NewGitLabClient(
			"gitlab",
			gitlabToken,
			100*time.Millisecond),

		providers.NewCircleCIClient(
			providers.CircleCIURL,
			"circleci",
			circleCIToken,
			100*time.Millisecond),
	}

	ctx := context.Background()
	if err := tui.RunApplication(ctx, tcell.NewScreen, repository, CIProviders, time.Local); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
