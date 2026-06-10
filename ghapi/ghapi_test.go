package ghapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamnd/gitview/backend"
)

var _ backend.Repo = (*Repo)(nil)

// newTestRepo wires a Repo at a mock server for owner "o", repo "r".
func newTestRepo(t *testing.T, h http.Handler) (*Repo, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewWithBaseURL("o", "r", "tok", srv.URL, srv.Client()), srv
}

func TestInfo(t *testing.T) {
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/repos/o/r" {
			t.Errorf("path = %q", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		if got := req.Header.Get("User-Agent"); got != "gitview" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := req.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
			t.Errorf("X-GitHub-Api-Version = %q", got)
		}
		_, _ = fmt.Fprint(w, `{"name":"r","description":"a repo","default_branch":"main",
			"clone_url":"https://github.com/o/r.git","owner":{"login":"o"}}`)
	}))
	info, err := r.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := backend.Info{
		Owner: "o", Name: "r", Description: "a repo", DefaultBranch: "main",
		CloneURL: "https://github.com/o/r.git", Mirror: true,
	}
	if info != want {
		t.Errorf("Info = %+v, want %+v", info, want)
	}
}

func TestRefsPagination(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/branches", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("page") == "2" {
			_, _ = fmt.Fprint(w, `[{"name":"b3","commit":{"sha":"333"}}]`)
			return
		}
		w.Header().Set("Link",
			fmt.Sprintf(`<%s/repos/o/r/branches?per_page=100&page=2>; rel="next", <%s/repos/o/r/branches?per_page=100&page=2>; rel="last"`,
				srv.URL, srv.URL))
		_, _ = fmt.Fprint(w, `[{"name":"b1","commit":{"sha":"111"}},{"name":"b2","commit":{"sha":"222"}}]`)
	})
	mux.HandleFunc("/repos/o/r/tags", func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprint(w, `[{"name":"v2.0","commit":{"sha":"aaa"}},{"name":"v1.0","commit":{"sha":"bbb"}}]`)
	})
	r, s := newTestRepo(t, mux)
	srv = s

	refs, err := r.Refs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs.Branches) != 3 {
		t.Fatalf("branches = %d, want 3", len(refs.Branches))
	}
	for i, want := range []backend.Ref{{Name: "b1", SHA: "111"}, {Name: "b2", SHA: "222"}, {Name: "b3", SHA: "333"}} {
		if refs.Branches[i] != want {
			t.Errorf("branch[%d] = %+v, want %+v", i, refs.Branches[i], want)
		}
	}
	// Tags keep API order, newest first, dates zero.
	if len(refs.Tags) != 2 || refs.Tags[0].Name != "v2.0" || refs.Tags[1].Name != "v1.0" {
		t.Errorf("tags = %+v", refs.Tags)
	}
	if !refs.Tags[0].Date.IsZero() {
		t.Errorf("tag date = %v, want zero", refs.Tags[0].Date)
	}
}

func TestResolve(t *testing.T) {
	var calls atomic.Int64
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		switch req.URL.Path {
		case "/repos/o/r/commits/main":
			_, _ = fmt.Fprint(w, `{"sha":"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}`)
		default:
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		}
	}))
	ctx := context.Background()

	// Hostile refs are rejected before any HTTP request happens.
	for _, bad := range []string{"", "-flag", "a~1", "a^", "a:b", "a?b", "a*b", "a[b", `a\b`, "a b", "a\tb", "a\nb", "a@{0}"} {
		if _, err := r.Resolve(ctx, bad); !errors.Is(err, backend.ErrNotFound) {
			t.Errorf("Resolve(%q) err = %v, want ErrNotFound", bad, err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("bad refs made %d HTTP calls, want 0", n)
	}

	sha, err := r.Resolve(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if sha != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("sha = %q", sha)
	}
	if _, err := r.Resolve(ctx, "gone"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("Resolve(gone) err = %v, want ErrNotFound", err)
	}
}

// An empty repository answers 409 on most endpoints; that must read as
// not-found so the server shows the empty-repo page, not a 500.
func TestEmptyRepoConflict(t *testing.T) {
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, `{"message":"Git Repository is empty."}`, http.StatusConflict)
	}))
	if _, err := r.Resolve(context.Background(), "main"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("Resolve on empty repo err = %v, want ErrNotFound", err)
	}
}

