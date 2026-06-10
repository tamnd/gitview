package local

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/gitview/backend"
)

// Blame parses `git blame --porcelain`. The porcelain stream emits, per
// line, a "<sha> <orig> <final> [<n>]" header where <n> appears only on the
// first line of a hunk, followed by key-value commit fields the first time
// a commit is seen, then the content line prefixed with a tab.
func (r *Repo) Blame(ctx context.Context, rev, pth string) ([]backend.BlameHunk, error) {
	rev, pth, err := checkRevPath(rev, pth)
	if err != nil {
		return nil, err
	}
	if pth == "" {
		return nil, fmt.Errorf("blame needs a path: %w", backend.ErrNotFound)
	}
	out, err := r.run(ctx, "blame", "--porcelain", rev, "--", pth)
	if err != nil {
		return nil, fmt.Errorf("blame %s:%s: %w", rev, pth, backend.ErrNotFound)
	}
	return parseBlame(out), nil
}

// parseBlame consumes the porcelain stream. It is a pure function over the
// bytes so the fuzz target can drive it without a repository.
func parseBlame(out []byte) []backend.BlameHunk {
	commits := make(map[string]*blameCommit)
	var hunks []backend.BlameHunk
	var cur *blameCommit

	lines := strings.Split(string(out), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		if line[0] == '\t' {
			continue // content line
		}
		f := strings.Fields(line)
		if len(f) >= 3 && isHex(f[0]) && len(f[0]) >= 40 {
			sha := f[0]
			c := commits[sha]
			if c == nil {
				c = &blameCommit{}
				c.commit.SHA = sha
				commits[sha] = c
			}
			cur = c
			if len(f) == 4 { // first line of a hunk
				final, _ := strconv.Atoi(f[2])
				n, _ := strconv.Atoi(f[3])
				hunks = append(hunks, backend.BlameHunk{StartLine: final, Lines: n})
				c.hunkIdx = append(c.hunkIdx, len(hunks)-1)
			}
			continue
		}
		if cur == nil {
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "author":
			cur.commit.Author.Name = val
		case "author-mail":
			cur.commit.Author.Email = strings.Trim(val, "<>")
		case "author-time":
			if sec, err := strconv.ParseInt(val, 10, 64); err == nil {
				cur.commit.Author.Date = time.Unix(sec, 0).UTC()
			}
		case "committer":
			cur.commit.Committer.Name = val
		case "committer-mail":
			cur.commit.Committer.Email = strings.Trim(val, "<>")
		case "committer-time":
			if sec, err := strconv.ParseInt(val, 10, 64); err == nil {
				cur.commit.Committer.Date = time.Unix(sec, 0).UTC()
			}
		case "summary":
			cur.commit.Subject = val
		case "previous":
			if prev, _, ok := strings.Cut(val, " "); ok {
				cur.prev = prev
			} else {
				cur.prev = val
			}
		}
	}

	for _, c := range commits {
		for _, idx := range c.hunkIdx {
			hunks[idx].Commit = c.commit
			hunks[idx].Prev = c.prev
		}
	}
	return hunks
}

type blameCommit struct {
	commit  backend.Commit
	prev    string
	hunkIdx []int
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return len(s) > 0
}
