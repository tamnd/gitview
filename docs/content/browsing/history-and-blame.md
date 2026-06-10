---
title: "History and blame"
description: "Commit lists, full diffs, branches and tags pages, and blame with the age heat bar."
weight: 30
---

## Commit lists

`Commits` in the header lists history newest first, grouped by day, with
author, relative time, and a copyable short SHA per row. `Older` and `Newer`
page through. The `History` button on any tree or blob page filters the list
to commits touching that path, on backends whose API can filter by path.

## Commit pages

Click any subject line for `/{owner}/{repo}/commit/{sha}`: the full message,
author and committer when they differ, parent links, and the complete diff.
The diff is split per file with rename and copy detection, per-hunk
rendering, green and red line numbers on both sides, and a files-changed
summary bar at the top. Large diffs collapse per file so the page stays
usable; binary changes say so instead of rendering noise.

Short SHAs in URLs redirect to the full SHA, so a `git log --oneline` hash
pastes straight into the address bar.

## Branches and tags

`/branches` lists branches with their tip commit and relative date, the
default branch pinned on top. `/tags` lists tags newest first, each with
archive download links on backends that support archives.

## Blame

`Blame` on any blob page, or the `b` key, opens
`/{owner}/{repo}/blame/{ref}/{path}`. Every hunk shows its commit, author,
and age, with the same age heat bar github.com has: the newer the change,
the hotter the strip at the left edge. Each hunk offers *view blame prior to
this change* to reblame at the parent commit, which is the actual way to
archaeology through a file.

Blame needs the full repository history, so it is a local-backend feature;
on API backends the button simply is not there. The details per backend are
on the [backends pages](/backends/).
