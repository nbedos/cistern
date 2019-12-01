# NAME

citop – Continuous Integration Table Of Pipelines

# SYNOPSIS

**`citop`** `[--commit=COMMIT]` `[--repository=REPOSITORY]`

**`citop`** `--version`

# DESCRIPTION

citop monitors CI pipelines associated to a given commit of a git
repository.

citop currently integrates with the following online services. Each of
the service is one or both of the following:

  - A “source provider” that is used to list the pipelines associated to
    a given commit of an online repository
  - A “CI provider” that is used to get detailed information about CI
    builds

| Provider  | Source | CI  | URL                                               |
| :-------- | :----- | :-- | :------------------------------------------------ |
| GitHub    | yes    | no  | https://github.com/                               |
| GitLab    | yes    | yes | https://gitlab.com/                               |
| AppVeyor  | no     | yes | https://www.appveyor.com/                         |
| CircleCI  | no     | yes | https://circleci.com/                             |
| Travis CI | no     | yes | https://travis-ci.org/ and https://travis-ci.com/ |

# OPTIONS

## `-c, --commit=COMMIT`

Specify the commit to monitor. COMMIT is expected to be the sha1
identifier of a commit. If this option is missing, citop will monitor
the commit referenced by HEAD.

Example:

``` shell
citop -c 64be3c6
```

## `-r, --repository=REPOSITORY`

Specify the URL of the repository to monitor. REPOSITORY is expected to
be the URL of an online repository hosted at GitHub or GitLab. It may be
either a git URL (over SSH or HTTPS) or a web URL.

In the absence of this option, citop must be run from a directory
containing a git repository. citop will then monitor the repository
identified by the first push URL associated to the remote named
“origin”.

Examples:

``` shell
citop -r 'https://github.com/nbedos/citop'
citop -r 'git@github.com:nbedos/citop.git'
```

## `--version`

Print the version of citop being run

# INTERACTIVE COMMANDS

Below are the default commands for interacting with citop.

| Key       | Action                                                |
| :-------- | :---------------------------------------------------- |
| Up, j     | Move cursor up by one line                            |
| Down, k   | Move cursor down by one line                          |
| Page Up   | Move cursor up by one screen                          |
| Page Down | Move cursor down by one screen                        |
| o, +      | Open the fold at the cursor                           |
| O         | Open the fold at the cursor and all sub-folds         |
| c, -      | Close the fold at the cursor                          |
| C         | Close the fold at the cursor and all sub-folds        |
| /         | Open search prompt                                    |
| Escape    | Close search prompt                                   |
| Enter, n  | Move to the next match                                |
| N         | Move to the previous match                            |
| v         | View the log of the job at the cursor<sup>\[a\]</sup> |
| b         | Open with default web browser                         |
| q         | Quit                                                  |
| ?         | View manual page                                      |

  - <sup>\[a\]</sup> Note that if the job is still running, the log may
    be incomplete.

# CONFIGURATION FILE

## Location

citop follows the XDG base directory specification \[1\] and expects to
find the configuration file at one of the following locations depending
on the value of the two environment variables `XDG_CONFIG_HOME` and
`XDG_CONFIG_DIRS`:

1.  `"$XDG_CONFIG_HOME/citop/citop.toml"`
2.  `"$DIR/citop/citop.toml"` for every directory `DIR` in the
    comma-separated list `"$XDG_CONFIG_DIRS"`

If `XDG_CONFIG_HOME` (resp. `XDG_CONFIG_DIRS`) is not set, citop uses
the default value `"$HOME/.config"` (resp. `"/etc/xdg"`) instead.

## Format

citop uses a configuration file in TOML version v0.5.0 format. The
configuration file is made of keys grouped together in tables. The
specification of each table is given below.

### Table `[providers]`

The ‘providers’ table is used to define credentials for accessing online
services. citop relies on two types of providers:

  - ‘source providers’ are used for listing the CI pipelines associated
    to a given commit (GitHub and GitLab are source providers)
  - ‘CI providers’ are used to get detailed information about CI
    pipelines (GitLab, AppVeyor, CircleCI and Travis are CI providers)

