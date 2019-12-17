[![Travis Build Status](https://travis-ci.org/nbedos/citop.svg?branch=master)](https://travis-ci.org/nbedos/citop/builds)

# citop
A UNIX program that displays information about pipelines of Continuous
Integration services. citop stands for Continous Integration Table Of Pipelines.

![User Interface](citop.svg)

[Animated demo \[SVG, 290kB\]](https://nbedos.github.io/citop/demo.svg)

# Project status
citop is under active development and not yet ready for public release.

Integration with a few CI providers has been implemented and is usable.

The remaining steps before a first alpha release are:
* Implementing application configuration (essentially for credentials in a first time)
* Improving quality by adding tests

# Features
## Monitor pipelines of GitHub or GitLab repository 
This is as simple as running `git push && citop` to monitor the pipelines triggered by your last
`push` or running `citop <commit>` to monitor a specific commit. citop will show the status, timings
and logs of pipelines, stages and jobs.

## Integration with Travis CI, AppVeyor, CircleCI, GitLab CI and Azure DevOps
For repositories that rely on multiple CI providers this allows monitoring pipelines of multiple
providers in a single place.

## Quick access to pipeline web pages
Just select the pipeline, stage or job you're interested in and press 'b' to open the corresponding
web page on the CI provider's website. This gives easy access to features offered by CI providers
that are not implemented by citop (pipeline cancellation, artifact download...)


# Installation
## Building citop from source
### Building automatically from source (recommended)
This method requires a UNIX system with `make`, `pandoc` and golang >= 1.11.
```shell
git clone git@github.com:nbedos/citop.git && cd citop
make citop
```

At this point you should find the executable located at `./build/citop` as well as two versions
of the manual page:
* `./build/citop.man.1` (roff format)
* `./build/citop.man.html`

### Building manually from source
This method requires golang >= 1.11 and a UNIX system.
```shell
git clone git@github.com:nbedos/citop.git && cd citop
mkdir build
BUILD_VERSION="$(git describe --tags --dirty)_$(go env GOOS)/$(go env GOARCH)" && \
GO111MODULE=on && \
go build -ldflags "-X main.Version=$BUILD_VERSION" -o build/citop
```

At this point you should find the executable located at `./build/citop`.

Note that this method ignores eventual changes made to the file `man.md` and won't build the manual
page. It is provided for users that do not wish to install `pandoc` on their system. In any case
the manual page is made available for each [release](https://github.com/nbedos/citop/releases). 

### Building a Docker image
This method requires access to Docker 17.05 or higher since it relies on a multi-stage build.
```shell
git clone git@github.com:nbedos/citop.git && cd citop
export CITOP_DOCKER_IMAGE="citop:$(git describe --tags --dirty)"
docker build -t "$CITOP_DOCKER_IMAGE" .

# Mount a local repository as a volume mapped to `/citop` to monitor its pipelines 
docker run -it -v "$PWD:/citop" "$CITOP_DOCKER_IMAGE"

# Monitor non local repositories by specifying the URL:
docker run -it "$CITOP_DOCKER_IMAGE" -r github.com/nbedos/citop
```

## Binary releases
Binary releases are made available for each version of citop 
[here](https://github.com/nbedos/citop/releases).

Each release archive contains a statically linked executable named `citop`, the manual page
in HTML and roff format and a copy of the license. 

# Configuration
citop requires access to various APIs. The corresponding credentials should be stored in a
configuration file as described in the [manual page](https://nbedos.github.io/citop/citop.man).

If the configuration file is missing, citop will run with the following limitations:
* citop will likely reach the [rate limit of the GitHub API](https://developer.github.com/v3/#rate-limiting)
for unauthenticated clients in a few minutes
* citop will not be able to access pipeline jobs on GitLab without an API access token
    
In most cases running without a configuration file should still be enough for a quick test
of the application without having to bother with personal access tokens.

# Usage
```
usage: citop [-r REPOSITORY | --repository REPOSITORY] [COMMIT]
       citop -h | --help
       citop --version

Monitor CI pipelines associated to a specific commit of a git repository

Positional arguments:
  COMMIT        Specify the commit to monitor. COMMIT is expected to be
                the SHA identifier of a commit, or the name of a tag or
                a branch. If this option is missing citop will monitor
                the commit referenced by HEAD.

Options:
  -r REPOSITORY, --repository REPOSITORY
                Specify the git repository to work with. REPOSITORY can
                be either a path to a local git repository, or the URL
                of an online repository hosted at GitHub or GitLab.
                Both web URLs and git URLs are accepted.

                In the absence of this option, citop will work with the
                git repository located in the current directory. If
                there is no such repository, citop will fail.

  -h, --help    Show usage

  --version     Print the version of citop being run
```

## Examples
Monitor pipelines of the current git repository
```shell
# Move to a directory containing a git repository of your choosing
git clone git@github.com:nbedos/citop.git && cd citop
# Run citop to list the pipelines associated to the last commit of the repository 
citop

# Show pipelines associated to a specific commit, tag or branch
citop a24840c
citop 0.1.0
citop master
```

Monitor pipelines of other repositories
```shell
# Show pipelines of a repository identified by a URL or path
citop -r https://gitlab.com/nbedos/citop        # Web URL
citop -r git@github.com:nbedos/citop.git        # Git URL
citop -r github.com/nbedos/citop                # URL without scheme
citop -r /home/user/repos/repo                  # Path to a repository

# Specify both repository and git reference
citop -r github.com/nbedos/citop master
```

More information is available in the [manual page](https://nbedos.github.io/citop/citop.man).


## Support
Questions, bug reports and feature requests are welcome and should be submitted as
[issues](https://github.com/nbedos/citop/issues).

## Contributing
Pull requests are welcome. If you foresee that a PR will take any significant amount of your time,
you probably want to open an issue first to discuss your changes and make sure they are
likely to be accepted.
