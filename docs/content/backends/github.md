---
title: "GitHub repositories"
description: "Browse any github.com repo over the REST API: targets, tokens, rate limits, and what degrades."
weight: 20
---

The GitHub backend browses a repository on github.com without cloning it.
gitview talks to the REST API on demand and caches conditionally, so
revisiting pages costs almost nothing.

## Targets

```bash
gitview gh:torvalds/linux
gitview https://github.com/torvalds/linux
gitview https://github.com/torvalds/linux/tree/master/kernel   # deep links work
```

A pasted URL can carry any deeper path; gitview takes the owner and repo and
serves the whole repository. Both forms accept private repositories when
your token can see them.

## Tokens and rate limits

Anonymous GitHub API access allows 60 requests per hour, which browsing
chews through quickly. With a token you get 5,000:

```bash
export GITHUB_TOKEN=ghp_yourtoken
gitview gh:yourorg/private-repo
```

or `-token ghp_yourtoken` if you prefer a flag (the flag wins when both are
set). Any classic or fine-grained token with read access to the repository
works. The token stays in memory, goes only to `api.github.com` over TLS,
and never appears in logs, error pages, or URLs.

gitview also plays nice with the quota it has: responses are cached and
revalidated with ETags, so a page you reload costs a 304, not a fresh
request. If you do hit the limit, gitview says so plainly and shows when the
quota resets instead of erroring cryptically.

## What works and what degrades

Everything on the [feature matrix](/backends/) marked yes: home, trees,
blobs, the finder, commit lists, per-file history, full commit diffs, and
archive downloads (streamed from GitHub's own archive service).

Two things degrade, both because the REST API has no affordable call for
them:

- **The last-commit column** on file tables stays empty; filling it would
  cost one API request per row.
- **Blame** is not offered; the button never appears.

Everything else is the full experience.
