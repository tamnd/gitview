---
title: "Files and code"
description: "Tree pages, the blob viewer, line anchors and permalinks, markdown, and the previews: images, media, pdf, docx, csv, parquet."
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

## File previews

Every file type below renders as a preview on the blob page. Text-backed
previews (markdown, csv) keep a `Code` toggle to the plain source; the
rest show the content directly.

| File | What you get |
|---|---|
| Images | rendered inline (PNG, JPEG, GIF, SVG, WebP, AVIF); SVG is served sandboxed |
| Audio | a player (MP3, WAV, OGG, FLAC, M4A, Opus) with seeking |
| Video | a player (MP4, WebM, MOV, OGV) with seeking, no autoplay |
| PDF | the browser's own viewer, embedded |
| Word (.docx) | extracted text at reading fidelity: headings, bold and italic, lists, tables, external links |
| CSV / TSV | a table with a header row, row numbers, and a `Code` toggle; semicolon csv detected; capped at 1,000 rows and 100 columns with a note |
| Parquet | the schema (every column with its type) plus the first 100 rows; the total row count comes from the file footer |

A preview that cannot parse its file falls back to the next sensible
view instead of an error page: a corrupt parquet shows the binary
notice, a malformed csv shows the source.

## Files that are not text

| File | What you get |
|---|---|
| Binary | a "binary file not shown" notice with the size and a `Raw` download link |
| Git LFS pointer | an LFS banner with the object id and true size, no accidental gigabyte download |
| Symlink | the link target, shown as the file content, like git stores it |
| Over 10 MB | a too-large notice with a `Raw` link; the page stays fast |

On Hugging Face repos, parquet and media files above the LFS threshold
show the LFS banner rather than a preview; gitview never downloads an
LFS object behind your back. Clone the repo and point gitview at the
local copy to preview those.

## Raw files

`/{owner}/{repo}/raw/{ref}/{path}` serves the file bytes with a correct
content type and aggressive caching for SHA-pinned URLs. It is what `curl`
wants.
