package localgit

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tamnd/gitview/backend"
)

// Commit returns one commit with its diff against the first parent, or
// against the empty tree for a root commit, the way github.com renders
// commit pages.
func (r *Repo) Commit(ctx context.Context, sha string) (backend.CommitDetail, error) {
	full, err := r.Resolve(ctx, sha)
	if err != nil {
		return backend.CommitDetail{}, err
	}
	out, err := r.run(ctx, "show", "-s", "--format="+logFormat, full)
	if err != nil {
		return backend.CommitDetail{}, fmt.Errorf("commit %s: %w", sha, backend.ErrNotFound)
	}
	commits, err := parseLog(out)
	if err != nil || len(commits) == 0 {
		return backend.CommitDetail{}, fmt.Errorf("commit %s: %w", sha, backend.ErrNotFound)
	}
	detail := backend.CommitDetail{Commit: commits[0]}

	diffArgs := []string{"diff-tree", "--patch", "--find-renames", "--no-color"}
	if len(detail.Parents) == 0 {
		diffArgs = append(diffArgs, "--root", full)
	} else {
		diffArgs = append(diffArgs, detail.Parents[0], full)
	}
	patch, err := r.run(ctx, diffArgs...)
	if err != nil {
		return backend.CommitDetail{}, err
	}
	detail.Files = backend.ParsePatch(string(patch))

	// numstat is authoritative for counts: it covers binary files (as "-")
	// and files whose hunks were dropped for size.
	numArgs := []string{"diff-tree", "--numstat", "-z", "--find-renames"}
	if len(detail.Parents) == 0 {
		numArgs = append(numArgs, "--root", full)
	} else {
		numArgs = append(numArgs, detail.Parents[0], full)
	}
	numOut, err := r.run(ctx, numArgs...)
	if err != nil {
		return backend.CommitDetail{}, err
	}
	applyNumstat(string(numOut), detail.Files)

	for i := range detail.Files {
		detail.Stats.Additions += detail.Files[i].Additions
		detail.Stats.Deletions += detail.Files[i].Deletions
	}
	detail.Stats.FilesChanged = len(detail.Files)
	return detail, nil
}

// applyNumstat parses `--numstat -z` records and overwrites per-file counts.
// Record shapes: "added\tdeleted\tpath\x00" for plain entries and
// "added\tdeleted\t\x00old\x00new\x00" for renames and copies.
func applyNumstat(out string, files []backend.FileDiff) {
	byPath := make(map[string]*backend.FileDiff, len(files))
	for i := range files {
		byPath[files[i].NewPath] = &files[i]
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(fields) {
			path = fields[i+2] // rename: old, then new
			i += 2
		}
		f, ok := byPath[path]
		if !ok {
			continue
		}
		if a, err := strconv.Atoi(parts[0]); err == nil {
			f.Additions = a
		}
		if d, err := strconv.Atoi(parts[1]); err == nil {
			f.Deletions = d
		}
	}
}

// CommitCount reports how many commits are reachable from rev. The server
// uses this for the "N commits" header link via an optional interface.
func (r *Repo) CommitCount(ctx context.Context, rev string) (int, error) {
	rev, _, err := checkRevPath(rev, "")
	if err != nil {
		return 0, err
	}
	out, err := r.run(ctx, "rev-list", "--count", rev)
	if err != nil {
		return 0, fmt.Errorf("commit count %s: %w", rev, backend.ErrNotFound)
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}
