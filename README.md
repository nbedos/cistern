# cistern
A top-like utility for Unix to monitor Continuous Integration pipelines from
the command line. Current integrations include GitLab, Azure DevOps, Travis CI,
AppVeyor and CircleCI. Think of `cistern` as the receptacle that holds the
results of your CI pipelines.  `cistern` stands for **C**ontinous
**I**ntegration **S**ervices **Ter**minal for U**n**ix.

![Animated demonstration](demo.svg)

# Project status
`cistern` is currently in the initial development phase. Anything may change at any time and the API
should not be considered stable.

# Features

* **List pipelines associated to a commit of a GitHub or GitLab repository**: pipelines are shown in
a tree view where expanding a pipeline will reveal its stages, jobs and tasks
* **Monitor status changes in quasi real time and view job logs** 
* **Integration with Travis CI, AppVeyor, CircleCI, GitLab CI and Azure DevOps**: `cistern` is
targeted at open source developers

# Limitations

* **Starting, restarting or canceling a pipeline is not possible for now** ([issue #14](https://github.com/nbedos/cistern/issues/14))
* **Compatibility is restricted to Unix systems**: all dependencies and the majority of the code base
should work on Windows, but there are still a few Unixisms here and there.
* **No integration with GitHub Actions**: GitHub does not currently allow access to action logs
via their API
* **Integrations are limited to CI providers**: Integration with Netlify, code coverage services, etc
is currently considered out of scope 
* **Git is the only version-control system supported**


# Installation
## Binary releases
Binary releases are made available for each version of `cistern` 
[here](https://github.com/nbedos/cistern/releases).

Each release archive contains a statically linked executable named `cistern`, the manual page
in HTML and groff man format, a copy of the license and a sample configuration file.

## Building from source
### Building automatically from source (recommended)
This method requires a UNIX system with golang >= 1.11, git and [pandoc](https://pandoc.org/installing.html).
```shell
git clone git@github.com:nbedos/cistern.git
cd cistern
# Compile and run build script
export GO111MODULE=on
go run ./cmd/make cistern
```

If all went well you should find the following content in the `./build/` directory: 

Filename           | Content
------------------ | ---
`cistern`          | statically linked executable
`cistern.man.1`    | manual page (groff man format)
`cistern.man.html` | manual page (HTML format)
`cistern.toml`     | sample configuration file
`LICENSE`          | copy of the license

### Building manually from source
This method is provided for users that do not wish to install [pandoc](https://pandoc.org/installing.html)
on their system. It requires a UNIX system with golang >= 1.11 and git.
```shell
git clone git@github.com:nbedos/cistern.git
cd cistern
mkdir build
export CISTERN_VERSION="$(git describe --tags --dirty)_$(go env GOOS)/$(go env GOARCH)"
export GO111MODULE=on
go build -ldflags "-X main.Version=$CISTERN_VERSION" -o build/cistern ./cmd/cistern
```

At this point you should find the executable located at `./build/cistern`.



### Building a Docker image
This method requires access to Docker 17.05 or higher since it relies on a multi-stage build.
```shell
git clone git@github.com:nbedos/cistern.git
cd cistern
export CISTERN_DOCKER_IMAGE="cistern:$(git describe --tags --dirty)"
docker build -t "$CISTERN_DOCKER_IMAGE" .

# Mount a local repository as a volume mapped to `/cistern` to monitor its pipelines 
docker run -it -v "$PWD:/cistern" "$CISTERN_DOCKER_IMAGE"

# Monitor a non local repository by specifying a URL:
docker run -it "$CISTERN_DOCKER_IMAGE" -r github.com/nbedos/cistern
```

# Configuration
`cistern` requires access to various APIs and the corresponding credentials should be stored in a
configuration file. A minimal example is given in the [manual page](https://nbedos.github.io/cistern/cistern.man)
and a full example (which is also included in every release archive) is available
[here](https://github.com/nbedos/cistern/blob/master/cmd/cistern/cistern.toml).

If the configuration file is missing, `cistern` will run with the following limitations:
* `cistern` will likely reach the [rate limit of the GitHub API](https://developer.github.com/v3/#rate-limiting)
for unauthenticated clients in a few minutes
* `cistern` will not be able to access pipeline jobs on GitLab without an API access token
    
In most cases running without a configuration file should still work well enough for quickly
testing the application without having to bother with personal access tokens.

# Usage
See the [manual page](https://nbedos.github.io/cistern/cistern.man) for more detailed information.

```
usage: cistern [-r REPOSITORY | --repository REPOSITORY] [COMMIT]
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
                the associated remotes. If REPOSITORY is a URL, cistern
                will monitor the corresponding online repository.
                If this option is not set, cistern will behave as if it
                had been set to the path of the current directory.
                Note that cistern will only monitor repositories hosted
                on GitLab or GitHub.

  -h, --help    Show usage

  --version     Print the version of cistern being run
```

## Examples
Monitor pipelines of the current git repository
```shell
# Move to a directory containing a git repository of your choosing
git clone git@github.com:nbedos/cistern.git && cd cistern
# Run cistern to list the pipelines associated to the last commit of the repository 
cistern

# Show pipelines associated to a specific commit, tag or branch
cistern a24840c
cistern 0.1.0
cistern master
```

Monitor pipelines of other repositories
```shell
# Show pipelines of a repository identified by a URL or path
cistern -r https://gitlab.com/nbedos/cistern        # Web URL
cistern -r git@github.com:nbedos/cistern.git        # Git URL
cistern -r github.com/nbedos/cistern                # URL without scheme
cistern -r /home/user/repos/repo                    # Path to a repository

# Specify both repository and git reference
cistern -r github.com/nbedos/cistern master
```

## Support
Questions, bug reports and feature requests are welcome and should be submitted as
[issues](https://github.com/nbedos/cistern/issues).


## Contributing
Pull requests are welcome. If you foresee that a PR will take any significant amount of your time,
you probably want to open an issue first to discuss your changes and make sure they are
likely to be accepted.
