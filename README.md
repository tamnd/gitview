# gitview

Browse any git repository in your browser with a UI that looks and feels like
github.com. One binary, no setup, read only.

```
gitview                                    # serve the repo in the current directory
gitview ~/src/linux                        # any local repo, bare repos included
gitview ~/src                              # a folder of repos, with an index page
gitview https://github.com/torvalds/linux  # browse a remote GitHub repo
gitview hf:Qwen/Qwen3-8B                   # browse a Hugging Face repo
```

Then open http://127.0.0.1:9419.

Full documentation lives at [gitview.tamnd.com](https://gitview.tamnd.com).

gitview covers the code browsing surfaces and nothing else: repo home with the
file table and rendered README, tree and blob views with syntax highlighting
and line anchors, file previews (images, audio, video, pdf, docx, csv,
parquet), commits, commit diffs, branches, tags, blame, archive
downloads, the `t` file finder, light and dark themes, and the same URL scheme
as github.com so deep links transfer by swapping the host. There are no
issues, pull requests, accounts, or write operations, and there never will be.

## Install

```
go install github.com/tamnd/gitview/cmd/gitview@latest
```

The local backend shells out to `git`, which you already have.

## Usage

```
gitview [flags] [target]

  target            local path (default "."), a directory of repos,
                    https://github.com/owner/repo or gh:owner/repo,
                    huggingface.co URL or hf:[datasets/|spaces/]owner/name

  -addr string      listen address (default "127.0.0.1:9419")
  -open             open the browser after starting
  -token string     GitHub token for the GitHub backend (or GITHUB_TOKEN)
  -hf-token string  Hugging Face token for the Hub backend (or HF_TOKEN)
  -dev              verbose error pages and template reload
  -version          print version and exit
```

Keyboard shortcuts match GitHub: `t` opens the file finder, `y` turns the
current URL into a permalink, `b` opens blame on a file, `?` lists them.

## Notes

- The GitHub backend works without a token at 60 requests/hour; set
  `GITHUB_TOKEN` for 5000. Blame and the per-file commit columns are not
  available on the GitHub backend yet, the REST API has no cheap way to get
  them.
- The Hugging Face backend handles models, datasets, and Spaces. Gated and
  private repos need `HF_TOKEN`. Commit diffs, blame, and archives are not
  available there; the Hub API does not expose them.
- gitview binds to loopback by default. It has no authentication, so think
  before passing `-addr 0.0.0.0`.

## License

MIT
