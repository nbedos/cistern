% CISTERN(1) | version \<version\>
% Nicolas Bedos

# NAME
**cistern** – Continuous Integration Table Of Pipelines

# SYNOPSIS
`cistern [-r REPOSITORY | --repository REPOSITORY] [COMMIT]`

`cistern -h | --help`

`cistern --version`

# DESCRIPTION
cistern monitors the CI pipelines associated to a specific commit of a git repository.

cistern currently integrates with the following online services. Each of the service is one or both
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
name of a tag or a branch. If this option is missing cistern will monitor the commit referenced by
HEAD.

Example:
```shell
# Show pipelines for commit 64be3c6
cistern 64be3c6
# Show pipelines for the commit referenced by the tag '0.9.0'
cistern 0.9.0
# Show pipelines for the commit at the tip of a branch
cistern feature/doc
```

# OPTIONS
## `-r=REPOSITORY, --repository=REPOSITORY`
Specify the git repository to monitor. If REPOSITORY is the path of a local repository, cistern
will monitor all the associated remotes. If REPOSITORY is a URL, cistern will monitor the
corresponding online repository.

If this option is not set, cistern will behave as if it had been set to the path of the current
directory.

Note that cistern will only monitor repositories hosted on GitLab or GitHub.

Examples:
```shell
# Monitor pipelines of the git repository in the current directory
cistern
# Monitor pipelines of the repository specified by a web URL
cistern -r https://gitlab.com/nbedos/cistern
cistern -r github.com/nbedos/cistern
# Git URLs are accepted
cistern -r git@github.com:nbedos/cistern.git
# A path referring to a local repository is valid too
cistern -r /home/user/repos/myrepo
```

## `-h, --help`
Show usage of cistern

## `--version`
Print the version of cistern being run

# COLUMNS
## REF
Tag or branch associated to the pipeline

## PIPELINE
Identifier of the pipeline

## TYPE
Either "P" (Pipeline), "S" (Stage), "J" (Job) or "T" (Task)

## STATE
State of the pipeline

## XFAIL
Expected failure. Boolean indicating whether this step is allowed to fail without impacting the
overall state of the pipeline

## CREATED, STARTED, FINISHED
Date when the pipeline was created, started or finished

## DURATION
Time it took for the pipeline to finish

## NAME
Name of the provider followed by the name of the pipeline, if any

## URL
URL of the step on the website of the provider


# INTERACTIVE COMMANDS
Below are the default commands for interacting with cistern.



## Tabular view

-----------------------------------------------------------------
Key                 Action
------------------  -----------------------------------------------
Up, k, Ctrl-p       Move cursor up by one line

Down, j, Ctrl-n     Move cursor down by one line

Right, l            Scroll right

Left, h             Scroll left

Ctrl-u              Move cursor up by half a page

Page Up, Ctrl-B     Move cursor up by one page

Ctrl-d              Move cursor down by half a page

Page Down, Ctrl-F   Move cursor down by one page

Home                Move cursor to the first line

End                 Move cursor to the last line

<                   Move sort column left

!                   Reverse sort order

o, +                Open the fold at the cursor

O                   Open the fold at the cursor and all sub-folds

c, -                Close the fold at the cursor

C                   Close the fold at the cursor and all sub-folds

Tab                 Toggle fold open/closed

b                   Open associated web page in $BROWSER

v                   View the log of the job at the cursor

/                   Open search prompt

Escape              Close search prompt

Enter, n            Move to the next search match

N                   Move to the previous search match

f                   Follow the current git reference to the commit it points to

g                   Open git reference selection prompt

r, F5               Refresh pipeline data

?, F1               Show help screen

q                   Quit

-----------------------------------------------------------------



## Search prompt

--------------------------------------
Key          Action
-----------  -------------------------
Enter        Search

Backspace    Delete last character

Ctrl-U       Delete whole line

Escape       Close prompt

--------------------------------------



## Git reference selection prompt

-----------------------------------------------------------------
Key                 Action
------------------  -----------------------------------------------
Enter               Validate

Backspace           Delete last character

Ctrl-U              Delete whole line

Tab, Shift-Tab      Complete

Up, Ctrl-P          Move the cursor to the previous suggestion

Down, Ctrl-N        Move the cursor to the next suggestion

Page up, Ctrl-B     Move the cursor up by one page

Page down, Ctrl-F   Move the cursor down by one page

Escape              Close prompt

----------------------------------------------------------


## Help screen

----------------------------------------------------------
Key                    Action
---------------------  -------------------------------------
j, Down, Ctrl-N        Scroll down by one line

k, Up, Ctrl-P          Scroll up by one line

Page up, Ctrl-B        Scroll up by one page

Page down, Ctrl-F      Scroll down by one page

Ctrl-U                 Scroll up by half a page

Ctrl-D                 Scroll down by half a page

