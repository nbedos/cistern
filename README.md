[![Travis Build Status](https://travis-ci.org/nbedos/citop.svg?branch=master)](https://travis-ci.org/nbedos/citop/builds)

# citop
A UNIX program that displays information about pipelines of continuous
integration services. citop stands for Continous Integration Table Of Pipelines.

![User Interface](citop.svg)

[Live demo \[SVG, 290kB\]](https://nbedos.github.io/citop/demo.svg)


# Project status
citop is under active development and not yet ready for public release.

Integration with a few CI providers has been implemented and is usable.

The remaining steps before a first alpha release are:
* Implementing the SourceProvider interface for GitLab
* Allowing application configuration (essentially for credentials in a first time)
* Improving quality by adding tests
 
# Use cases
The first use case I aim to address with citop is **following the status of CI pipelines
from the command line**. I've always found it cumbersome to go look for a pipeline on the website
of a CI provider after running `git push`. I'd much rather have a terminal application that defaults
to listing the builds associated to git HEAD since it's almost always what I need.

Another use case that I'd like to address is **monitoring pipelines from multiple providers in a
single location**. Most CI providers have free plans for free software. Relying on multiple providers
is made easier when all pipelines can be monitored with a single application.      

# Features
* Display builds associated to git HEAD
* Update build status in quasi real time
* Show job log in `$PAGER`
* Integrates with travis CI, AppVeyor, CircleCI and GitLab 

# Installation
Install from source by running the following commands. Requires golang >= 1.12.
```shell
$ git clone git@github.com:nbedos/citop.git
$ go build .
``` 

# Configuration
For now configuration is done via environment variables. Set the following variables with
a valid API token.

* `GITHUB_API_TOKEN`
* `TRAVIS_API_TOKEN`
* `GITLAB_API_TOKEN`
* `CIRCLECI_API_TOKEN`
* `APPVEYOR_API_TOKEN`

GITHUB_API_TOKEN is mandatory and at least another variable must be set.

# Usage
Running `citop` in a directory containing a git repository will show pipelines associated to git HEAD

See the [manual page](./man/citop.md) for additional information.
 

