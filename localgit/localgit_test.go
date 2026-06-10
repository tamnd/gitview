package localgit

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tamnd/gitview/backend"
	"github.com/tamnd/gitview/gittest"
)

func fixture(t *testing.T) (*gittest.Repo, *Repo) {
	t.Helper()
	g := gittest.New(t)
	g.Write("README.md", "# fixture\n\nhello\n")
	g.Write("main.go", "package main\n\nfunc main() {}\n")
	g.Write("docs/guide.md", "guide v1\n")
	g.Commit("Initial commit")
	g.Write("main.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(1) }\n")
	g.Commit("Print something")
	r, err := New(context.Background(), g.Path())
	if err != nil {
		t.Fatal(err)
	}
	return g, r
}

func TestNewRejectsNonRepo(t *testing.T) {
	if _, err := New(context.Background(), t.TempDir()); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestInfo(t *testing.T) {
	g, r := fixture(t)
	info, err := r.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Owner != "local" || info.DefaultBranch != "main" {
		t.Errorf("info: %+v", info)
	}
	if info.CloneURL != g.Path() {
		t.Errorf("clone url: %q", info.CloneURL)
	}
}

func TestInfoFromOrigin(t *testing.T) {
	g := gittest.New(t)
	g.Write("a.txt", "a\n")
	g.Commit("Initial commit")
	g.SetOrigin("git@github.com:tamnd/gitview.git")
	r, err := New(context.Background(), g.Path())
	if err != nil {
		t.Fatal(err)
	}
	info, _ := r.Info(context.Background())
	if info.Owner != "tamnd" || info.Name != "gitview" {
		t.Errorf("identity from origin: %+v", info)
	}
}

func TestParseRemote(t *testing.T) {
	cases := []struct{ url, owner, name string }{
		{"git@github.com:tamnd/gitview.git", "tamnd", "gitview"},
		{"https://github.com/tamnd/gitview", "tamnd", "gitview"},
		{"https://github.com/tamnd/gitview.git", "tamnd", "gitview"},
		{"ssh://git@github.com/tamnd/gitview.git", "tamnd", "gitview"},
		{"https://gitlab.com/group/sub/repo.git", "sub", "repo"},
	}
	for _, c := range cases {
		owner, name, ok := parseRemote(c.url)
		if !ok || owner != c.owner || name != c.name {
			t.Errorf("parseRemote(%q) = %q/%q %v", c.url, owner, name, ok)
		}
	}
	if _, _, ok := parseRemote("/plain/local/path"); ok {
		t.Error("local path should not parse as remote")
	}
}

func TestRefsAndResolve(t *testing.T) {
	g, r := fixture(t)
	g.Branch("feature/x/y")
	g.Write("feat.txt", "f\n")
	featSHA := g.Commit("Feature work")
	g.Checkout("main")
	g.AnnotatedTag("v1.0.0", "release v1")
	ctx := context.Background()

	refs, err := r.Refs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range refs.Branches {
		names = append(names, b.Name)
	}
	if !contains(names, "main") || !contains(names, "feature/x/y") {
		t.Errorf("branches: %v", names)
	}
	if len(refs.Tags) != 1 || refs.Tags[0].Name != "v1.0.0" {
		t.Fatalf("tags: %+v", refs.Tags)
	}

	mainSHA, err := r.Resolve(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	// The annotated tag must peel to the commit, and equal main's tip.
	if refs.Tags[0].SHA != mainSHA {
		t.Errorf("tag not peeled: %s vs %s", refs.Tags[0].SHA, mainSHA)
	}
	if sha, err := r.Resolve(ctx, "feature/x/y"); err != nil || sha != featSHA {
		t.Errorf("slashed branch: %s %v", sha, err)
	}
	if sha, err := r.Resolve(ctx, "v1.0.0"); err != nil || sha != mainSHA {
		t.Errorf("tag resolve: %s %v", sha, err)
	}
	if sha, err := r.Resolve(ctx, mainSHA[:8]); err != nil || sha != mainSHA {
		t.Errorf("sha prefix: %s %v", sha, err)
	}
	for _, bad := range []string{"main~1", "main^", "main:file", "--exec=x", "", "nope"} {
		if _, err := r.Resolve(ctx, bad); !errors.Is(err, backend.ErrNotFound) {
			t.Errorf("Resolve(%q) should fail with ErrNotFound, got %v", bad, err)
		}
	}
}

func TestTree(t *testing.T) {
	g, r := fixture(t)
	g.Symlink("README.md", "link.md")
	pinned := strings.Repeat("a", 40)
	g.Submodule("deps", pinned)
	g.Commit("Add symlink and submodule")
	ctx := context.Background()
	rev, _ := r.Resolve(ctx, "main")

	entries, err := r.Tree(ctx, rev, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
		if e.SHA == "" {
			t.Errorf("%s: empty SHA", e.Name)
		}
	}
	// dirs and submodules first, then files case-insensitively
	want := []string{"deps", "docs", "link.md", "main.go", "README.md"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order: %v want %v", names, want)
	}
	if entries[0].Kind != backend.KindSubmodule {
		t.Errorf("deps kind: %v", entries[0].Kind)
	}
	if entries[0].SHA != pinned {
		t.Errorf("deps SHA: %s want %s", entries[0].SHA, pinned)
	}
	if entries[1].Kind != backend.KindDir {
		t.Errorf("docs kind: %v", entries[1].Kind)
	}
	if entries[2].Kind != backend.KindSymlink {
		t.Errorf("link kind: %v", entries[2].Kind)
	}
	if entries[3].Size <= 0 {
		t.Errorf("main.go size: %d", entries[3].Size)
	}

	sub, err := r.Tree(ctx, rev, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 1 || sub[0].Path != "docs/guide.md" {
		t.Errorf("subdir: %+v", sub)
	}

	if _, err := r.Tree(ctx, rev, "nope"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("missing dir: %v", err)
	}
	if _, err := r.Tree(ctx, rev, "../escape"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("traversal: %v", err)
	}
}

func TestBlob(t *testing.T) {
	g, r := fixture(t)
	g.WriteBinary("blob.bin", 256)
	g.LFSPointer("big.dat", 123456789)
	g.Commit("Add binaries")
	ctx := context.Background()
	rev, _ := r.Resolve(ctx, "main")

	b, err := r.Blob(ctx, rev, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if b.Binary || b.TooBig || !bytes.Contains(b.Content, []byte("package main")) {
		t.Errorf("blob: %+v", b)
	}
	if b.Size != int64(len(b.Content)) {
		t.Errorf("size mismatch: %d vs %d", b.Size, len(b.Content))
	}

	bin, err := r.Blob(ctx, rev, "blob.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bin.Binary {
		t.Error("binary not detected")
	}

	lfs, err := r.Blob(ctx, rev, "big.dat")
	if err != nil {
		t.Fatal(err)
	}
	if lfs.LFS == nil || lfs.LFS.Size != 123456789 {
		t.Errorf("lfs pointer: %+v", lfs.LFS)
	}
	if b.Symlink || bin.Symlink || lfs.Symlink {
		t.Error("Symlink set on a regular file")
	}

	// Directories are not blobs.
	if _, err := r.Blob(ctx, rev, "docs"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("dir as blob: %v", err)
	}
	if _, err := r.Blob(ctx, rev, "absent.txt"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("missing blob: %v", err)
	}
}

func TestBlobSymlink(t *testing.T) {
	g, r := fixture(t)
	g.Symlink("main.go", "link.go")
	g.Submodule("deps", strings.Repeat("b", 40))
	g.Commit("Add symlink and submodule")
	ctx := context.Background()
	rev, _ := r.Resolve(ctx, "main")

	b, err := r.Blob(ctx, rev, "link.go")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Symlink {
		t.Error("Symlink not set")
	}
	if string(b.Content) != "main.go" {
		t.Errorf("target: %q", b.Content)
	}
	if b.Size != int64(len("main.go")) {
		t.Errorf("size: %d", b.Size)
	}

	// A submodule path is not a blob either.
	if _, err := r.Blob(ctx, rev, "deps"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("submodule as blob: %v", err)
	}
}

func TestCommitsAndLastCommit(t *testing.T) {
	g, r := fixture(t)
	ctx := context.Background()
	rev, _ := r.Resolve(ctx, "main")

	all, more, err := r.Commits(ctx, rev, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || more {
		t.Fatalf("commits: %d more=%v", len(all), more)
	}
	if all[0].Subject != "Print something" || all[1].Subject != "Initial commit" {
		t.Errorf("order: %q %q", all[0].Subject, all[1].Subject)
	}
	if all[0].Author.Name != "Ada Lovelace" || all[0].Author.Email != "ada@example.com" {
		t.Errorf("author: %+v", all[0].Author)
	}
	if len(all[0].Parents) != 1 || all[0].Parents[0] != all[1].SHA {
		t.Errorf("parents: %v", all[0].Parents)
	}
	if !all[0].Author.Date.After(all[1].Author.Date) {
		t.Error("dates not advancing")
	}

	// Window and more-flag.
	page1, more, err := r.Commits(ctx, rev, "", 1, 0)
	if err != nil || len(page1) != 1 || !more {
		t.Fatalf("page1: %d more=%v err=%v", len(page1), more, err)
	}
	page2, more, err := r.Commits(ctx, rev, "", 1, 1)
	if err != nil || len(page2) != 1 || more {
		t.Fatalf("page2: %d more=%v err=%v", len(page2), more, err)
	}
	if page2[0].SHA != all[1].SHA {
		t.Error("skip window wrong")
	}

	// Path filter.
	docCommits, _, err := r.Commits(ctx, rev, "docs/guide.md", 10, 0)
	if err != nil || len(docCommits) != 1 {
		t.Fatalf("path filter: %d %v", len(docCommits), err)
	}

	last, err := r.LastCommit(ctx, rev, "main.go")
	if err != nil || last.Subject != "Print something" {
		t.Errorf("last commit main.go: %+v %v", last, err)
	}
	last, err = r.LastCommit(ctx, rev, "README.md")
	if err != nil || last.Subject != "Initial commit" {
		t.Errorf("last commit README: %+v %v", last, err)
	}

	g.Write("multi.txt", "x\n")
	g.Git("add", "-A")
	g.Git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "Title line\n\nBody first.\nBody second.")
	rev2, _ := r.Resolve(ctx, "main")
	head, _, err := r.Commits(ctx, rev2, "", 1, 0)
	if err != nil || len(head) != 1 {
		t.Fatal(err)
	}
	if head[0].Subject != "Title line" || head[0].Body != "Body first.\nBody second." {
		t.Errorf("subject/body split: %q / %q", head[0].Subject, head[0].Body)
	}
}

func TestCommitDetail(t *testing.T) {
	g, r := fixture(t)
	ctx := context.Background()

	detail, err := r.Commit(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Subject != "Print something" {
		t.Errorf("subject: %q", detail.Subject)
	}
	if detail.Stats.FilesChanged != 1 || len(detail.Files) != 1 {
		t.Fatalf("files: %+v", detail.Stats)
	}
	f := detail.Files[0]
	if f.NewPath != "main.go" || f.Status != backend.Modified {
		t.Errorf("file: %+v", f)
	}
	if f.Additions == 0 || len(f.Hunks) == 0 {
		t.Errorf("hunks: %+v", f)
	}

	// Root commit diffs against the empty tree.
	first, _, _ := r.Commits(ctx, detail.SHA, "", 100, 0)
	root := first[len(first)-1]
	rootDetail, err := r.Commit(ctx, root.SHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(rootDetail.Files) != 3 {
		t.Errorf("root files: %d", len(rootDetail.Files))
	}
	for _, f := range rootDetail.Files {
		if f.Status != backend.Added {
			t.Errorf("root status: %+v", f)
		}
	}

	// Rename detection.
	g.Rename("docs/guide.md", "docs/manual.md")
	sha := g.Commit("Rename the guide")
	renDetail, err := r.Commit(ctx, sha)
	if err != nil {
		t.Fatal(err)
	}
	if len(renDetail.Files) != 1 || renDetail.Files[0].Status != backend.Renamed {
		t.Fatalf("rename: %+v", renDetail.Files)
	}
	if renDetail.Files[0].OldPath != "docs/guide.md" || renDetail.Files[0].NewPath != "docs/manual.md" {
		t.Errorf("rename paths: %+v", renDetail.Files[0])
	}

	// Merge commit diffs against first parent.
	g.Branch("side")
	g.Write("side.txt", "s\n")
	g.Commit("Side work")
	g.Checkout("main")
	mergeSHA := g.Merge("side")
	mergeDetail, err := r.Commit(ctx, mergeSHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(mergeDetail.Parents) != 2 {
		t.Fatalf("merge parents: %v", mergeDetail.Parents)
	}
	if len(mergeDetail.Files) != 1 || mergeDetail.Files[0].NewPath != "side.txt" {
		t.Errorf("merge diff vs first parent: %+v", mergeDetail.Files)
	}
}

func TestBlame(t *testing.T) {
	g, r := fixture(t)
	g.Write("docs/guide.md", "guide v1\nsecond line\n")
	g.Commit("Extend the guide")
	ctx := context.Background()
	rev, _ := r.Resolve(ctx, "main")

	hunks, err := r.Blame(ctx, rev, "docs/guide.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) != 2 {
		t.Fatalf("hunks: %+v", hunks)
	}
	if hunks[0].StartLine != 1 || hunks[0].Commit.Subject != "Initial commit" {
		t.Errorf("hunk 0: %+v", hunks[0])
	}
	if hunks[1].StartLine != 2 || hunks[1].Commit.Subject != "Extend the guide" {
		t.Errorf("hunk 1: %+v", hunks[1])
	}
	if hunks[0].Commit.Author.Name != "Ada Lovelace" {
		t.Errorf("blame author: %+v", hunks[0].Commit.Author)
	}
	if hunks[0].Commit.Author.Date.IsZero() {
		t.Error("blame date missing")
	}
	// The line modified later carries a reblame pointer.
	if hunks[1].Prev == "" {
		t.Error("expected previous sha for reblame")
	}
}

func TestFilesAndCount(t *testing.T) {
	_, r := fixture(t)
	ctx := context.Background()
	rev, _ := r.Resolve(ctx, "main")

	files, err := r.Files(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"README.md", "docs/guide.md", "main.go"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Errorf("files: %v", files)
	}
	again, _ := r.Files(ctx, rev) // cached path
	if strings.Join(again, ",") != strings.Join(want, ",") {
		t.Errorf("cached files: %v", again)
	}

	n, err := r.CommitCount(ctx, rev)
	if err != nil || n != 2 {
		t.Errorf("count: %d %v", n, err)
	}
}

func TestArchive(t *testing.T) {
	_, r := fixture(t)
	ctx := context.Background()
	rev, _ := r.Resolve(ctx, "main")

	var zipBuf bytes.Buffer
	if err := r.Archive(ctx, rev, "zip", "fixture-main/", &zipBuf); err != nil {
		t.Fatal(err)
	}
	if zipBuf.Len() == 0 || !bytes.HasPrefix(zipBuf.Bytes(), []byte("PK")) {
		t.Error("not a zip")
	}
	var tarBuf bytes.Buffer
	if err := r.Archive(ctx, rev, "tar.gz", "fixture-main/", &tarBuf); err != nil {
		t.Fatal(err)
	}
	if tarBuf.Len() < 2 || tarBuf.Bytes()[0] != 0x1f || tarBuf.Bytes()[1] != 0x8b {
		t.Error("not gzip")
	}
	if err := r.Archive(ctx, rev, "rar", "x/", &bytes.Buffer{}); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("format: %v", err)
	}
}

func TestBareRepo(t *testing.T) {
	g, _ := fixture(t)
	bare := g.Bare()
	r, err := New(context.Background(), bare)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rev, err := r.Resolve(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := r.Tree(ctx, rev, "")
	if err != nil || len(entries) != 3 {
		t.Errorf("bare tree: %d %v", len(entries), err)
	}
}

func TestUnicodeFilename(t *testing.T) {
	g := gittest.New(t)
	g.Write("tài liệu.md", "xin chào\n")
	g.Commit("Add unicode file")
	r, err := New(context.Background(), g.Path())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rev, _ := r.Resolve(ctx, "main")
	entries, err := r.Tree(ctx, rev, "")
	if err != nil || len(entries) != 1 || entries[0].Name != "tài liệu.md" {
		t.Fatalf("unicode entry: %+v %v", entries, err)
	}
	b, err := r.Blob(ctx, rev, "tài liệu.md")
	if err != nil || !bytes.Contains(b.Content, []byte("xin chào")) {
		t.Errorf("unicode blob: %v", err)
	}
}

func TestEmptyRepo(t *testing.T) {
	g := gittest.New(t)
	r, err := New(context.Background(), g.Path())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	refs, err := r.Refs(ctx)
	if err != nil || len(refs.Branches) != 0 {
		t.Errorf("empty refs: %+v %v", refs, err)
	}
	if _, err := r.Resolve(ctx, "main"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("empty resolve: %v", err)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestBlobTooBig(t *testing.T) {
	g := gittest.New(t)
	g.WriteBinary("big.bin", backend.MaxBlobBytes+1)
	g.Commit("Add a file over the display cap")
	r, err := New(context.Background(), g.Path())
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Blob(context.Background(), "main", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !b.TooBig {
		t.Error("TooBig = false for a file over MaxBlobBytes")
	}
	if b.Size != backend.MaxBlobBytes+1 {
		t.Errorf("Size = %d, want %d", b.Size, backend.MaxBlobBytes+1)
	}
	if len(b.Content) != 0 {
		t.Errorf("Content loaded anyway: %d bytes", len(b.Content))
	}
}

func TestResolveBranchTagCollision(t *testing.T) {
	g := gittest.New(t)
	g.Write("a.txt", "one\n")
	tagged := g.Commit("First")
	g.Tag("dual")
	g.Write("a.txt", "two\n")
	branched := g.Commit("Second")
	g.Branch("dual")
	g.Checkout("main")
	r, err := New(context.Background(), g.Path())
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(context.Background(), "dual")
	if err != nil {
		t.Fatal(err)
	}
	if got != branched {
		t.Errorf("Resolve(dual) = %s, want the branch commit %s (tag is at %s)", got, branched, tagged)
	}
}
