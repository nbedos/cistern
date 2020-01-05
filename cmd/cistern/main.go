package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/cistern/cache"
	"github.com/nbedos/cistern/providers"
	"github.com/nbedos/cistern/utils"
	"github.com/pelletier/go-toml"
)

var Version = "undefined"

const ConfDir = "cistern"
const ConfFilename = "cistern.toml"
const defaultConfiguration = `
[[providers.github]]

[[providers.gitlab]]

[[providers.travis]]
url = "org"
token = ""

[[providers.travis]]
url = "com"
token = ""

[[providers.appveyor]]

[[providers.circleci]]

[[providers.azure]]

`

type PipelinesConfiguration struct {
	Columns []string `toml:"name"`
	Sort    string   `toml:"string"`
	Depth   int      `toml:"depth"`
}

type ProvidersConfiguration struct {
	GitLab []struct {
		Name              string  `toml:"name"`
		URL               string  `toml:"url"`
		Token             string  `toml:"token"`
		RequestsPerSecond float64 `toml:"max_requests_per_second"`
	}
	GitHub []struct {
		Token string `toml:"token"`
	}
	CircleCI []struct {
		Name              string  `toml:"name"`
		Token             string  `toml:"token"`
		RequestsPerSecond float64 `toml:"max_requests_per_second"`
	}
	Travis []struct {
		Name              string  `toml:"name"`
		URL               string  `toml:"url"`
		Token             string  `toml:"token"`
		RequestsPerSecond float64 `toml:"max_requests_per_second"`
	}
	AppVeyor []struct {
		Name              string  `toml:"name"`
		Token             string  `toml:"token"`
		RequestsPerSecond float64 `toml:"max_requests_per_second"`
	}
	Azure []struct {
		Name              string  `toml:"name"`
		Token             string  `toml:"token"`
		RequestsPerSecond float64 `toml:"max_requests_per_second"`
	}
}

func (c ProvidersConfiguration) Providers(ctx context.Context) ([]cache.SourceProvider, []cache.CIProvider, error) {
	source := make([]cache.SourceProvider, 0)
	ci := make([]cache.CIProvider, 0)

	for i, conf := range c.GitLab {
		id := fmt.Sprintf("gitlab-%d", i)
		client, err := providers.NewGitLabClient(id, conf.Name, conf.URL, conf.Token, conf.RequestsPerSecond)
		if err != nil {
			return nil, nil, err
		}
		source = append(source, client)
		ci = append(ci, client)
	}

	for i, conf := range c.GitHub {
		id := fmt.Sprintf("github-%d", i)
		client := providers.NewGitHubClient(ctx, id, &conf.Token)
		source = append(source, client)
	}

	for i, conf := range c.CircleCI {
		id := fmt.Sprintf("circleci-%d", i)
		client := providers.NewCircleCIClient(id, conf.Name, conf.Token, conf.RequestsPerSecond)
		ci = append(ci, client)
	}

	for i, conf := range c.AppVeyor {
		id := fmt.Sprintf("appveyor-%d", i)
		client := providers.NewAppVeyorClient(id, conf.Name, conf.Token, conf.RequestsPerSecond)
		ci = append(ci, client)
	}

	for i, conf := range c.Travis {
		id := fmt.Sprintf("travis-%d", i)
		client, err := providers.NewTravisClient(id, conf.Name, conf.Token, conf.URL, conf.RequestsPerSecond)
		if err != nil {
			return nil, nil, err
		}
		ci = append(ci, client)
	}

	for i, conf := range c.Azure {
		id := fmt.Sprintf("azure-%d", i)
		client := providers.NewAzurePipelinesClient(id, conf.Name, conf.Token, conf.RequestsPerSecond)
		ci = append(ci, client)
	}
	return source, ci, nil
}

type Configuration struct {
	Pipelines PipelinesConfiguration
	Providers ProvidersConfiguration
}

var ErrMissingConf = errors.New("missing configuration file")

func ConfigFromPaths(paths ...string) (Configuration, error) {
	var c Configuration

	for _, p := range paths {
		c = Configuration{}
		bs, err := ioutil.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
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

	tree, err := toml.LoadBytes([]byte(defaultConfiguration))
	if err != nil {
		return c, err
	}
	if err := tree.Unmarshal(&c); err != nil {
		return c, err
	}

	return c, ErrMissingConf
}

const usage = `usage: cistern [-r REPOSITORY | --repository REPOSITORY] [COMMIT]
       cistern -h | --help
       cistern --version

Monitor CI pipelines associated to a specific commit of a git repository

Positional arguments:
  COMMIT        Specify the commit to monitor. COMMIT is expected to be
                the SHA identifier of a commit, or the name of a tag or
                a branch. If this option is missing cistern will monitor
                the commit referenced by HEAD.

Options:
  -r REPOSITORY, --repository REPOSITORY
                Specify the git repository to monitor. If REPOSITORY is
                the path of a local repository, cistern will monitor all
                the associated remotes. If REPOSITORY is a url, cistern
                will monitor the corresponding online repository.
                If this option is not set, cistern will behave as if it
                had been set to the path of the current directory.
                Note that cistern will only monitor repositories hosted
                on GitLab or GitHub.

  -h, --help    Show usage

  --version     Print the version of cistern being run`

func main() {
	SetupSignalHandlers()

	f := flag.NewFlagSet("cistern", flag.ContinueOnError)
	null := bytes.NewBuffer(nil)
	f.SetOutput(null)

	defaultCommit := "HEAD"
	defaultRepository, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		os.Exit(1)
	}
	versionFlag := f.Bool("version", false, "")
	helpFlagShort := f.Bool("h", false, "")
	helpFlag := f.Bool("help", false, "")
	repoFlag := f.String("repository", defaultRepository, "")
	repoFlagShort := f.String("r", defaultRepository, "")

	if err := f.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	if *versionFlag {
		fmt.Fprintf(os.Stderr, "cistern %s\n", Version)
		os.Exit(0)
	}

	if *helpFlag || *helpFlagShort {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(0)
	}

	sha := defaultCommit
	if commits := f.Args(); len(commits) == 1 {
		sha = commits[0]
	} else if len(commits) > 1 {
		fmt.Fprintln(os.Stderr, "Error: at most one commit can be specified")
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	repo := *repoFlag
	if repo == defaultRepository {
		repo = *repoFlagShort
	}

	paths := utils.XDGConfigLocations(path.Join(ConfDir, ConfFilename))
	config, err := ConfigFromPaths(paths...)
	switch err {
	case nil:
		for _, g := range config.Providers.GitLab {
			if g.Token == "" {
				fmt.Fprintln(os.Stderr, "warning: cistern will not be able to access pipeline jobs on GitLab without an API access token")
				break
			}
		}
	case ErrMissingConf:
		msgFormat := `warning: No configuration file found at %s, using default configuration without credentials.
Please note that:
    - cistern will likely reach the rate limit of the GitHub API for unauthenticated clients in a few minutes
    - cistern will not be able to access pipeline jobs on GitLab without an API access token
	
To lift these restrictions, create a configuration file containing your credentials at the aforementioned location.
`
		fmt.Fprintf(os.Stderr, msgFormat, paths[0])
	default:
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	ctx := context.Background()
	sourceProviders, ciProviders, err := config.Providers.Providers(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Sprintf("configuration error: %s", err.Error()))
		os.Exit(1)
	}
	if err := RunApplication(ctx, tcell.NewScreen, repo, sha, ciProviders, sourceProviders, time.Local, manualPage); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
