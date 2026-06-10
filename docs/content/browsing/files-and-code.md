---
title: "Files and code"
description: "Tree pages, the blob viewer, line anchors and permalinks, markdown, and how binary, image, LFS, and oversized files render."
weight: 20
---

## Tree pages

`/{owner}/{repo}/tree/{ref}/{path}` lists a directory at any ref:
branch, tag, or commit SHA. The breadcrumb is clickable segment by segment,
and a README in the directory renders below the listing.

## The blob viewer

`/{owner}/{repo}/blob/{ref}/{path}` shows a file with syntax highlighting
(a couple hundred languages, by file name and content), line numbers, the
file size, and the line count. The toolbar offers `Raw` (the exact bytes,
correct content type), `Blame`, and `History` for that file.

### Line anchors

Click a line number to select the line and put `#L42` in the URL.
Shift-click another to select the range, `#L42-L57`. Links with anchors
scroll and highlight on load, exactly like github.com, so code review links
and chat links transfer between gitview and github.com by swapping the host.

### Permalinks

Press `y` and the ref in your URL is replaced with the full commit SHA, the
same trick github.com has. The page does not reload; the address bar just
becomes durable. Share that link and it shows the same bytes forever, no
matter what the branch does next.

## Markdown and rendering

Markdown files render GitHub-flavored by default, with a `Raw` view one
click away. Alerts, task lists, tables, footnotes, and heading anchors all
work. Sanitization is strict: scripts, event handlers, and `javascript:`
links never survive, even inside raw HTML blocks.

## Files that are not text

| File | What you get |
|---|---|
| Images | rendered inline (PNG, JPEG, GIF, SVG, WebP); SVG is served sandboxed |
| Binary | a "binary file not shown" notice with the size and a `Raw` download link |
| Git LFS pointer | an LFS banner with the object id and true size, no accidental gigabyte download |
| Symlink | the link target, shown as the file content, like git stores it |
| Over 10 MB | a too-large notice with a `Raw` link; the page stays fast |

## Raw files

`/{owner}/{repo}/raw/{ref}/{path}` serves the file bytes with a correct
content type and aggressive caching for SHA-pinned URLs. It is what `curl`
wants.
