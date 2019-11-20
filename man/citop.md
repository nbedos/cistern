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
View the log of the job at the cursor in PAGER. The log may be incomplete if
the job is still running.

## b
View selected line in $BROWSER

## q
Quit

## ?
View manual page

# ENVIRONMENT
citop uses the following environment variables:

* BROWSER is called to open web pages

# EXAMPLES

Check pipeline status after pushing a commit:
```
$ git add .
$ git commit -m 'Commit message'
$ git push
$ citop
```

