---
title: "Backends"
linkTitle: "Backends"
description: "Local git, GitHub, and Hugging Face targets: how each works, tokens, and what degrades where."
weight: 30
featured: true
---

gitview reads repositories through three backends. The UI is identical
everywhere; what differs is how the data is fetched and which features the
upstream API can afford. When a backend cannot support a feature, gitview
hides that button or renders a clear not-supported page rather than failing
sideways.

| | Local | GitHub | Hugging Face |
|---|---|---|---|
| Repo home, tree, blobs | yes | yes | yes |
| Last-commit column | yes | no | yes |
| Commits list | yes | yes | yes |
| Per-file history | yes | yes | no |
| Commit diff pages | yes | yes | no |
| Blame | yes | no | no |
| Archive downloads | yes | yes | no |
| File finder | yes | yes | yes |
