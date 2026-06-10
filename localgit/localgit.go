// Package localgit implements backend.Repo for a repository on disk by
// driving the git command line. Every call is one or two short-lived git
// child processes; git itself is safe for concurrent readers, so the type
// holds no locks beyond a small cache for recursive file listings.
package localgit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamnd/gitview/backend"
)

// Repo drives git for one repository.
type Repo struct {
	dir  string // worktree or bare repo path as given
	info backend.Info

	mu        sync.Mutex
	fileCache map[string][]string // rev -> sorted paths, bounded
}

// New validates dir as a git repository and derives its identity.
func New(ctx context.Context, dir string) (*Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	r := &Repo{dir: abs, fileCache: make(map[string][]string)}
	if _, err := r.run(ctx, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("%s is not a git repository: %w", dir, backend.ErrNotFound)
	}
	r.info = r.deriveInfo(ctx, abs)
	return r, nil
}

func (r *Repo) deriveInfo(ctx context.Context, abs string) backend.Info {
	info := backend.Info{Owner: "local", DefaultBranch: "main", CloneURL: abs}
	name := strings.TrimSuffix(filepath.Base(abs), ".git")
	info.Name = name

	if out, err := r.run(ctx, "remote", "get-url", "origin"); err == nil {
		url := strings.TrimSpace(string(out))
		if url != "" {
			info.CloneURL = url
			if owner, repo, ok := parseRemote(url); ok {
				info.Owner, info.Name = owner, repo
			}
		}
	}
	if out, err := r.run(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		info.DefaultBranch = strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
	} else if out, err := r.run(ctx, "symbolic-ref", "--short", "HEAD"); err == nil {
		info.DefaultBranch = strings.TrimSpace(string(out))
	}
	if gd, err := r.run(ctx, "rev-parse", "--absolute-git-dir"); err == nil {
		desc, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(gd)), "description"))
		if err == nil {
			d := strings.TrimSpace(string(desc))
			if d != "" && !strings.HasPrefix(d, "Unnamed repository") {
				info.Description = d
			}
		}
	}
	return info
}

// parseRemote extracts owner/name from https and scp-like remote URLs.
func parseRemote(url string) (owner, name string, ok bool) {
	s := strings.TrimSuffix(url, "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if j := strings.IndexByte(s, '/'); j >= 0 {
			s = s[j+1:]
		} else {
			return "", "", false
		}
	} else if i := strings.IndexByte(s, ':'); i >= 0 && !strings.Contains(s[:i], "/") {
		s = s[i+1:] // git@host:owner/repo
	} else {
		return "", "", false
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner, name = parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || name == "" {
		return "", "", false
	}
	return owner, name, true
}

func (r *Repo) Info(ctx context.Context) (backend.Info, error) {
	return r.info, nil
}

func (r *Repo) Refs(ctx context.Context) (backend.Refs, error) {
	out, err := r.run(ctx, "for-each-ref", "refs/heads", "refs/tags",
		"--format=%(refname)%00%(refname:short)%00%(objectname)%00%(*objectname)%00%(creatordate:iso-strict)")
	if err != nil {
		return backend.Refs{}, err
	}
	var refs backend.Refs
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x00")
		if len(f) != 5 {
			return backend.Refs{}, fmt.Errorf("unexpected for-each-ref record %q", line)
		}
		sha := f[2]
		if f[3] != "" {
			sha = f[3] // peeled commit for annotated tags
		}
		date, _ := time.Parse(time.RFC3339, f[4])
		ref := backend.Ref{Name: f[1], SHA: sha, Date: date}
		switch {
		case strings.HasPrefix(f[0], "refs/heads/"):
			refs.Branches = append(refs.Branches, ref)
		case strings.HasPrefix(f[0], "refs/tags/"):
			refs.Tags = append(refs.Tags, ref)
		}
	}
	sort.SliceStable(refs.Tags, func(i, j int) bool {
		return refs.Tags[i].Date.After(refs.Tags[j].Date)
	})
	return refs, nil
}

