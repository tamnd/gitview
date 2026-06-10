---
title: "Finder and shortcuts"
description: "The t file finder, fuzzy matching, and the complete keyboard map."
weight: 40
---

## The file finder

Press `t` on any repository page. You get a full-screen list of every file
at the current ref with a filter box focused and waiting. Type a few
characters of any part of the path; the match is fuzzy and subsequence-based,
so `svtjs` finds `server/templates/view.js`. Matched characters are
highlighted in each candidate.

| Key | In the finder |
|---|---|
| type | filter the list |
| `↓` / `↑` | move the selection |
| `Enter` | open the selected file |
| `Esc` | back to where you were |

The list is the real file inventory at your ref, so the finder works the
same on a branch, a tag, or a 6-year-old commit SHA.

## The shortcut map

| Key | Where | What |
|---|---|---|
| `t` | any repo page | open the file finder at the current ref |
| `y` | blob and tree pages | rewrite the URL into a SHA permalink |
| `b` | blob pages | open blame for this file |
| `?` | anywhere | show the shortcuts dialog |
| `Esc` | dialogs and the finder | close |

These are the github.com bindings, on purpose. Shortcuts never fire while
you are typing in an input, and everything they do is also reachable by
mouse, so nothing is keyboard-only.

## Themes

gitview follows your system's light or dark preference automatically and
remembers a manual override per browser. The toggle is in the footer. Both
themes are GitHub's actual color tokens, which is why screenshots are
indistinguishable from the original.
