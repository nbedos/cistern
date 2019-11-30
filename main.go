package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/providers"
	"github.com/nbedos/citop/tui"
	"github.com/nbedos/citop/utils"
	"github.com/pelletier/go-toml"
)

const usage = "Usage: citop [repository_URL]"

const ConfDir = "citop"
const ConfFilename = "citop.toml"

type ProviderConfiguration struct {
	Name              string  `toml:"name"`
	Url               string  `toml:"url"`
	Token             string  `toml:"token"`
	RequestsPerSecond float64 `toml:"max_requests_per_second"`
}

type ProvidersConfiguration struct {
	GitLab   []ProviderConfiguration
	GitHub   []ProviderConfiguration
	CircleCI []ProviderConfiguration
	Travis   []ProviderConfiguration
	AppVeyor []ProviderConfiguration
}

type Configuration struct {
	Providers ProvidersConfiguration
}

var ErrMissingConf = errors.New("missing configuration file")

func ConfigFromPaths(paths ...string) (Configuration, error) {
	var c Configuration

	for _, p := range paths {
		c = Configuration{}
		bs, err := ioutil.ReadFile(p)
		if err != nil {
			if err, ok := err.(*os.PathError); ok && err.Err == syscall.ENOENT {
				// No config file at this location, try the next one
				continue
			}
			return c, err
		}
		tree, err := toml.LoadBytes(bs)
		if err != nil {
			return c, err
		}
		err = tree.Unmarshal(&c)
		return c, err
	}

	return c, ErrMissingConf
}

func (c ProvidersConfiguration) Providers(ctx context.Context) ([]cache.SourceProvider, []cache.CIProvider, error) {
	source := make([]cache.SourceProvider, 0)
	ci := make([]cache.CIProvider, 0)

	for i, conf := range c.GitLab {
		rateLimit := time.Second / 10
		if conf.RequestsPerSecond > 0 {
			rateLimit = time.Second / time.Duration(conf.RequestsPerSecond)
		}

		id := fmt.Sprintf("gitlab-%d", i)
		name := "gitlab"
		if conf.Name != "" {
			name = conf.Name
		}
		client := providers.NewGitLabClient(id, name, conf.Token, rateLimit)
		source = append(source, client)
		ci = append(ci, client)
	}

	for _, conf := range c.GitHub {
		client := providers.NewGitHubClient(ctx, &conf.Token)
		source = append(source, client)
	}

	for i, conf := range c.CircleCI {
		rateLimit := time.Second / 10
		if conf.RequestsPerSecond > 0 {
			rateLimit = time.Second / time.Duration(conf.RequestsPerSecond)
		}
		id := fmt.Sprintf("circleci-%d", i)
		name := "circleci"
		if conf.Name != "" {
			name = conf.Name
		}
		client := providers.NewCircleCIClient(id, name, conf.Token, providers.CircleCIURL, rateLimit)
		ci = append(ci, client)
	}

	for i, conf := range c.AppVeyor {
		rateLimit := time.Second / 10
		if conf.RequestsPerSecond > 0 {
			rateLimit = time.Second / time.Duration(conf.RequestsPerSecond)
		}
		id := fmt.Sprintf("appveyor-%d", i)
		name := "appveyor"
		if conf.Name != "" {
			name = conf.Name
		}
		client := providers.NewAppVeyorClient(id, name, conf.Token, rateLimit)
		ci = append(ci, client)
	}

	for i, conf := range c.Travis {
		rateLimit := time.Second / 20
		if conf.RequestsPerSecond > 0 {
			rateLimit = time.Second / time.Duration(conf.RequestsPerSecond)
		}
		id := fmt.Sprintf("travis-%d", i)
		var err error
		var u *url.URL
		switch strings.ToLower(conf.Url) {
		case "org":
			u = &providers.TravisOrgURL
		case "com":
			u = &providers.TravisComURL
		default:
			u, err = url.Parse(conf.Url)
			if err != nil {
				return nil, nil, err
			}
		}

		name := "travis"
		if conf.Name != "" {
			name = conf.Name
		}
		client := providers.NewTravisClient(id, name, conf.Token, *u, rateLimit)
		ci = append(ci, client)
	}

	return source, ci, nil
}

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

	paths := utils.XDGConfigLocations(path.Join(ConfDir, ConfFilename))

	config, err := ConfigFromPaths(paths...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	ctx := context.Background()

	sourceProviders, ciProviders, err := config.Providers.Providers(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if err := tui.RunApplication(ctx, tcell.NewScreen, repository, commit, ciProviders, sourceProviders, time.Local); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
