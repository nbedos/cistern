% CITOP(1) | version \<version\>
% Nicolas Bedos

# NAME
**citop** – Continuous Integration Table Of Pipelines

# SYNOPSIS
`citop [-r REPOSITORY | --repository REPOSITORY] [COMMIT]`

`citop -h | --help`

`citop --version`

# DESCRIPTION
citop monitors the CI pipelines associated to a specific commit of a git repository.

citop currently integrates with the following online services. Each of the service is one or both
of the following:

* A "source provider" that is used to list the pipelines associated to a given commit of an online repository
* A "CI provider" that is used to get detailed information about CI builds

--------------------------------------------------------
Service        Source   CI      URL
-------------  -------  ------  ---------------------------
GitHub         yes      no      [https://github.com/](https://github.com/)

GitLab         yes      yes     [https://gitlab.com/](https://gitlab.com/)

AppVeyor       no       yes     [https://www.appveyor.com/](https://www.appveyor.com/)

CircleCI       no       yes     [https://circleci.com/](https://circleci.com/)

Travis CI      no       yes     [https://travis-ci.org/](https://travis-ci.org/)
                                [https://travis-ci.com/](https://travis-ci.com/)
                             
Azure Devops   no       yes     [https://dev.azure.com](https://dev.azure.com)

--------------------------------------------------------

# POSITIONAL ARGUMENTS
## `COMMIT`
Specify the commit to monitor. COMMIT is expected to be the SHA identifier of a commit, or the
name of a tag or a branch. If this option is missing citop will monitor the commit referenced by
HEAD.

Example:
```shell
# Show pipelines for commit 64be3c6
citop 64be3c6
# Show pipelines for the commit referenced by the tag '0.9.0'
citop 0.9.0
# Show pipelines for the commit at the tip of a branch
citop feature/doc
```

# OPTIONS
## `-r=REPOSITORY, --repository=REPOSITORY`
Specify the git repository to monitor. If REPOSITORY is the path of a local repository, citop
will monitor all the associated remotes. If REPOSITORY is a URL, citop will monitor the
corresponding online repository. 

If this option is not set, citop will behave as if it had been set to the path of the current
directory.

Note that citop will only monitor repositories hosted on GitLab or GitHub.

Examples:
```shell
# Monitor pipelines of the git repository in the current directory
citop
# Monitor pipelines of the repository specified by a web URL
citop -r https://gitlab.com/nbedos/citop
citop -r github.com/nbedos/citop
# Git URLs are accepted
citop -r git@github.com:nbedos/citop.git
# A path referring to a local repository is valid too
citop -r /home/user/repos/myrepo
```

## `-h, --help`
Show usage of citop

## `--version`
Print the version of citop being run

# INTERACTIVE COMMANDS
Below are the default commands for interacting with citop.

----------------------------------------------------------
Key        Action
---------  -----------------------------------------------
Up, j      Move cursor up by one line

Down, k    Move cursor down by one line

Page Up    Move cursor up by one screen

Page Down  Move cursor down by one screen

o, +       Open the fold at the cursor

O          Open the fold at the cursor and all sub-folds

c, -       Close the fold at the cursor

C          Close the fold at the cursor and all sub-folds

/          Open search prompt

Escape     Close search prompt

Enter, n   Move to the next match

N          Move to the previous match

v          View the log of the job at the cursor<sup>\[a\]</sup>

b          Open with default web browser

q          Quit

?              View manual page

----------------------------------------------------------

* <sup>\[a\]</sup>  Note that if the job is still running, the log may be incomplete.


# CONFIGURATION FILE
## Location
citop follows the XDG base directory specification \[2\] and expects to find the configuration file
at one of the following locations depending on the value of the two environment variables
`XDG_CONFIG_HOME` and `XDG_CONFIG_DIRS`:

1. `"$XDG_CONFIG_HOME/citop/citop.toml"`
2. `"$DIR/citop/citop.toml"` for every directory `DIR` in the comma-separated list `"$XDG_CONFIG_DIRS"`

If `XDG_CONFIG_HOME` (resp. `XDG_CONFIG_DIRS`) is not set, citop uses the default value
`"$HOME/.config"` (resp. `"/etc/xdg"`) instead.


## Format
citop uses a configuration file in [TOML version v0.5.0](https://github.com/toml-lang/toml/blob/master/versions/en/toml-v0.5.0.md)
format. The configuration file is made of keys grouped together in tables. The specification of
each table is given in the example below.

## Example
This example describes and uses all existing configuration options.

```toml
#### CITOP CONFIGURATION FILE ####
# This file is a complete, valid configuration file for citop
# and should be located at $XDG_CONFIG_HOME/citop/citop.toml
# 

## PROVIDERS ##
[providers]
# The 'providers' table is used to define credentials for 
# accessing online services. citop relies on two types of
# providers:
#
#    - 'source providers' are used for listing the CI pipelines
#    associated to a given commit (GitHub and GitLab are source
#    providers)
#    - 'CI providers' are used to get detailed information about
#    CI pipelines (GitLab, AppVeyor, CircleCI, Travis and Azure
#    Devops are CI providers)
#
# citop requires credentials for at least one source provider and
# one CI provider to run. Feel free to remove sections below 
# as long as this rule is met.
#
# Note that for all providers, not setting an API token or 
# setting `token = ""` will cause the provider to make
# unauthenticated API requests. 
#

### GITHUB ###
[[providers.github]]
# GitHub API token (optional, string)
#
# Note: Unauthenticated API requests are heavily rate-limited by 
# GitHub (60 requests per hour and per IP address) whereas 
# authenticated clients benefit from a rate of 5000 requests per
# hour. Providing an  API token is strongly encouraged: without
# one, citop will likely reach the rate limit in a matter of
# minutes.
#
# GitHub token management: https://github.com/settings/tokens
token = ""


### GITLAB ###
[[providers.gitlab]]
# Name shown by citop for this provider
# (optional, string, default: "gitlab")
name = "gitlab"

# GitLab instance URL (optional, string, default: "https://gitlab.com")
# (the GitLab instance must support GitLab REST API V4)
url = "https://gitlab.com"

# GitLab API token (optional, string)
#
# Note: GitLab prevents access to pipeline jobs for 
# unauthenticated users meaning if you wish to use citop
# to view GitLab pipelines you will have to provide
# appropriate credentials. This is true even for pipelines
# of public repositories.
#
# gitlab.com token management:
#     https://gitlab.com/profile/personal_access_tokens
token = ""


### TRAVIS CI ###
[[providers.travis]]
# Name shown by citop for this provider
# (optional, string, default: "travis")
name = "travis"

# URL of the Travis instance. "org" and "com" can be used as
# shorthands for the full URL of travis.org and travis.com
# (string, mandatory)
url = "org"

# API access token for the travis API (string, optional)
# Travis tokens are managed at:
#    - https://travis-ci.org/account/preferences
#    - https://travis-ci.com/account/preferences
token = ""


# Define another account for accessing travis.com
[[providers.travis]]
name = "travis"
url = "com"
token = ""


### APPVEYOR ###
[[providers.appveyor]]
# Name shown by citop for this provider
# (optional, string, default: "appveyor")
name = "appveyor"

# AppVeyor API token (optional, string)
# AppVeyor token managemement: https://ci.appveyor.com/api-keys
token = ""


### CIRCLECI ###
[[providers.circleci]]
# Name shown by citop for this provider
# (optional, string, default: "circleci")
name = "circleci"

# Circle CI API token (optional, string)
# See https://circleci.com/account/api
token = ""


### AZURE DEVOPS ###
[[providers.azure]]
# Name shown by citop for this provider
# (optional, string, default: "azure")
name = "azure"

# Azure API token (optional, string)
# Azure token management is done at https://dev.azure.com/ via
# the user settings menu
token = ""

```

# ENVIRONMENT
## ENVIRONMENT VARIABLES

* `BROWSER` is used to find the path of the default web browser
* `PAGER` is used to view log files. If the variable is not set, citop will call `less`
* `HOME`, `XDG_CONFIG_HOME` and `XDG_CONFIG_DIRS` are used to locate the configuration file

## LOCAL PROGRAMS

citop relies on the following local executables:

* `git` to translate the abbreviated SHA identifier of a commit into a non-abbreviated SHA
* `less` to view log files, unless `PAGER` is set
* `man` to show the manual page

# EXAMPLES

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

# BUGS
Questions, bug reports and feature requests are welcome and should be submitted
on [GitHub](https://github.com/nbedos/citop/issues).

# NOTES
1. **citop repository**
    * [https://github.com/nbedos/citop](https://github.com/nbedos/citop)
2. **XDG base directory specification**
    * [https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html)



