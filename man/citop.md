% CITOP(1)
% Nicolas Bedos
% November 2019

# NAME
citop – Continuous Integration Table Of Pipelines

# SYNOPSIS
**citop**

# DESCRIPTION
citop lists the most recent CI pipelines associated to the current Git repository.

# INTERACTIVE COMMANDS
## Up, j (resp. Down, k)
Move cursor up (resp. down) by one line

## Page Up (resp. Page Down)
Move cursor up (resp. down) by one screen

## o, + (resp. O)
Open (resp. open recursively) the fold at the cursor

## c, - (resp. C)
Close (resp. close recursively) the fold at the cursor

## /
Show search prompt. The prompt may be closed with Enter or Escape.

## Enter, n (resp. N)
Move to the next (resp. previous) match

## v
View the log of the job at the cursor. The log may be incomplete if
the job is still running.

## b
Open web page associated to the current row with the default web browser

## q
Quit

## ?
View manual page

# CONFIGURATION FILE
## Location
citop follows the XDG base directory specification and expects to find the configuration file
at one of the following locations:

1. `$XDG_CONFIG_HOME/citop/citop.toml`
2. `$HOME/.config/citop/citop.toml` if `XDG_CONFIG_HOME` is not set
3. `$DIR/citop/citop.toml` for every directory `DIR` in the comma-separated list `XDG_CONFIG_DIRS`
4. `/etc/xdg/citop/citop.toml` if `XDG_CONFIG_DIRS` is not set

## Format
citop uses a configuration file in TOML version v0.5.0 format.

Full example:
```toml
# The 'providers' array is mostly used to define credential for accessing online services. citop
# relies on two types of providers:
#    - 'source providers' are used for listing the CI pipelines associated to a given commit
#   (GitHub and GitLab are source providers).
#    - 'CI providers' are used to get detailed information about CI pipelines (GitLab, AppVeyor,
#    CircleCI, Travis are CI providers) 
#  
# citop requires credentials for at least one source provider and one CI provider to run.
#
[providers]

[[providers.gitlab]]
# Name of the provider to be shown in the TUI (optional, default: "gitlab")
name = "gitlab.com"
# URL of the gitlab instance (optional, default: "https://gitlab.com") 
url = "https://gitlab.com"
# Personal access token for accessing the GitLab API
# Tokens can be generated at https://gitlab.com/profile/personal_access_tokens
token = "gitlab_access_token"

[[providers.github]]
# Personal access token for accessing the GitHub API
# Tokens can be generated at https://github.com/settings/tokens
token = "github_access_token"

[[providers.travis]]
# Name of the provider to be shown in the TUI (optional, default: "travis")
name = "travis.org"
# URL of the travis instance ("org" and "com" can be used as shorthands for the URL
# of travis.org and travis.com)
url = "org"
# Personal access token for accessing the Travis API
# Token generation procedure: https://developer.travis-ci.org/authentication
token = "travis_org_access_token"

# Another travis instance with a different name, URL and access token
[[providers.travis]]
name = "travis.com"
url = "com"
token = "travis_com_access_token"

[[providers.appveyor]]
# Name of the provider to be shown in the TUI (optional, default: "appveyor")
name = "appveyor"
# API key for accessing the AppVeyor API
# API key can be generated here: https://ci.appveyor.com/api-keys
token = "token"

[[providers.circleci]]
# Name of the provider to be shown in the TUI (optional, default: "circleci")
name = "circleci"
# Personal API token to access the CircleCI API
# Tokens can be managed here: https://circleci.com/account/api
token = "token"
```

# ENVIRONMENT VARIABLES

* `BROWSER` is used to find the path of the default web browser
* `HOME`, `XDG_CONFIG_HOME` and `XDG_CONFIG_DIRS` are used to locate the configuration file

# EXAMPLES

Check pipeline status after pushing a commit:
```
$ git add .
$ git commit -m 'Commit message'
$ git push
$ citop
```

