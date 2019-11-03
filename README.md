[![Travis Build Status](https://travis-ci.org/nbedos/citop.svg?branch=master)](https://travis-ci.org/nbedos/citop/builds) [![GitLab Build Status](https://gitlab.com/nbedos/citop/badges/master/pipeline.svg)](https://gitlab.com/nbedos/citop/pipelines)

![User Interface](citop.svg)

# citop
A UNIX program for accessing build information from continuous integration services inspired by the utility program top.
citop stands for Continous Integration Table Of Pipelines.

## Project scope
citop intends to offer the following features:
* Monitoring and management of CI builds from the command line: check build status or view job logs, cancel or restart jobs...
* Monitoring and management of builds from multiple CI providers within a single program
* Integration with VCSs: for example running citop from a Git repository should show the builds of that repository

The target audience is mostly open-source programmers so in a first time citop will integrate with the following services:
* Travis CI
* GitLab CI
* Azure Pipelines
* AppVeyor
* sourcehut builds

Integration with GitHub Actions would be nice but as far as I know the GitHub API does not expose build logs.

## Project status
citop is under active development and not yet ready for public release.

As of 2019/09/21, citop allows to 
* fetch build information for specific repositories from Travis CI and GitLab CI and store it in a SQLite database that
serves as a local cache
* display builds, stages and jobs in a textual user interface
* view job logs

## TODO
Below is a list of tasks that need to be addressed before the first public release.

| Domain | Task | Status | Comment|
|-----|------|--------|--------|
| Cache |Restrict value of state column | Not started |   |
| Cache |Deduplicate job and build_job tables | Not started |   |
| Cache |Implement maximum cache size | Not started |   |
| Providers |Travis CI integration (read only) | In progress | Mostly done, continuous update strategy still missing |
| Providers|GitLab CI integration (read only) | In progress | Mostly done, continuous update strategy still missing |
| Providers|Azure Pipelines integration (read only) | Not started |  |
| Providers|AppVeyor integration (read only) | Not started |  |
| Quality | Increase test coverage to 80-90% |Not started |  |
| UI |Implement table search function | Not started | |
| UI |Implement table filter function | Not started | |
| UI |Implement status bar | Not started | |
| UI |Implement help screen | Not started | |
| UI |Implement UI configuration | Not started | |
| Views | Define a better interface for data sources |Not started |  |
| Views | Deduplicate SQL queries |Not started |  |

