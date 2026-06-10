---
title: "Installation"
description: "Install gitview with go install or build it from source."
weight: 20
---

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
