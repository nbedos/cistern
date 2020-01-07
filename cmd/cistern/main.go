package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path"

	"github.com/gdamore/tcell"
	"github.com/nbedos/cistern/utils"
)

var Version = "undefined"

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

const warningNoConfigFileFormat = `warning: No configuration file found at %s, using default configuration without credentials.
Please note that:
    - cistern will likely reach the rate limit of the GitHub API for unauthenticated clients in a few minutes
    - cistern will not be able to access pipeline jobs on GitLab without an API access token
	
To lift these restrictions, create a configuration file containing your credentials at the aforementioned location.
`

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
		fmt.Fprintf(os.Stderr, warningNoConfigFileFormat, paths[0])
	default:
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	ctx := context.Background()
	if err := RunApplication(ctx, tcell.NewScreen, repo, sha, config); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
