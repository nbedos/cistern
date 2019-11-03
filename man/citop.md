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
## Up, j
Select the previous row of the list

## Down, k
Select the next row of the list

## o, + (resp. O)
Open (resp. Open recursively) the currently selected fold

## c, - (resp. C)
Close (resp. Close recursively) the currently select fold

## /
Open search prompt. The prompt may be closed with Enter or Escape.

## Enter, n (resp. N)
Move to the next (resp. previous) match

## v
View logs of the jobs under the cursor in less 

## b
Open selected row in $BROWSER

## q
Quit

## ?
Open manual page

# ENVIRONMENT
citop relies on the BROWSER environment variable to show web pages.

# EXAMPLES

Check pipeline status after pushing a commit:
```
$ git add .
$ git commit -m 'Commit message'
$ git push
$ citop
```