q                      Exit help screen

----------------------------------------------------------


# CONFIGURATION FILE
## Location
cistern follows the XDG base directory specification \[2\] and expects to find the configuration file
at one of the following locations depending on the value of the two environment variables
`XDG_CONFIG_HOME` and `XDG_CONFIG_DIRS`:

1. `"$XDG_CONFIG_HOME/cistern/cistern.toml"`
2. `"$DIR/cistern/cistern.toml"` for every directory `DIR` in the comma-separated list `"$XDG_CONFIG_DIRS"`

If `XDG_CONFIG_HOME` (resp. `XDG_CONFIG_DIRS`) is not set, cistern uses the default value
`"$HOME/.config"` (resp. `"/etc/xdg"`) instead.


## Format
cistern uses a configuration file in [TOML version v0.5.0](https://github.com/toml-lang/toml/blob/master/versions/en/toml-v0.5.0.md)
format.

The complete format of the configuration file is described in the example included in the
release archives which is also available [on GitHub](https://github.com/nbedos/cistern/blob/master/cmd/cistern/cistern.toml)

## Minimal example
This is a minimal example of a configuration file focused mostly on setting up credentials.
It should be enough to get you started running cistern.

```toml
#### CISTERN CONFIGURATION FILE ####
# This is a configuration file for cistern that should be located at
# $XDG_CONFIG_HOME/cistern/cistern.toml

## GENERIC OPTIONS ##
# List of columns displayed on screen. Available columns are
# "ref", "pipeline", "type", "state", "created", "started",
# "finished", "duration", "xfail", "name", "url"
columns = ["ref", "pipeline", "type", "state", "started", "duration", "name", "url"]

# Name of the column used for sorting the table prefixed by an
# optional "+" (ascending order) or "-" (descending order).
sort = "-started"


## PROVIDERS ##
[providers]
# The sections below define credentials for accessing source
# providers (GitHub, GitLab) and CI providers (GitLab, Travis,
# AppVeyor, Azure Devops, CircleCI).
#
# Feel free to remove any section as long as you leave one
# section for a source provider and one for a CI provider.
#
# When an API token is not set or set to the empty string,
# cistern will still run but with some limitations:
#     - GitHub: cistern will hit the rate-limit for
#     unauthenticated requests in a few minutes
#     - GitLab: cistern will NOT be able to access pipeline
#     jobs
#

### GITHUB ###
[[providers.github]]
# GitHub API token (optional, string)
# GitHub token management: https://github.com/settings/tokens
token = ""


### GITLAB ###
[[providers.gitlab]]
# GitLab instance URL (optional, string, default:
# "https://gitlab.com")
# (the GitLab instance must support GitLab REST API V4)
url = "https://gitlab.com"

# GitLab API token (optional, string)
# gitlab.com token management:
#     https://gitlab.com/profile/personal_access_tokens
token = ""


### TRAVIS CI ###
[[providers.travis]]
# URL of the Travis instance. "org" and "com" can be used as
# shorthands for the full URL of travis.org and travis.com
# (string, mandatory)
url = "org"

# API access token for the travis API (string, optional).
# Travis tokens are managed at:
#    - https://travis-ci.org/account/preferences
#    - https://travis-ci.com/account/preferences
token = ""


# Define another account for accessing travis.com
[[providers.travis]]
url = "com"
token = ""


### APPVEYOR ###
[[providers.appveyor]]
# AppVeyor API token (optional, string)
# AppVeyor token managemement: https://ci.appveyor.com/api-keys
token = ""


### CIRCLECI ###
[[providers.circleci]]
# Circle CI API token (optional, string)
# See https://circleci.com/account/api
token = ""


### AZURE DEVOPS ###
[[providers.azure]]
# Azure API token (optional, string)
# Azure token management is done at https://dev.azure.com/ via
# the user settings menu
token = ""

```

# ENVIRONMENT
## ENVIRONMENT VARIABLES

* `BROWSER` is used to find the path of the default web browser
* `PAGER` is used to view log files. If the variable is not set, cistern will call `less`
* `HOME`, `XDG_CONFIG_HOME` and `XDG_CONFIG_DIRS` are used to locate the configuration file

## LOCAL PROGRAMS

cistern relies on the following local executables:

* `less` to view log files, unless `PAGER` is set
* `git` (optional) to translate the abbreviated SHA identifier of a commit into
a non-abbreviated SHA and also to support 'insteadOf' and 'pushInsteadOf'
configuration options for remote URLs

# EXAMPLES

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

# BUGS
Questions, bug reports and feature requests are welcome and should be submitted
on [GitHub](https://github.com/nbedos/cistern/issues).

# NOTES
1. **cistern repository**
    * [https://github.com/nbedos/cistern](https://github.com/nbedos/cistern)
2. **XDG base directory specification**
    * [https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html)



