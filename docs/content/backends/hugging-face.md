---
title: "Hugging Face repositories"
description: "Browse models, datasets, and Spaces from the Hub like code: targets, tokens, LFS, and what degrades."
weight: 30
---

Every Hugging Face repository is a git repository, and gitview treats it
like one. Configs, tokenizers, READMEs, training scripts, dataset loaders:
all of it browses like code, with the Hub's multi-gigabyte weights showing
up as tidy LFS banners instead of downloads.

## Targets

```bash
gitview hf:openai-community/gpt2              # a model
gitview hf:datasets/squad/squad               # a dataset
gitview hf:spaces/gradio/hello_world          # a Space
gitview https://huggingface.co/openai-community/gpt2   # URLs work too
```

The optional `datasets/` or `spaces/` segment picks the repository kind;
without it gitview assumes a model. Pasted URLs may carry deeper paths
(`/tree/main/...`), which are ignored the same way GitHub URLs are. Legacy
rootless ids like `gpt2` are not accepted; use the canonical `owner/name`
form the Hub redirects to anyway.

## Tokens and gated repos

Public repositories need no token. For private repositories, and for gated
models where you have accepted the terms, pass a Hub token:

```bash
export HF_TOKEN=hf_yourtoken
gitview hf:meta-llama/Llama-3.1-8B
```

or `-hf-token hf_yourtoken`. The flag is deliberately separate from the
GitHub `-token` so one service's credential is never sent to the other.
Without a valid token, a gated or private repository looks like it does not
exist, which is exactly what the Hub tells anonymous callers.

## LFS done right

Model weights live in git LFS. gitview reads the pointer metadata from the
Hub API, so a `model.safetensors` row shows its true size (the gigabytes,
not the 134-byte pointer), and opening it renders the LFS banner with the
object id and size. The actual object is never fetched; you can browse a
70B checkpoint repository on hotel wifi.

## What works and what degrades

Home, trees, blobs, the finder, commit lists, and the last-commit column on
file tables all work; the Hub's API was practically designed for that
column. Four things are not offered, because the Hub has no endpoint for
them:

- **Commit diff pages.** The commit list renders; subjects are not links to
  diffs.
- **Blame.**
- **Per-file history** (the `History` button on a single file).
- **Archive downloads.**

Each absent feature is a hidden button or a clear not-supported page, never
a broken one.
