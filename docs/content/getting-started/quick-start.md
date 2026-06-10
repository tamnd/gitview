---
title: "Quick start"
description: "A two-minute tour: serve a repo, walk the UI, learn the three keys worth knowing."
weight: 30
---

This page serves a repository and walks every major surface once. Any git
checkout works; your own project is the most fun.

## Serve a repository

```bash
cd ~/src/yourproject
gitview -open
```

`-open` launches your browser at <http://127.0.0.1:9419>, which redirects to
`/local/yourproject`, the repository home. You get the github.com layout: the
branch picker, the file table with per-file last-commit messages, the README
rendered underneath, and a clone box on the right.

A few other targets to try later:

```bash
gitview ~/src                        # every repo under ~/src, with an index page
gitview gh:golang/go                 # github.com repo over the API, no clone
gitview hf:openai-community/gpt2     # a Hugging Face model
```

## Walk the tree

Click into a directory, then a file. Blob pages have syntax highlighting,
line numbers, and the same line anchors github.com has: click a line number,
shift-click another to select a range, and the `#L10-L20` URL is shareable.
Markdown renders as on github.com, alerts and all; images, binaries, and LFS
pointers each get a sensible page instead of a wall of bytes.

## Read some history

The `History` button on any file, or `Commits` in the header, opens the
commit list. Click a subject line for the full diff, split into files with
per-hunk rendering, additions and deletions counted in the bar at the top.
`Blame` on a blob page attributes every line, with the age heat bar
github.com has, and lets you reblame at the commit before each hunk.

## Learn three keys

| Key | What it does |
|---|---|
| `t` | open the file finder, fuzzy match across every path at the current ref |
| `y` | turn the URL into a permalink (ref becomes the full commit SHA) |
| `?` | the full shortcut list, in a dialog |

The finder is the one to internalize: `t`, a few characters, arrows if
needed, Enter. It is the fastest way around any codebase. The
[full shortcut map](/browsing/finder-and-shortcuts/) has the rest.

## Where to next

- [Repo home](/browsing/repo-home/), [files and code](/browsing/files-and-code/),
  and [history](/browsing/history-and-blame/) cover each surface in detail.
- [Backends](/backends/) explains local, GitHub, and Hugging Face targets,
  tokens, and which buttons disappear where.
- The [CLI reference](/reference/cli/) lists every flag, variable, and exit
  code on one page.