citop requires credentials for at least one source provider and one CI
provider to run.

### Table `[[providers.gitlab]]`

`[[providers.gitlab]]` defines a GitLab account

| Key   | Description                                                                             |
| :---- | :-------------------------------------------------------------------------------------- |
| name  | Name under which this provider appears in the TUI (string, optional, default: “gitlab”) |
| url   | URL of the GitLab instance (string, optional, default: “gitlab.com”)                    |
| token | Personal access token for the GitLab API (string, optional, default: "")                |

GitLab access tokens are managed at
https://gitlab.com/profile/personal\_access\_tokens

Example:

``` toml
[[providers.gitlab]]
name = "gitlab.com"
url = "https://gitlab.com"
token = "gitlab_api_token"
```

### Table `[[providers.github]]`

`[[providers.github]]` defines a GitHub account

| Key   | Description                                                              |
| :---- | :----------------------------------------------------------------------- |
| token | Personal access token for the GitHub API (string, optional, default: "") |

GitHub access tokens are managed at https://github.com/settings/tokens

Example:

``` toml
[[providers.github]]
token = "github_api_token"
```

### Table `[[providers.travis]]`

`[[providers.travis]]` defines a Travis CI account

| Key   | Description                                                                                                                             |
| :---- | :-------------------------------------------------------------------------------------------------------------------------------------- |
| name  | Name under which this provider appears in the TUI (string, mandatory)                                                                   |
| url   | URL of the GitLab instance. “org” and “com” can be used as shorthands for the full URL of travis.org and travis.com (string, mandatory) |
| token | Personal access token for the Travis API (string, optional, default: "")                                                                |

Travis access tokens are managed at the following locations:

  - https://travis-ci.org/account/preferences
  - https://travis-ci.com/account/preferences

Example:

``` toml
[[providers.travis]]
name = "travis.org"
url = "org"
token = "travis_org_api_token"

[[providers.travis]]
name = "travis.com"
url = "com"
token = "travis_com_api_token"
```

### Table `[[providers.appveyor]]`

`[[providers.appveyor]]` defines an AppVeyor account

| Key   | Description                                                                               |
| :---- | :---------------------------------------------------------------------------------------- |
| name  | Name under which this provider appears in the TUI (string, optional, default: “appveyor”) |
| token | Personal access token for the AppVeyor API (string, optional, default: "")                |

AppVeyor access tokens are managed at https://ci.appveyor.com/api-keys

Example:

``` toml
[[providers.appveyor]]
name = "appveyor"
token = "appveyor_api_key"
```

### Table `[[providers.circleci]]`

`[[providers.circleci]]` defines a CircleCI account

| Key   | Description                                                                               |
| :---- | :---------------------------------------------------------------------------------------- |
| name  | Name under which this provider appears in the TUI (string, optional, default: “circleci”) |
| token | Personal access token for the CircleCI API (string, optional, default: "")                |

CircleCI access tokens are managed at https://circleci.com/account/api

Example:

``` toml
[[providers.circleci]]
name = "circleci"
token = "circleci_api_token"
```

### Examples

Here are a few examples of `citop.toml` configuration files.

Monitor pipelines on Travis CI, AppVeyor and CircleCI for a repository
hosted on GitHub:

``` toml
[[providers.github]]
token = "github_api_token"

[[providers.travis]]
url = "org"
token = "travis_org_api_token"

[[providers.appveyor]]
token = "appveyor_api_key"

[[providers.circleci]]
token = "circleci_api_token"
```

Monitor pipelines on GitLab CI for a repository hosted on GitLab itself:

``` toml
[[providers.gitlab]]
token = "gitlab_api_token"
```

# ENVIRONMENT VARIABLES

  - `BROWSER` is used to find the path of the default web browser
  - `HOME`, `XDG_CONFIG_HOME` and `XDG_CONFIG_DIRS` are used to locate
    the configuration file

# EXAMPLES

Check pipeline status after pushing a commit:

``` shell
git add .
git commit -m 'Commit message'
git push
citop
```

# NOTES

1.  https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html
