---
title: "Troubleshooting"
description: "The errors you might actually meet, and what they mean."
weight: 40
---

## "is not a git repository and contains none"

The target path is neither a repository nor a directory with repositories
one level down. The scan is deliberately one level deep, so
`gitview ~/code` finds `~/code/project` but not `~/code/work/project`;
point gitview at the directory whose direct children are the repos.

## "address already in use"

Something else owns the port. Either stop it or pick another:

```bash
gitview -addr 127.0.0.1:0 .    # port 0 = any free port, printed at startup
```

## A GitHub repo 404s that definitely exists

Almost always auth. Private repositories without a token, or with a token
that cannot read them, are indistinguishable from missing ones; that is how
the GitHub API answers. Set `GITHUB_TOKEN` and retry.

## "rate limit" pages while browsing GitHub

Anonymous API quota is 60 requests per hour and browsing is chatty on first
visit. Set a token and the quota becomes 5,000, which is hard to exhaust;
gitview's conditional caching makes repeat visits nearly free either way.
The error page shows when the quota resets.

## A Hugging Face repo 404s that exists on the Hub

Gated and private Hub repositories look like 404 to anonymous callers. Set
`HF_TOKEN` to a token whose account has accepted the model's terms. Also
check the kind: datasets need `hf:datasets/owner/name`, Spaces
`hf:spaces/owner/name`.

## Buttons missing on a remote repo

Not a bug: the backend cannot support that feature, so its UI is hidden.
The [feature matrix](/backends/) says exactly what each backend offers.
Blame and the last-commit column are local-only and local-plus-Hub
respectively; commit diffs and archives exist everywhere except the Hub.

## A page renders oddly

Run with `-dev` to get error detail in responses and a request log on
stderr, then open an issue at
[github.com/tamnd/gitview](https://github.com/tamnd/gitview/issues) with
the page URL shape (no need for the repo itself) and the log lines.