func TestTree(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/contents/dir", func(w http.ResponseWriter, req *http.Request) {
		if got := req.URL.Query().Get("ref"); got != "abc123" {
			t.Errorf("ref = %q", got)
		}
		_, _ = fmt.Fprint(w, `[
			{"name":"b.txt","path":"dir/b.txt","size":7,"type":"file"},
			{"name":"Zoo","path":"dir/Zoo","size":0,"type":"dir"},
			{"name":"a.txt","path":"dir/a.txt","size":3,"type":"file"},
			{"name":"link","path":"dir/link","size":6,"type":"symlink"},
			{"name":"sub","path":"dir/sub","size":0,"type":"submodule"}
		]`)
	})
	mux.HandleFunc("/repos/o/r/contents/file.txt", func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprint(w, `{"name":"file.txt","type":"file","size":3}`)
	})
	r, _ := newTestRepo(t, mux)
	ctx := context.Background()

	entries, err := r.Tree(ctx, "abc123", "dir")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	// Dirs and submodules first, case-insensitive within each group.
	want := []string{"sub", "Zoo", "a.txt", "b.txt", "link"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", names, want)
	}
	byName := map[string]backend.TreeEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if e := byName["Zoo"]; e.Kind != backend.KindDir || e.Size != -1 || e.Mode != "040000" {
		t.Errorf("Zoo = %+v", e)
	}
	if e := byName["a.txt"]; e.Kind != backend.KindFile || e.Size != 3 || e.Path != "dir/a.txt" {
		t.Errorf("a.txt = %+v", e)
	}
	if e := byName["link"]; e.Kind != backend.KindSymlink || e.Mode != "120000" {
		t.Errorf("link = %+v", e)
	}
	if e := byName["sub"]; e.Kind != backend.KindSubmodule {
		t.Errorf("sub = %+v", e)
	}

	// A file path answers with an object, not an array.
	if _, err := r.Tree(ctx, "abc123", "file.txt"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("Tree(file) err = %v, want ErrNotFound", err)
	}
}

func TestBlob(t *testing.T) {
	var blobCalls atomic.Int64
	big := bytes.Repeat([]byte("x"), 100)
	big[10] = 0 // NUL inside the first 8000 bytes
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/contents/hello.txt", func(w http.ResponseWriter, req *http.Request) {
		// Embedded newline mid-base64, as the API serves it.
		_, _ = fmt.Fprint(w, `{"type":"file","size":6,"sha":"s1","encoding":"base64","content":"aGVs\nbG8K"}`)
	})
	mux.HandleFunc("/repos/o/r/contents/big.bin", func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprint(w, `{"type":"file","size":100,"sha":"s2","encoding":"none","content":""}`)
	})
	mux.HandleFunc("/repos/o/r/git/blobs/s2", func(w http.ResponseWriter, req *http.Request) {
		blobCalls.Add(1)
		_, _ = fmt.Fprintf(w, `{"encoding":"base64","content":"%s","size":100}`, b64(big))
	})
	mux.HandleFunc("/repos/o/r/contents/huge.bin", func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprintf(w, `{"type":"file","size":%d,"sha":"s3","encoding":"none","content":""}`, backend.MaxBlobBytes+1)
	})
	mux.HandleFunc("/repos/o/r/contents/link", func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprint(w, `{"type":"symlink","size":7,"target":"../real"}`)
	})
	mux.HandleFunc("/repos/o/r/contents/lfs.bin", func(w http.ResponseWriter, req *http.Request) {
		ptr := "version https://git-lfs.github.com/spec/v1\noid sha256:abcd\nsize 12345\n"
		_, _ = fmt.Fprintf(w, `{"type":"file","size":%d,"sha":"s4","encoding":"base64","content":"%s"}`, len(ptr), b64([]byte(ptr)))
	})
	r, _ := newTestRepo(t, mux)
	ctx := context.Background()

	b, err := r.Blob(ctx, "abc123", "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Content) != "hello\n" || b.Size != 6 || b.Binary || b.TooBig {
		t.Errorf("hello.txt = %+v", b)
	}

	// Empty inline content under the cap falls back to git/blobs.
	b, err = r.Blob(ctx, "abc123", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if blobCalls.Load() != 1 {
		t.Errorf("git/blobs calls = %d, want 1", blobCalls.Load())
	}
	if !bytes.Equal(b.Content, big) || !b.Binary {
		t.Errorf("big.bin content len=%d binary=%v", len(b.Content), b.Binary)
	}

	// Over the cap: TooBig, no content, no blob fetch.
	b, err = r.Blob(ctx, "abc123", "huge.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !b.TooBig || b.Content != nil || b.Size != backend.MaxBlobBytes+1 {
		t.Errorf("huge.bin = %+v", b)
	}

	b, err = r.Blob(ctx, "abc123", "link")
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Content) != "../real" {
		t.Errorf("link content = %q", b.Content)
	}

	b, err = r.Blob(ctx, "abc123", "lfs.bin")
	if err != nil {
		t.Fatal(err)
	}
	if b.LFS == nil || b.LFS.OID != "abcd" || b.LFS.Size != 12345 {
		t.Errorf("lfs = %+v", b.LFS)
	}
}

