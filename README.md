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

## Integration with Travis CI, AppVeyor, CircleCI and GitLab CI
For repositories that rely on multiple CI providers this allows monitoring pipelines of multiple
providers in a single place.

## Quick access to pipeline web pages
Just select the pipeline, stage or job you're interested in and press 'b' to open the corresponding
web page on the CI provider's website. This gives easy access to features offered by CI providers
that are not implemented by citop (pipeline cancellation, artifact download...)

# Building citop
## Building automatically from source
This method requires a UNIX system with `make`, `pandoc` and golang >= 1.12.
```shell
git clone git@github.com:nbedos/citop.git && cd citop
make citop
# Test newly built executable
./build/citop
```

## Building manually from source
This method requires only golang >= 1.12 and a UNIX system.
```shell
git clone git@github.com:nbedos/citop.git && cd citop
mkdir build
BUILD_VERSION="$(git describe --tags --long --dirty)_$(go env GOOS)/$(go env GOARCH)" && \
go build -ldflags "-X main.Version=$BUILD_VERSION" -o build/citop
# Test newly built executable
./build/citop
```

Note that this method ignores eventual changes made to the file `man.md`. It is provided for
users that do not wish to install `pandoc` on their system.

## Building a Docker image
This method requires access to a Docker instance
```shell
git clone git@github.com:nbedos/citop.git && cd citop
export CITOP_DOCKER_IMAGE="citop:$(git describe --tags --long --dirty)"
docker build -t "$CITOP_DOCKER_IMAGE" .
docker run -it "$CITOP_DOCKER_IMAGE"
```

# Configuration
citop requires access to various APIs. The corresponding credentials should be stored in a
configuration file as described in the [manual page](https://nbedos.github.io/citop/citop.man).

If the configuration file is missing, citop will still work but with the following limitations:
* citop will likely reach the rate limit of the GitHub API for unauthenticated clients in a few minutes
* citop will not be able to access pipeline jobs on GitLab without an API access token
    
In most cases this should still be enough for a quick test of the application without having to
bother with personal access tokens.

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
```shell
# Show pipelines associated to the last commit of the current git repository 
citop

# Show pipelines associated to a specific commit, tag or branch of the
# current git repository 
citop 64be3c6
citop 0.9.0
citop feature/doc

# Show pipelines of a repository identified by a URL
citop -r https://gitlab.com/nbedos/citop
citop -r git@github.com:nbedos/citop.git
citop -r github.com/nbedos/citop

# Specify both repository and git reference
citop -r github.com/nbedos/citop master
```

More information is available in the [manual page](https://nbedos.github.io/citop/citop.man).


## Support
Questions, bug reports and feature requests are welcome and should be submitted
[here](https://github.com/nbedos/citop/issues).

## Contributing
Pull requests are welcome. If you foresee that a PR will take any significant amount of your time,
you probably want to open an issue first to discuss your changes and make sure they are
likely to be accepted.
