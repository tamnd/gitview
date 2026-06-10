---
title: "URLs"
description: "The full route map, and why github.com deep links transfer by swapping the host."
weight: 20
---

gitview's URL space mirrors github.com exactly. That is a feature with
teeth: any github.com deep link, with the host swapped, lands on the same
content here, line anchors included.

## The route map

| Path | Page |
|---|---|
| `/` | index of served repositories (redirects home when there is only one) |
| `/{owner}/{repo}` | repository home |
| `/{owner}/{repo}/tree/{ref}/{path}` | directory listing |
| `/{owner}/{repo}/blob/{ref}/{path}` | file viewer |
| `/{owner}/{repo}/raw/{ref}/{path}` | raw file bytes |
| `/{owner}/{repo}/commits` | commit list at the default branch |
| `/{owner}/{repo}/commits/{ref}` | commit list at a ref, optionally `/{ref}/{path}` for one path |
| `/{owner}/{repo}/commit/{sha}` | one commit with its full diff |
| `/{owner}/{repo}/branches` | branch list |
| `/{owner}/{repo}/tags` | tag list |
| `/{owner}/{repo}/blame/{ref}/{path}` | blame view |
| `/{owner}/{repo}/find/{ref}` | the file finder |
| `/{owner}/{repo}/archive/...` | archive downloads, github.com-style refs |

Local repositories use `local` as the owner, so a repo served from disk
lives at `/local/{name}`.

## Refs in URLs

`{ref}` is a branch, a tag, or a commit SHA, exactly as github.com accepts
them. Branch names containing slashes disambiguate the same way github.com
does. Short SHAs redirect to the full SHA, so an ID copied from
`git log --oneline` works in the address bar.

## Anchors

`#L42` highlights a line on blob and blame pages, `#L42-L57` a range, and
`#diff-...` anchors address a file within a commit diff. These are the
github.com anchor forms, so links keep meaning the same thing when the host
changes.

## Permalinks

The `y` key rewrites the current URL's ref into the full commit SHA without
reloading. SHA-pinned pages are immutable, so gitview also marks them
long-cacheable; revisits are instant.
