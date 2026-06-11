---
title: "Release notes"
description: "What shipped in each gitview release, in plain words."
weight: 50
---

The full artifact list for every release, with checksums, signatures, and
packages, lives on the [releases page](https://github.com/tamnd/gitview/releases).
This page is the readable history.

## v0.1.0

The first release: one binary that serves a read-only, GitHub-style web view
of git repositories. Point it at a checkout, a folder of checkouts, a
repository on github.com, or a model on huggingface.co, and browse it in a UI
you already know.

```bash
gitview ~/src/myproject          # one local repository
gitview ~/src                    # a directory of repositories
gitview gh:torvalds/linux        # github.com over the REST API
gitview hf:datasets/squad        # a Hugging Face dataset
```

No database, no config file, no daemon. The local backend shells out to your
own `git`, so anything your git can read, gitview can show.

### Browsing

Everything the code side of github.com does, the same way it does it: the
file tree with last-commit messages, commit history with diffs, branches and
tags, blame, a fuzzy file finder on `t`, line anchors that update as you
click, and README rendering with GitHub-flavored markdown, alerts, and
syntax-highlighted fences. Light and dark themes follow your system. The
[quick start](/getting-started/quick-start/) walks all of it.

### Three backends

- **Local**: any repository or bare repository on disk, via the git CLI.
- **GitHub** (`gh:owner/repo` or a github.com URL): the REST API with ETag
  caching, so a token is optional and the rate limit is treated politely.
- **Hugging Face** (`hf:owner/name`, also `datasets/` and `spaces/`): models,
  datasets, and spaces straight from the Hub API, with LFS handled honestly.

Each backend declares what it can do and the UI degrades cleanly; the
[backends section](/backends/) has the per-backend details.

### File previews

Files render by type, not just as text: images, audio, and video play
inline; PDFs display in the browser; Word documents render as readable text;
CSV and TSV become real tables; Parquet files show their schema and first
rows with proper types. Anything tabular keeps an honest truncation note,
and every preview keeps Raw a click away with HTTP range support. The
[full matrix](/browsing/files-and-code/) lists what renders how.

### Quiet on security

The server is read-only by construction. Strict CSP on every page, sanitized
markdown, escape-everything previews with hard size caps, and tokens that
never cross services. The [security page](/reference/security/) is the
complete posture.

### Install

Archives for Linux, macOS, Windows, and FreeBSD, plus deb, rpm, and apk
packages, are attached to the release. There is also a container image:

```bash
docker run -v ~/src:/repos:ro -p 9419:9419 ghcr.io/tamnd/gitview:0.1.0
```

The `checksums.txt` on each release is signed with keyless cosign. See
[installation](/getting-started/installation/) for every option.
