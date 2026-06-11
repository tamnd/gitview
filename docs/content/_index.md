---
title: "gitview"
description: "Browse any git repository in your browser with a UI that looks and feels like github.com. One binary, read only, no setup."
heroTitle: "The github.com code browser, for any repository"
heroLead: "gitview is a single read-only binary that serves the github.com browsing experience for local repositories, GitHub repositories, and Hugging Face repos. Point it at a path or a URL and start reading code."
heroPrimaryURL: "/getting-started/quick-start/"
heroPrimaryText: "Get started"
---

You know how to read code on github.com: the file table, the README under it,
syntax-highlighted blobs with line anchors, commits, diffs, blame, the `t`
finder. gitview gives you that exact experience for repositories that are not
on github.com, or not online at all.

```bash
gitview                                    # the repo you are standing in
gitview ~/src/linux                        # any local repo, bare repos included
gitview ~/src                              # a whole folder of repos, with an index
gitview gh:torvalds/linux                  # a repo on github.com, over the API
gitview hf:openai-community/gpt2           # a model on huggingface.co
```

Then open <http://127.0.0.1:9419> and browse. The URL scheme matches
github.com exactly, so the muscle memory and the deep links transfer: swap the
host on a `/blob/main/...#L42` link and it just works.

gitview is deliberately small. It covers code browsing and nothing else: no
issues, no pull requests, no accounts, no editing, and no write operations of
any kind, ever. It is one static Go binary that shells out to `git` for local
repositories and speaks the public HTTP APIs for remote ones, renders
everything server-side, and ships its assets embedded. Start it, read code,
ctrl-C it, forget it was running.