const commitJSON = `{
	"sha":"c1",
	"commit":{
		"message":"Fix the thing\n\nLonger explanation.\nSecond line.\n",
		"author":{"name":"Ann","email":"ann@example.com","date":"2026-01-02T03:04:05Z"},
		"committer":{"name":"Bob","email":"bob@example.com","date":"2026-01-02T04:05:06Z"}
	},
	"author":{"login":"ann","avatar_url":"https://avatars.example/ann"},
	"committer":null,
	"parents":[{"sha":"p1"},{"sha":"p2"}]
}`

func TestCommits(t *testing.T) {
	var gotQuery atomic.Value
	var link atomic.Value
	link.Store("")
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/commits", func(w http.ResponseWriter, req *http.Request) {
		gotQuery.Store(req.URL.RawQuery)
		if l := link.Load().(string); l != "" {
			w.Header().Set("Link", l)
		}
		n := 2
		if req.URL.Query().Get("per_page") == "3" {
			n = 3
		}
		items := make([]string, n)
		for i := range items {
			items[i] = strings.Replace(commitJSON, `"sha":"c1"`, fmt.Sprintf(`"sha":"c%d"`, i+1), 1)
		}
		_, _ = fmt.Fprint(w, "["+strings.Join(items, ",")+"]")
	})
	r, srv := newTestRepo(t, mux)
	ctx := context.Background()

	// Aligned skip: page math, more=true from the Link header.
	link.Store(fmt.Sprintf(`<%s/repos/o/r/commits?sha=main&per_page=2&page=3>; rel="next"`, srv.URL))
	commits, more, err := r.Commits(ctx, "main", "some/path", 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	q := gotQuery.Load().(string)
	for _, want := range []string{"per_page=2", "page=3", "sha=main", "path=some%2Fpath"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q missing %q", q, want)
		}
	}
	if !more {
		t.Error("more = false, want true")
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(commits))
	}
	c := commits[0]
	if c.Subject != "Fix the thing" || c.Body != "Longer explanation.\nSecond line." {
		t.Errorf("subject=%q body=%q", c.Subject, c.Body)
	}
	if c.Author.Name != "Ann" || c.Author.Email != "ann@example.com" ||
		c.Author.Login != "ann" || c.Author.AvatarURL != "https://avatars.example/ann" {
		t.Errorf("author = %+v", c.Author)
	}
	if !c.Author.Date.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("author date = %v", c.Author.Date)
	}
	// Top-level committer is null: no login, git stamp still mapped.
	if c.Committer.Login != "" || c.Committer.Name != "Bob" {
		t.Errorf("committer = %+v", c.Committer)
	}
	if len(c.Parents) != 2 || c.Parents[0] != "p1" || c.Parents[1] != "p2" {
		t.Errorf("parents = %v", c.Parents)
	}

	// Misaligned skip: one larger page, sliced; more=false without Link.
	link.Store("")
	commits, more, err = r.Commits(ctx, "main", "", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	q = gotQuery.Load().(string)
	for _, want := range []string{"per_page=3", "page=1"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q missing %q", q, want)
		}
	}
	if more {
		t.Error("more = true, want false")
	}
	if len(commits) != 2 || commits[0].SHA != "c2" || commits[1].SHA != "c3" {
		t.Errorf("sliced commits = %+v", commits)
	}
}

