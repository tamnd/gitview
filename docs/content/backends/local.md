---
title: "Local repositories"
description: "Single repos, bare repos, and whole directories of repos, read through the git CLI."
weight: 10
---

The local backend reads repositories by shelling out to your `git`. Nothing
is parsed out of `.git` by hand, so every repository your git can read,
gitview can serve: worktrees, bare repos, mirrors, partial clones,
alternates, whatever.

## A single repository

```bash
gitview ~/src/yourproject
gitview                       # default target is "."
```

Like git itself, gitview finds the repository from anywhere inside it:
running `gitview` in `repo/sub/dir` serves the whole repo. Bare repositories
work too:

```bash
gitview ~/mirrors/linux.git
```

The repository appears at `/local/{name}`, named after its directory.

## A directory of repositories

```bash
gitview ~/src
```

If the target is not itself a repository, gitview scans its immediate
children (one level, hidden directories skipped) and serves every repository
it finds, with an index page at `/` listing them all. A `~/src` with forty
clones becomes a browsable forge in one command.

The scan happens once at startup; clone something new into the directory and
restart to pick it up.

## What you get

Everything. The local backend is the only one with the full repository on
disk, so every feature works: blame, per-path history, the last-commit
column, archives, reblame-at-parent, all of it. It is also fast; pages
render in tens of milliseconds on real repositories.

## Live repositories

gitview reads the repository as it is at request time. Commit, fetch, or
switch branches and the next page load sees the new state; no restarts, no
cache to bust. The one startup-time decision is the repo list in
multi-repo mode.
