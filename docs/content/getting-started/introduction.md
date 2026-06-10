---
title: "Introduction"
description: "What gitview is, what it deliberately is not, and when to reach for it."
weight: 10
---

gitview is a read-only web view of git repositories whose UI matches
github.com at the pixel level for the surfaces it builds: repository home
with the file table and rendered README, tree and blob pages with syntax
highlighting and line anchors, commit lists, commit diffs, branches, tags,
blame with an age heat bar, archive downloads, and the `t` file finder. Light
and dark themes follow your system, and the keyboard shortcuts you already
use on github.com work here.

## When it is the right tool

- **Reading a repo that only exists on your disk.** A checkout, a bare repo,
  a mirror, a `~/src` full of clones. `gitview ~/src` serves the whole folder
  with an index page.
- **Reading a GitHub repo without leaving your terminal-and-localhost
  workflow**, or when you want one tool for local and remote browsing. The
  GitHub backend speaks the REST API, so nothing is cloned.
- **Reading the files of a Hugging Face model, dataset, or Space** the way
  you read code, instead of through the Hub's file viewer.
- **Sharing a read-only view on a LAN.** One flag binds a non-loopback
  address; gitview prints a clear warning about what that exposes.

## What it will never do

There are no issues, pull requests, wikis, actions, accounts, stars, or
notifications, and there is no editing. gitview never writes to your
repository: the local backend invokes `git` with read subcommands only, and
there is no flag that enables anything else. This is not a roadmap gap, it is
the product: a viewer you can point at anything without thinking about what
it might touch.

## How it is built

One static Go binary. Local repositories are read by shelling out to the
`git` CLI you already have; GitHub repositories through the REST API;
Hugging Face repositories through the Hub API. Pages render server-side with
a hand-written reimplementation of GitHub's Primer look, real Octicons, and a
sprinkle of vanilla JavaScript. Everything is embedded in the binary, so
there is nothing to install next to it and the pages work with JavaScript
disabled.

Ready? [Install it](/getting-started/installation/) and take the
[quick start](/getting-started/quick-start/) tour.
