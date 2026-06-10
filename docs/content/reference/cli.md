---
title: "CLI"
description: "Every flag, target form, environment variable, and exit code."
weight: 10
---

```
gitview [flags] [target]
```

One optional target, flags before it (standard Go `flag` behavior). With no
target, gitview serves the repository you are standing in.

## Targets

| Form | Example | Serves |
|---|---|---|
| local path | `.`, `~/src/linux`, `~/mirrors/x.git` | that repository, worktree or bare |
| directory of repos | `~/src` | every child repository, with an index page |
| GitHub shorthand | `gh:torvalds/linux` | the repo over the GitHub API |
| GitHub URL | `https://github.com/o/r`, deep links included | same |
| Hugging Face shorthand | `hf:owner/name`, `hf:datasets/o/n`, `hf:spaces/o/n` | the Hub repo over its API |
| Hugging Face URL | `https://huggingface.co/o/n` | same |

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:9419` | listen address; port `0` picks a free one |
| `-open` | off | open the browser once the server is listening |
| `-token` | empty | GitHub API token; falls back to `$GITHUB_TOKEN` |
| `-hf-token` | empty | Hugging Face token; falls back to `$HF_TOKEN` |
| `-dev` | off | verbose error pages and per-request logging, for hacking on gitview |
| `-version` | | print the version and exit |

## Environment

| Variable | Used for |
|---|---|
| `GITHUB_TOKEN` | GitHub backend auth when `-token` is not given |
| `HF_TOKEN` | Hugging Face backend auth when `-hf-token` is not given |

There is no config file, on purpose. The tool is launch-and-discard; every
option fits on the command line, and nothing persists except the theme
choice in your browser.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | clean exit, including ctrl-C shutdown |
| 1 | runtime failure: target is not a repository, port already bound, remote repo not found |
| 2 | usage error: unknown flag, two targets, malformed `gh:` or `hf:` target |

`SIGINT` and `SIGTERM` shut down gracefully with a few seconds' grace for
in-flight requests; a second signal exits immediately.
