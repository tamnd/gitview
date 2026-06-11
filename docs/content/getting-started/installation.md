---
title: "Installation"
description: "Install gitview from a release, with go install, or from source."
weight: 20
---

## Prebuilt binaries

Every [release](https://github.com/tamnd/gitview/releases) carries archives
for Linux, macOS, Windows, and FreeBSD, plus deb, rpm, and apk packages for
Linux. Download, unpack, put `gitview` on your `PATH`, done. The
`checksums.txt` on each release is signed with keyless
[cosign](https://docs.sigstore.dev/) if you want to verify before running.

## Container

```bash
docker run -v ~/src:/repos:ro -p 9419:9419 ghcr.io/tamnd/gitview
```

The image ships with git inside; mount the repositories to browse at
`/repos` (read-only is enough) and open <http://127.0.0.1:9419>.

## With Go

```bash
go install github.com/tamnd/gitview/cmd/gitview@latest
```

That puts `gitview` in `$(go env GOPATH)/bin`, which is `~/go/bin` unless you
moved it. Make sure that directory is on your `PATH`.

## From source

```bash
git clone https://github.com/tamnd/gitview
cd gitview
go build ./cmd/gitview
./gitview -version
```

## Requirements

- **Go 1.26 or later** to build. The released binary has no Go requirement.
- **The `git` CLI**, for browsing local repositories. gitview shells out to
  it rather than reimplementing git, so anything your git can read, gitview
  can show. Remote backends do not need git at all.

That is the whole list. No database, no config file, no daemon.

## Checking the install

```bash
gitview -version
```

prints the version and exits. Then run `gitview` inside any git checkout and
open <http://127.0.0.1:9419>:

```bash
cd ~/src/yourproject
gitview
```

If you see your file table and README, you are done. Next:
the [quick start](/getting-started/quick-start/) walks the rest of the UI.
