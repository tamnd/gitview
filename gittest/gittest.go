// Package gittest builds throwaway git repositories for tests. Commits are
// deterministic: fixed author, committer, and timezone, with timestamps
// advancing one minute per commit.
package gittest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Repo is a scratch repository rooted in a test temp directory.
type Repo struct {
	t    *testing.T
	dir  string
	tick time.Time
}

// New creates an empty repository on branch main.
func New(t *testing.T) *Repo {
	t.Helper()
	r := &Repo{
		t:    t,
		dir:  t.TempDir(),
		tick: time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC),
	}
	r.Git("init", "-q", "-b", "main")
	return r
}

// Path returns the repository root.
func (r *Repo) Path() string { return r.dir }

// Git runs a git command in the repository and returns trimmed stdout.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Ada Lovelace",
		"GIT_AUTHOR_EMAIL=ada@example.com",
		"GIT_COMMITTER_NAME=Ada Lovelace",
		"GIT_COMMITTER_EMAIL=ada@example.com",
		"GIT_AUTHOR_DATE="+r.tick.Format(time.RFC3339),
		"GIT_COMMITTER_DATE="+r.tick.Format(time.RFC3339),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"TZ=UTC",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write creates or replaces a file, creating parent directories.
func (r *Repo) Write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Symlink creates a symbolic link at path pointing at target.
func (r *Repo) Symlink(target, path string) {
	r.t.Helper()
	full := filepath.Join(r.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Symlink(target, full); err != nil {
		r.t.Fatal(err)
	}
}

// Remove deletes a file from the worktree.
func (r *Repo) Remove(path string) {
	r.t.Helper()
	if err := os.Remove(filepath.Join(r.dir, filepath.FromSlash(path))); err != nil {
		r.t.Fatal(err)
	}
}

// Commit stages everything and commits, returning the commit SHA.
// The clock advances one minute per commit.
func (r *Repo) Commit(msg string) string {
	r.t.Helper()
	r.tick = r.tick.Add(time.Minute)
	r.Git("add", "-A")
	r.Git("-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", msg)
	return r.Git("rev-parse", "HEAD")
}

// Branch creates a branch at HEAD and checks it out.
func (r *Repo) Branch(name string) {
	r.t.Helper()
	r.Git("checkout", "-q", "-b", name)
}

// Checkout switches branches.
func (r *Repo) Checkout(name string) {
	r.t.Helper()
	r.Git("checkout", "-q", name)
}

// Tag creates a lightweight tag at HEAD.
func (r *Repo) Tag(name string) {
	r.t.Helper()
	r.tick = r.tick.Add(time.Minute)
	r.Git("tag", name)
}

// AnnotatedTag creates an annotated tag at HEAD.
func (r *Repo) AnnotatedTag(name, msg string) {
	r.t.Helper()
	r.tick = r.tick.Add(time.Minute)
	r.Git("-c", "tag.gpgsign=false", "tag", "-a", name, "-m", msg)
}

// Merge merges the named branch into the current one with a merge commit.
func (r *Repo) Merge(name string) string {
	r.t.Helper()
	r.tick = r.tick.Add(time.Minute)
	r.Git("-c", "commit.gpgsign=false", "merge", "-q", "--no-ff", "--no-edit", name)
	return r.Git("rev-parse", "HEAD")
}

// Rename moves a file with git mv.
func (r *Repo) Rename(from, to string) {
	r.t.Helper()
	r.Git("mv", from, to)
}

// Bare clones the repository into a new bare directory and returns its path.
func (r *Repo) Bare() string {
	r.t.Helper()
	dest := filepath.Join(r.t.TempDir(), "bare.git")
	r.Git("clone", "-q", "--bare", r.dir, dest)
	return dest
}

// SetOrigin points the origin remote at url without fetching.
func (r *Repo) SetOrigin(url string) {
	r.t.Helper()
	r.Git("remote", "add", "origin", url)
}

// WriteBinary writes len bytes including NULs so the file sniffs as binary.
func (r *Repo) WriteBinary(path string, size int) {
	r.t.Helper()
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i % 7) // includes NULs
	}
	full := filepath.Join(r.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, buf, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// LFSPointer writes a git-lfs pointer file at path.
func (r *Repo) LFSPointer(path string, size int64) {
	r.t.Helper()
	oid := strings.Repeat("ab", 32)
	r.Write(path, fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, size))
}