var hexRe = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// Resolve maps a ref name or hex prefix to a full commit SHA. Branches win
// over tags, matching github.com, and revision-walk syntax is rejected.
func (r *Repo) Resolve(ctx context.Context, ref string) (string, error) {
	if err := checkRefName(ref); err != nil {
		return "", err
	}
	for _, full := range []string{"refs/heads/" + ref, "refs/tags/" + ref} {
		if out, err := r.run(ctx, "rev-parse", "--verify", "--quiet", full+"^{commit}"); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	if hexRe.MatchString(strings.ToLower(ref)) {
		if out, err := r.run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return "", fmt.Errorf("ref %q: %w", ref, backend.ErrNotFound)
}

func (r *Repo) Tree(ctx context.Context, rev, pth string) ([]backend.TreeEntry, error) {
	rev, pth, err := checkRevPath(rev, pth)
	if err != nil {
		return nil, err
	}
	out, err := r.run(ctx, "ls-tree", "-z", "-l", treeish(rev, pth))
	if err != nil {
		return nil, fmt.Errorf("tree %s:%s: %w", rev, pth, backend.ErrNotFound)
	}
	var entries []backend.TreeEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		// <mode> SP <type> SP <object> SP+ <size> TAB <name>
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("unexpected ls-tree record %q", rec)
		}
		meta := strings.Fields(string(rec[:tab]))
		if len(meta) != 4 {
			return nil, fmt.Errorf("unexpected ls-tree record %q", rec)
		}
		name := string(rec[tab+1:])
		e := backend.TreeEntry{Name: name, Path: path.Join(pth, name), Mode: meta[0], Size: -1}
		switch {
		case meta[0] == "160000" || meta[1] == "commit":
			e.Kind = backend.KindSubmodule
		case meta[1] == "tree":
			e.Kind = backend.KindDir
		case meta[0] == "120000":
			e.Kind = backend.KindSymlink
		default:
			e.Kind = backend.KindFile
		}
		if n, err := strconv.ParseInt(meta[3], 10, 64); err == nil {
			e.Size = n
		}
		entries = append(entries, e)
	}
	sortEntries(entries)
	return entries, nil
}

// sortEntries orders dirs and submodules first, then files and symlinks,
// case-insensitive within each group, the way github.com lists them.
func sortEntries(entries []backend.TreeEntry) {
	group := func(k backend.EntryKind) int {
		if k == backend.KindDir || k == backend.KindSubmodule {
			return 0
		}
		return 1
	}
	sort.SliceStable(entries, func(i, j int) bool {
		gi, gj := group(entries[i].Kind), group(entries[j].Kind)
		if gi != gj {
			return gi < gj
		}
		li, lj := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if li != lj {
			return li < lj
		}
		return entries[i].Name < entries[j].Name
	})
}

func (r *Repo) Blob(ctx context.Context, rev, pth string) (backend.Blob, error) {
	rev, pth, err := checkRevPath(rev, pth)
	if err != nil {
		return backend.Blob{}, err
	}
	spec := treeish(rev, pth)
	typ, err := r.run(ctx, "cat-file", "-t", spec)
	if err != nil || strings.TrimSpace(string(typ)) != "blob" {
		return backend.Blob{}, fmt.Errorf("blob %s:%s: %w", rev, pth, backend.ErrNotFound)
	}
	sizeOut, err := r.run(ctx, "cat-file", "-s", spec)
	if err != nil {
		return backend.Blob{}, fmt.Errorf("blob %s:%s: %w", rev, pth, backend.ErrNotFound)
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64)
	b := backend.Blob{Path: pth, Size: size}
	if size > backend.MaxBlobBytes {
		b.TooBig = true
		return b, nil
	}
	content, err := r.run(ctx, "cat-file", "blob", spec)
	if err != nil {
		return backend.Blob{}, fmt.Errorf("blob %s:%s: %w", rev, pth, backend.ErrNotFound)
	}
	b.Content = content
	b.Binary = isBinary(content)
	b.LFS = parseLFSPointer(content)
	return b, nil
}

func isBinary(content []byte) bool {
	head := content
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) >= 0
}

func parseLFSPointer(content []byte) *backend.LFSPointer {
	if !bytes.HasPrefix(content, []byte("version https://git-lfs.github.com/spec/v1")) {
		return nil
	}
	p := &backend.LFSPointer{}
	for _, line := range strings.Split(string(content), "\n") {
		if oid, ok := strings.CutPrefix(line, "oid sha256:"); ok {
			p.OID = strings.TrimSpace(oid)
		}
		if sz, ok := strings.CutPrefix(line, "size "); ok {
			p.Size, _ = strconv.ParseInt(strings.TrimSpace(sz), 10, 64)
		}
	}
	if p.OID == "" {
		return nil
	}
	return p
}

// logFormat carries ten NUL-separated fields per record; bodies may contain
// newlines but never NULs, and parseLog strips the record glue.
const logFormat = "%H%x00%P%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%s%x00%b%x00"

