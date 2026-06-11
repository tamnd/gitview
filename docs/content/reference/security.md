---
title: "Security"
description: "Read-only by construction, loopback by default, strict rendering, careful with tokens."
weight: 30
---

gitview's security story is short because the design does the work.

## Read-only by construction

There is no write path. The local backend invokes git with read subcommands
only; remote backends call read endpoints only; the server rejects non-GET
methods. No flag, header, or request can change a repository through
gitview.

## Loopback by default

The default listen address is `127.0.0.1:9419`, reachable only from your
machine. That is the entire access-control story: there are no users or
passwords because by default there is no one to control.

Binding a non-loopback address is an explicit choice:

```bash
gitview -addr 0.0.0.0:9419 ~/src
```

and gitview prints an unmissable warning, because it means anyone who can
reach that interface can read every served repository and download archives
of any commit. If you want a network-facing viewer with authentication, put
a reverse proxy with auth in front; gitview will not grow its own.

## Rendering is hostile-input safe

Repository content is untrusted input and is treated that way:

- Markdown and HTML are sanitized with a strict allowlist; scripts, event
  handlers, and `javascript:` URLs do not survive, even via raw HTML.
- Pages ship a strict Content-Security-Policy; there is no inline script
  except a hashed theme bootstrap.
- SVGs render sandboxed, where they cannot run scripts.
- File previews (csv, parquet, docx) build their HTML from escaped text
  only; a hostile cell or document run renders as text. Parsers are
  capped (8 MB of inflate for docx, fixed row and cell limits for
  tables), so a crafted file costs bounded work.
- Path traversal is rejected before routing; hostile file names, branch
  names, and commit messages render as text, not as markup or flags.

One deliberate exception is documented here plainly: raw responses
normally carry `Content-Security-Policy: default-src 'none'; sandbox`,
but for `application/pdf` the sandbox keyword is dropped, because
Chromium refuses to start its PDF viewer inside a CSP-sandboxed
document. PDF raw responses carry `frame-ancestors 'self'` instead, so
other sites cannot embed them. PDFs render in the browser's isolated
viewer process and gitview holds no cookies or credentials a PDF could
reach; the residual exposure of opening a hostile PDF is the same one
your file manager gives you.

## Tokens

`-token` and `-hf-token` values stay in process memory, are sent only to
their own service over TLS, and never appear in logs, error messages, or
rendered pages. The two flags are separate precisely so a GitHub credential
can never be sent to huggingface.co, or the reverse.

## What gitview sees

Nothing leaves your machine except the API calls a remote backend needs.
There is no telemetry, no update check, no analytics. Local browsing
generates no network traffic at all.
