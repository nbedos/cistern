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
Specify the git repository to work with. REPOSITORY can be either a path to a local git repository,
or the URL of an online repository hosted at GitHub or GitLab. Both web URLs and git URLs are
accepted.

In the absence of this option, citop will work with the git repository located in the current 
directory. If there is no such repository, citop will fail.

Examples:
```shell
# Work with the git repository in the current directory
citop
# Work with the repository specified by a web URL
citop -r https://gitlab.com/nbedos/citop
citop -r github.com/nbedos/citop
# Git URLs are accepted
citop -r git@github.com:nbedos/citop.git
# Paths to a local repository are accepted too
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

?          View manual page

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
each table is given below.

### Table `[providers]`
The 'providers' table is used to define credentials for accessing online services. citop
relies on two types of providers:

- 'source providers' are used for listing the CI pipelines associated to a given commit
(GitHub and GitLab are source providers)
- 'CI providers' are used to get detailed information about CI pipelines (GitLab, AppVeyor,
CircleCI, Travis and Azure Devops are CI providers)

citop requires credentials for at least one source provider and one CI provider to run.

### Table `[[providers.gitlab]]`
`[[providers.gitlab]]` defines a GitLab account

----------------------------------------------------------
Key      Description
------   -------------------------------------------------
name     Name under which this provider appears in the TUI (string, optional, default: "gitlab")

url      URL of the GitLab instance (string, optional, default: "gitlab.com")

token    Personal access token for the GitLab API (string, optional, default: "")

----------------------------------------------------------

GitLab access tokens are managed at [https://gitlab.com/profile/personal_access_tokens](https://gitlab.com/profile/personal_access_tokens)

Example:
```toml
[[providers.gitlab]]
name = "gitlab.com"
url = "https://gitlab.com"
token = "gitlab_api_token"
```

### Table `[[providers.github]]`
`[[providers.github]]` defines a GitHub account

-----------------------------------------------------------
Key     Description
------  ---------------------------------------------------
token   Personal access token for the GitHub API (string, optional, default: "")

-----------------------------------------------------------

GitHub access tokens are managed at [https://github.com/settings/tokens](https://github.com/settings/tokens)

Example:
```toml
[[providers.github]]
token = "github_api_token"
```


### Table `[[providers.travis]]`
`[[providers.travis]]` defines a Travis CI account

-----------------------------------------------------------
Key     Description
------  ---------------------------------------------------
name    Name under which this provider appears in the TUI (string, mandatory)

url     URL of the GitLab instance. "org" and "com" can be used as shorthands for the full URL of travis.org and travis.com (string, mandatory)

token   Personal access token for the Travis API (string, optional, default: "")

----------------------------------------------------------

Travis access tokens are managed at the following locations:

* [https://travis-ci.org/account/preferences](https://travis-ci.org/account/preferences)
* [https://travis-ci.com/account/preferences](https://travis-ci.com/account/preferences)


Example:
```toml
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

----------------------------------------------------------
Key     Description
------  --------------------------------------------------
name    Name under which this provider appears in the TUI (string, optional, default: "appveyor")

token   Personal access token for the AppVeyor API (string, optional, default: "")

----------------------------------------------------------

AppVeyor access tokens are managed at [https://ci.appveyor.com/api-keys](https://ci.appveyor.com/api-keys)


Example:
```toml
[[providers.appveyor]]
name = "appveyor"
token = "appveyor_api_key"
```


### Table `[[providers.circleci]]`
`[[providers.circleci]]` defines a CircleCI account

----------------------------------------------------------
Key     Description
------  --------------------------------------------------
name    Name under which this provider appears in the TUI (string, optional, default: "circleci")

token   Personal access token for the CircleCI API (string, optional, default: "")

----------------------------------------------------------

CircleCI access tokens are managed at [https://circleci.com/account/api](https://circleci.com/account/api)


Example:
```toml
[[providers.circleci]]
name = "circleci"
token = "circleci_api_token"
```

### Table `[[providers.azure]]`
`[[providers.azure]]` defines an Azure Devops account

----------------------------------------------------------
Key     Description
------  --------------------------------------------------
name    Name under which this provider appears in the TUI (string, optional, default: "azure")

token   Personal access token for the Azure Devops API (string, optional, default: "")

----------------------------------------------------------

Azure Devops personal access tokens are managed at [https://dev.azure.com/](https://dev.azure.com/)


Example:
```toml
[[providers.azure]]
name = "azure"
token = "azure_api_token"
```


### Examples
Here are a few examples of `citop.toml` configuration files.

Monitor pipelines on Travis CI, AppVeyor and CircleCI for a repository hosted on GitHub:
```toml
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
```toml
[[providers.gitlab]]
token = "gitlab_api_token"
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

Show pipelines associated to the HEAD of the current git repository
```shell
citop
```

Show pipelines associated to a specific commit, tag or branch
```shell
citop 64be3c6
citop 0.9.0
citop feature/doc
```

Show pipelines of a repository specified by a URL
```shell
citop -r https://gitlab.com/nbedos/citop
citop -r git@github.com:nbedos/citop.git
citop -r github.com/nbedos/citop
```

Show pipelines of a local repository specified by a path
```shell
citop -r /home/user/repos/myrepo
```

Specify both repository and commit
```shell
citop -r github.com/nbedos/citop 64be3c6
```

# BUGS
Questions, bug reports and feature requests are welcome and should be submitted
on [GitHub](https://github.com/nbedos/citop/issues).

# NOTES
1. **citop repository**
    * [https://github.com/nbedos/citop](https://github.com/nbedos/citop)
2. **XDG base directory specification**
    * [https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html)