func TestCommit(t *testing.T) {
	patch := "@@ -1,3 +1,3 @@\n context\n-old line\n+new line\n unchanged"
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/commits/c1", func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprintf(w, `{
			%s,
			"stats":{"additions":10,"deletions":4,"total":14},
			"files":[
				{"filename":"new/name.go","previous_filename":"old/name.go","status":"renamed",
				 "additions":1,"deletions":1,"changes":2,"patch":%q},
				{"filename":"gone.txt","status":"removed","additions":0,"deletions":3,"changes":3,
				 "patch":"@@ -1,3 +0,0 @@\n-a\n-b\n-c"},
				{"filename":"image.png","status":"modified","additions":0,"deletions":0,"changes":0}
			]
		}`, strings.TrimPrefix(strings.TrimSuffix(commitJSON, "}"), "{"), patch)
	})
	r, _ := newTestRepo(t, mux)

	d, err := r.Commit(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Subject != "Fix the thing" || d.SHA != "c1" {
		t.Errorf("commit = %+v", d.Commit)
	}
	if d.Stats != (backend.StatTotal{FilesChanged: 3, Additions: 10, Deletions: 4}) {
		t.Errorf("stats = %+v", d.Stats)
	}
	if len(d.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(d.Files))
	}

	ren := d.Files[0]
	if ren.Status != backend.Renamed || ren.OldPath != "old/name.go" || ren.NewPath != "new/name.go" {
		t.Errorf("renamed = %+v", ren)
	}
	if len(ren.Hunks) != 1 {
		t.Fatalf("renamed hunks = %d, want 1", len(ren.Hunks))
	}
	h := ren.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 3 || h.NewStart != 1 || h.NewLines != 3 {
		t.Errorf("hunk header = %+v", h)
	}
	if len(h.Lines) != 4 {
		t.Fatalf("hunk lines = %d, want 4", len(h.Lines))
	}
	if h.Lines[1].Kind != backend.Deletion || h.Lines[1].Text != "old line" || h.Lines[1].OldNum != 2 {
		t.Errorf("deletion line = %+v", h.Lines[1])
	}
	if h.Lines[2].Kind != backend.Addition || h.Lines[2].Text != "new line" || h.Lines[2].NewNum != 2 {
		t.Errorf("addition line = %+v", h.Lines[2])
	}

	if d.Files[1].Status != backend.Deleted || len(d.Files[1].Hunks) != 1 {
		t.Errorf("removed = %+v", d.Files[1])
	}
	if !d.Files[2].Binary || len(d.Files[2].Hunks) != 0 {
		t.Errorf("missing patch should mean binary: %+v", d.Files[2])
	}
}

func TestUnsupported(t *testing.T) {
	var calls atomic.Int64
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
	}))
	ctx := context.Background()
	if _, err := r.LastCommit(ctx, "main", "x"); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("LastCommit err = %v", err)
	}
	if _, err := r.Blame(ctx, "main", "x"); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("Blame err = %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("unsupported methods made %d HTTP calls", calls.Load())
	}
}

func TestFiles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/git/trees/abc123", func(w http.ResponseWriter, req *http.Request) {
		if got := req.URL.Query().Get("recursive"); got != "1" {
			t.Errorf("recursive = %q", got)
		}
		_, _ = fmt.Fprint(w, `{"tree":[
			{"path":"z.txt","type":"blob"},
			{"path":"dir","type":"tree"},
			{"path":"a/b.txt","type":"blob"}
		],"truncated":false}`)
	})
	r, _ := newTestRepo(t, mux)
	files, err := r.Files(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != "a/b.txt,z.txt" {
		t.Errorf("files = %v", files)
	}
}

func TestArchive(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/zipball/abc123", func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprint(w, "ZIPDATA")
	})
	mux.HandleFunc("/repos/o/r/tarball/abc123", func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprint(w, "TARDATA")
	})
	r, _ := newTestRepo(t, mux)
	ctx := context.Background()

	var buf bytes.Buffer
	if err := r.Archive(ctx, "abc123", "zip", "ignored/", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "ZIPDATA" {
		t.Errorf("zip body = %q", buf.String())
	}
	buf.Reset()
	if err := r.Archive(ctx, "abc123", "tar.gz", "ignored/", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "TARDATA" {
		t.Errorf("tar body = %q", buf.String())
	}
	if err := r.Archive(ctx, "abc123", "rar", "x/", &buf); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("rar err = %v, want ErrUnsupported", err)
	}
}

func TestETagCache(t *testing.T) {
	var calls atomic.Int64
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			if inm := req.Header.Get("If-None-Match"); inm != "" {
				t.Errorf("first request carries If-None-Match %q", inm)
			}
			w.Header().Set("ETag", `"v1"`)
			_, _ = fmt.Fprint(w, `{"name":"r","owner":{"login":"o"},"default_branch":"main","clone_url":"u"}`)
			return
		}
		if inm := req.Header.Get("If-None-Match"); inm != `"v1"` {
			t.Errorf("second request If-None-Match = %q, want %q", inm, `"v1"`)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	ctx := context.Background()

	first, err := r.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
	if first != second {
		t.Errorf("304 body mismatch: %+v vs %+v", first, second)
	}
}

func TestRateLimitError(t *testing.T) {
	reset := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.Header().Set("X-Ratelimit-Reset", fmt.Sprintf("%d", reset.Unix()))
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	}))
	_, err := r.Info(context.Background())
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want RateLimitError", err)
	}
	if !rl.Reset.Equal(reset) {
		t.Errorf("Reset = %v, want %v", rl.Reset, reset)
	}
	if errors.Is(err, backend.ErrNotFound) {
		t.Error("rate limit error must not be ErrNotFound")
	}
}

// b64 renders bytes the way fixtures need them inline.
func b64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
