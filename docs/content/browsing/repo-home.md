---
title: "Repo home"
description: "The file table, last-commit column, README panel, branch picker, and clone box."
weight: 10
---

`/{owner}/{repo}` is the repository home, laid out exactly like github.com.

## The header

The repository name links back home from anywhere. Next to it: `Commits`,
`Branches`, `Tags`, and the branch picker. The picker switches between
branches and tags with a filter box; the current ref carries through to
whatever page you are on, so switching a branch while deep in a subtree keeps
you in that subtree when it exists on the other branch.

## The file table

Directories first, then files, case-insensitive, the github.com order.
Each row shows the entry's last commit subject and a relative date, and the
subject links to the full diff. Symlinks and submodules get their own icons
and pages: a symlink page shows its target, a submodule row shows the pinned
commit.

On backends that cannot afford a per-row history lookup the column quietly
stays empty rather than hammering an API; see
[backends](/backends/) for who supports what.

## The README

Rendered under the file table, GitHub-flavored: tables, task lists,
strikethrough, autolinks, syntax-highlighted code fences, footnotes, and
GitHub alerts (`> [!NOTE]`, `> [!WARNING]`, and friends). Relative links and
images resolve within the repository at the current ref, so READMEs written
for github.com render correctly without edits. Raw HTML is sanitized; what
github.com strips, gitview strips.

Every directory page renders its own README below the listing too, the way
github.com does.

## The clone box

The green `Code` button shows the clone URL: the local filesystem path for
local repositories, the upstream URL for remote ones. Archive downloads
(`.zip` and `.tar.gz` of the current ref) live in the same menu on backends
that can stream them.

## Empty repositories

A repository with no commits gets a calm empty-state page, not an error.