func parseLog(out []byte) ([]backend.Commit, error) {
	fields := strings.Split(string(out), "\x00")
	var commits []backend.Commit
	for i := 0; i+9 < len(fields); i += 10 {
		c := backend.Commit{
			SHA:     strings.TrimLeft(fields[i], "\n"),
			Subject: fields[i+8],
			Body:    strings.TrimRight(fields[i+9], "\n"),
		}
		if fields[i+1] != "" {
			c.Parents = strings.Split(fields[i+1], " ")
		}
		c.Author = backend.Signature{Name: fields[i+2], Email: fields[i+3]}
		c.Author.Date, _ = time.Parse(time.RFC3339, fields[i+4])
		c.Committer = backend.Signature{Name: fields[i+5], Email: fields[i+6]}
		c.Committer.Date, _ = time.Parse(time.RFC3339, fields[i+7])
		commits = append(commits, c)
	}
	return commits, nil
}

func (r *Repo) Commits(ctx context.Context, rev, pth string, n, skip int) ([]backend.Commit, bool, error) {
	rev, pth, err := checkRevPath(rev, pth)
	if err != nil {
		return nil, false, err
	}
	if n <= 0 || n > 100 {
		n = 100
	}
	args := []string{"log", "--format=" + logFormat,
		"--max-count=" + strconv.Itoa(n+1), "--skip=" + strconv.Itoa(skip), rev}
	if pth != "" {
		args = append(args, "--", pth)
	}
	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, false, fmt.Errorf("commits %s: %w", rev, backend.ErrNotFound)
	}
	commits, err := parseLog(out)
	if err != nil {
		return nil, false, err
	}
	more := len(commits) > n
	if more {
		commits = commits[:n]
	}
	return commits, more, nil
}

func (r *Repo) LastCommit(ctx context.Context, rev, pth string) (backend.Commit, error) {
	rev, pth, err := checkRevPath(rev, pth)
	if err != nil {
		return backend.Commit{}, err
	}
	args := []string{"log", "-1", "--format=" + logFormat, rev}
	if pth != "" {
		args = append(args, "--", pth)
	}
	out, err := r.run(ctx, args...)
	if err != nil {
		return backend.Commit{}, fmt.Errorf("last commit %s: %w", rev, backend.ErrNotFound)
	}
	commits, err := parseLog(out)
	if err != nil {
		return backend.Commit{}, err
	}
	if len(commits) == 0 {
		return backend.Commit{}, fmt.Errorf("last commit %s:%s: %w", rev, pth, backend.ErrNotFound)
	}
	return commits[0], nil
}

func (r *Repo) Files(ctx context.Context, rev string) ([]string, error) {
	rev, _, err := checkRevPath(rev, "")
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	cached, ok := r.fileCache[rev]
	r.mu.Unlock()
	if ok {
		return cached, nil
	}
	out, err := r.run(ctx, "ls-tree", "-r", "-z", "--name-only", rev)
	if err != nil {
		return nil, fmt.Errorf("files %s: %w", rev, backend.ErrNotFound)
	}
	var files []string
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) > 0 {
			files = append(files, string(rec))
		}
	}
	sort.Strings(files)
	r.mu.Lock()
	if len(r.fileCache) >= 4 {
		clear(r.fileCache)
	}
	r.fileCache[rev] = files
	r.mu.Unlock()
	return files, nil
}

func (r *Repo) Archive(ctx context.Context, rev, format, prefix string, w io.Writer) error {
	rev, _, err := checkRevPath(rev, "")
	if err != nil {
		return err
	}
	if format != "zip" && format != "tar.gz" {
		return fmt.Errorf("archive format %q: %w", format, backend.ErrUnsupported)
	}
	cmd := r.command(ctx, "archive", "--format="+format, "--prefix="+prefix, rev)
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git archive %s: %v: %s", rev, err, stderr.String())
	}
	return nil
}

// treeish builds the rev:path object spec.
func treeish(rev, pth string) string {
	return rev + ":" + pth
}

// checkRevPath validates inputs before they reach git argv.
func checkRevPath(rev, pth string) (string, string, error) {
	if err := checkRefName(rev); err != nil {
		return "", "", err
	}
	if pth == "" {
		return rev, "", nil
	}
	clean := path.Clean(pth)
	if clean == "." {
		clean = ""
	}
	if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", fmt.Errorf("path %q: %w", pth, backend.ErrNotFound)
	}
	return rev, clean, nil
}

// checkRefName rejects revision-walk syntax and flag-shaped input.
func checkRefName(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "~^:?*[\\ \t\n@{") {
		return fmt.Errorf("ref %q: %w", ref, backend.ErrNotFound)
	}
	return nil
}

// command builds a git invocation rooted at the repository.
func (r *Repo) command(ctx context.Context, args ...string) *exec.Cmd {
	full := append([]string{"-c", "core.quotepath=off"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = time.Second
	return cmd
}

// run executes git and returns stdout, with stderr folded into the error.
func (r *Repo) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := r.command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
