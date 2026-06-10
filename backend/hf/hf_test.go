package hf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamnd/gitview/backend"
)

var _ backend.Repo = (*Repo)(nil)

// newTestRepo wires a model Repo at a mock server for owner "o", name "n".
func newTestRepo(t *testing.T, h http.Handler) (*Repo, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewWithBaseURL("model", "o", "n", "tok", srv.URL, srv.Client()), srv
}

func TestNewPanicsOnUnknownKind(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New with a bad kind did not panic")
		}
	}()
	New("gist", "o", "n", "")
}

func TestInfo(t *testing.T) {
	r, srv := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/models/o/n" {
			t.Errorf("path = %q", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		if got := req.Header.Get("User-Agent"); got != "gitview" {
			t.Errorf("User-Agent = %q", got)
		}
		_, _ = fmt.Fprint(w, `{"id":"o/n","sha":"abc","cardData":{"short_description":"a model"}}`)
	}))
	info, err := r.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := backend.Info{
		Owner: "o", Name: "n", Description: "a model", DefaultBranch: "main",
		CloneURL: srv.URL + "/o/n", Mirror: true,
	}
	if info != want {
		t.Errorf("Info = %+v, want %+v", info, want)
	}
}

func TestKindPrefixes(t *testing.T) {
	for _, tc := range []struct {
		kind, apiPath, clonePath string
	}{
		{"model", "/api/models/o/n", "/o/n"},
		{"dataset", "/api/datasets/o/n", "/datasets/o/n"},
		{"space", "/api/spaces/o/n", "/spaces/o/n"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				gotPath = req.URL.Path
				_, _ = fmt.Fprint(w, `{}`)
			}))
			t.Cleanup(srv.Close)
			r := NewWithBaseURL(tc.kind, "o", "n", "", srv.URL, srv.Client())
			info, err := r.Info(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if gotPath != tc.apiPath {
				t.Errorf("api path = %q, want %q", gotPath, tc.apiPath)
			}
			if want := srv.URL + tc.clonePath; info.CloneURL != want {
				t.Errorf("CloneURL = %q, want %q", info.CloneURL, want)
			}
		})
	}
}

func TestRefs(t *testing.T) {
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/models/o/n/refs" {
			t.Errorf("path = %q", req.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{
			"branches":[{"name":"main","ref":"refs/heads/main","targetCommit":"111"},
			            {"name":"dev","ref":"refs/heads/dev","targetCommit":"222"}],
			"tags":[{"name":"v1.0","ref":"refs/tags/v1.0","targetCommit":"333"}],
			"converts":[{"name":"parquet","ref":"refs/convert/parquet","targetCommit":"999"}]}`)
	}))
	refs, err := r.Refs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantBranches := []backend.Ref{{Name: "main", SHA: "111"}, {Name: "dev", SHA: "222"}}
	if len(refs.Branches) != 2 || refs.Branches[0] != wantBranches[0] || refs.Branches[1] != wantBranches[1] {
		t.Errorf("branches = %+v", refs.Branches)
	}
	// converts never leak into branches or tags.
	if len(refs.Tags) != 1 || refs.Tags[0] != (backend.Ref{Name: "v1.0", SHA: "333"}) {
		t.Errorf("tags = %+v", refs.Tags)
	}
}

func TestResolve(t *testing.T) {
	var calls atomic.Int64
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		switch req.URL.Path {
		case "/api/models/o/n/revision/main":
			_, _ = fmt.Fprint(w, `{"sha":"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}`)
		default:
			http.NotFound(w, req)
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

func TestTree(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/o/n/tree/main", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("cursor") == "page2" {
			_, _ = fmt.Fprint(w, `[{"type":"file","oid":"f2","size":134,"path":"weights.bin",
				"lfs":{"oid":"sha256oid","size":548105171,"pointerSize":134}}]`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/api/models/o/n/tree/main?cursor=page2>; rel="next"`, srv.URL))
		_, _ = fmt.Fprint(w, `[{"type":"file","oid":"f1","size":42,"path":"README.md"},
			{"type":"directory","oid":"d1","size":0,"path":"configs"}]`)
	})
	r, s := newTestRepo(t, mux)
	srv = s

	entries, err := r.Tree(context.Background(), "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	// Dirs sort first; pagination joined both pages.
	if entries[0].Name != "configs" || entries[0].Kind != backend.KindDir || entries[0].Size != -1 {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[1].Name != "README.md" || entries[1].Kind != backend.KindFile || entries[1].Size != 42 || entries[1].SHA != "f1" {
		t.Errorf("entries[1] = %+v", entries[1])
	}
	// LFS entries report the real content size, not the pointer size.
	if entries[2].Name != "weights.bin" || entries[2].Size != 548105171 {
		t.Errorf("entries[2] = %+v", entries[2])
	}
}

func TestTreeOnFilePath(t *testing.T) {
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprint(w, `[{"type":"file","oid":"f1","size":42,"path":"README.md"}]`)
	}))
	if _, err := r.Tree(context.Background(), "main", "README.md"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("Tree on file err = %v, want ErrNotFound", err)
	}
}

// blobMux wires paths-info and raw endpoints for one file.
func blobMux(t *testing.T, pathsInfoJSON, rawBody string, rawCalls *atomic.Int64) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/o/n/paths-info/main", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Errorf("paths-info method = %q", req.Method)
		}
		if err := req.ParseForm(); err != nil {
			t.Error(err)
		}
		if got := req.PostForm.Get("paths"); got == "" {
			t.Error("paths-info without a paths value")
		}
		_, _ = fmt.Fprint(w, pathsInfoJSON)
	})
	mux.HandleFunc("/o/n/raw/main/", func(w http.ResponseWriter, req *http.Request) {
		if rawCalls != nil {
			rawCalls.Add(1)
		}
		_, _ = fmt.Fprint(w, rawBody)
	})
	return mux
}

func TestBlobSmallFile(t *testing.T) {
	r, _ := newTestRepo(t, blobMux(t,
		`[{"type":"file","oid":"f1","size":13,"path":"README.md"}]`, "hello gitview", nil))
	b, err := r.Blob(context.Background(), "main", "README.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Content) != "hello gitview" || b.Binary || b.LFS != nil || b.TooBig {
		t.Errorf("Blob = %+v", b)
	}
	if b.Size != 13 {
		t.Errorf("Size = %d", b.Size)
	}
}

func TestBlobBinarySniff(t *testing.T) {
	r, _ := newTestRepo(t, blobMux(t,
		`[{"type":"file","oid":"f1","size":3,"path":"a.bin"}]`, "a\x00b", nil))
	b, err := r.Blob(context.Background(), "main", "a.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Binary {
		t.Error("NUL content not sniffed as binary")
	}
}

func TestBlobLFS(t *testing.T) {
	var rawCalls atomic.Int64
	r, _ := newTestRepo(t, blobMux(t,
		`[{"type":"file","oid":"f1","size":134,"path":"model.safetensors",
		   "lfs":{"oid":"sha256:abcdef","size":548105171,"pointerSize":134}}]`, "", &rawCalls))
	b, err := r.Blob(context.Background(), "main", "model.safetensors")
	if err != nil {
		t.Fatal(err)
	}
	if b.LFS == nil || b.LFS.OID != "abcdef" || b.LFS.Size != 548105171 {
		t.Errorf("LFS = %+v", b.LFS)
	}
	if b.Size != 548105171 {
		t.Errorf("Size = %d, want the LFS size", b.Size)
	}
	if n := rawCalls.Load(); n != 0 {
		t.Errorf("LFS blob fetched content %d times, want 0", n)
	}
}

func TestBlobTooBig(t *testing.T) {
	var rawCalls atomic.Int64
	size := backend.MaxBlobBytes + 1
	r, _ := newTestRepo(t, blobMux(t,
		fmt.Sprintf(`[{"type":"file","oid":"f1","size":%d,"path":"big.txt"}]`, size), "", &rawCalls))
	b, err := r.Blob(context.Background(), "main", "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !b.TooBig || b.Content != nil {
		t.Errorf("Blob = %+v, want TooBig with no content", b)
	}
	if n := rawCalls.Load(); n != 0 {
		t.Errorf("too-big blob fetched content %d times, want 0", n)
	}
}

func TestBlobOnDirectory(t *testing.T) {
	r, _ := newTestRepo(t, blobMux(t,
		`[{"type":"directory","oid":"d1","size":0,"path":"configs"}]`, "", nil))
	if _, err := r.Blob(context.Background(), "main", "configs"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("Blob on dir err = %v, want ErrNotFound", err)
	}
}

// commitsJSON builds a commit-list page with sequential ids from start.
func commitsJSON(start, count int) string {
	var b strings.Builder
	b.WriteString("[")
	for i := range count {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":"sha%03d","title":"commit %d","message":"","authors":[{"user":"alice"}],"date":"2026-06-01T00:00:00.000Z"}`,
			start+i, start+i)
	}
	b.WriteString("]")
	return b.String()
}

func TestCommitsWindow(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/o/n/commits/main", func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Query().Get("cursor") {
		case "":
			w.Header().Set("Link", fmt.Sprintf(`<%s/api/models/o/n/commits/main?cursor=p2&limit=100>; rel="next"`, srv.URL))
			_, _ = fmt.Fprint(w, commitsJSON(0, 100))
		case "p2":
			_, _ = fmt.Fprint(w, commitsJSON(100, 20))
		}
	})
	r, s := newTestRepo(t, mux)
	srv = s
	ctx := context.Background()

	// Window inside the first page.
	commits, more, err := r.Commits(ctx, "main", "", 35, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 35 || commits[0].SHA != "sha000" || commits[34].SHA != "sha034" {
		t.Fatalf("commits = %d, first %q, last %q", len(commits), commits[0].SHA, commits[len(commits)-1].SHA)
	}
	if !more {
		t.Error("more = false with 120 commits upstream")
	}
	if commits[0].Subject != "commit 0" || commits[0].Author.Login != "alice" || commits[0].Author.Date.IsZero() {
		t.Errorf("commits[0] = %+v", commits[0])
	}

	// Window straddling the page boundary walks the Link chain.
	commits, more, err = r.Commits(ctx, "main", "", 35, 70)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 35 || commits[0].SHA != "sha070" || commits[34].SHA != "sha104" {
		t.Fatalf("straddle: %d commits, first %q, last %q", len(commits), commits[0].SHA, commits[len(commits)-1].SHA)
	}
	if !more {
		t.Error("more = false with 15 commits left")
	}

	// Window over the tail reports no more.
	commits, more, err = r.Commits(ctx, "main", "", 35, 105)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 15 || more {
		t.Fatalf("tail: %d commits, more = %v", len(commits), more)
	}
}

func TestCommitsPathFilterUnsupported(t *testing.T) {
	var calls atomic.Int64
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
	}))
	_, _, err := r.Commits(context.Background(), "main", "README.md", 35, 0)
	if !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("path-filtered commits made %d HTTP calls, want 0", n)
	}
}

func TestLastCommit(t *testing.T) {
	var treeCalls atomic.Int64
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/models/o/n/tree/main/configs" {
			t.Errorf("path = %q", req.URL.Path)
		}
		if req.URL.Query().Get("expand") != "true" {
			t.Error("tree fetch without expand=true")
		}
		treeCalls.Add(1)
		_, _ = fmt.Fprint(w, `[
			{"type":"file","oid":"f1","size":1,"path":"configs/a.json",
			 "lastCommit":{"id":"c1","title":"add a","date":"2026-05-01T00:00:00.000Z"}},
			{"type":"file","oid":"f2","size":1,"path":"configs/b.json",
			 "lastCommit":{"id":"c2","title":"add b","date":"2026-05-02T00:00:00.000Z"}}]`)
	}))
	ctx := context.Background()

	c, err := r.LastCommit(ctx, "main", "configs/a.json")
	if err != nil {
		t.Fatal(err)
	}
	if c.SHA != "c1" || c.Subject != "add a" || c.Author.Date.IsZero() {
		t.Errorf("LastCommit = %+v", c)
	}

	// Same directory again: served from the cache, no new fetch.
	c, err = r.LastCommit(ctx, "main", "configs/b.json")
	if err != nil {
		t.Fatal(err)
	}
	if c.SHA != "c2" {
		t.Errorf("LastCommit = %+v", c)
	}
	if n := treeCalls.Load(); n != 1 {
		t.Errorf("two lookups in one dir made %d fetches, want 1", n)
	}

	// A path missing from its directory listing is not found.
	if _, err := r.LastCommit(ctx, "main", "configs/gone.json"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("missing path err = %v, want ErrNotFound", err)
	}
}

func TestLastCommitConcurrentSingleFetch(t *testing.T) {
	var treeCalls atomic.Int64
	release := make(chan struct{})
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		treeCalls.Add(1)
		<-release
		_, _ = fmt.Fprint(w, `[
			{"type":"file","oid":"f1","size":1,"path":"a.json",
			 "lastCommit":{"id":"c1","title":"add a","date":"2026-05-01T00:00:00.000Z"}}]`)
	}))

	// Eight concurrent lookups in one directory, the server's fill-pool
	// shape, must collapse to one upstream fetch.
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = r.LastCommit(context.Background(), "main", "a.json")
		})
	}
	// Let the goroutines pile up on the in-flight entry, then release.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	if n := treeCalls.Load(); n != 1 {
		t.Errorf("8 concurrent lookups made %d fetches, want 1", n)
	}
}

func TestFiles(t *testing.T) {
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/models/o/n/tree/main" {
			t.Errorf("path = %q", req.URL.Path)
		}
		if req.URL.Query().Get("recursive") != "true" {
			t.Error("files fetch without recursive=true")
		}
		_, _ = fmt.Fprint(w, `[
			{"type":"directory","oid":"d1","size":0,"path":"configs"},
			{"type":"file","oid":"f2","size":1,"path":"configs/a.json"},
			{"type":"file","oid":"f1","size":1,"path":"README.md"}]`)
	}))
	files, err := r.Files(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "README.md" || files[1] != "configs/a.json" {
		t.Errorf("files = %v", files)
	}
}

func TestUnsupported(t *testing.T) {
	var calls atomic.Int64
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
	}))
	ctx := context.Background()
	if _, err := r.Commit(ctx, "deadbeef"); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("Commit err = %v, want ErrUnsupported", err)
	}
	if _, err := r.Blame(ctx, "main", "README.md"); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("Blame err = %v, want ErrUnsupported", err)
	}
	if err := r.Archive(ctx, "main", "zip", "n-main/", io.Discard); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("Archive err = %v, want ErrUnsupported", err)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("unsupported methods made %d HTTP calls, want 0", n)
	}
}

func TestGatedRepoIsNotFound(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.Error(w, `{"error":"gated"}`, status)
		}))
		if _, err := r.Info(context.Background()); !errors.Is(err, backend.ErrNotFound) {
			t.Errorf("status %d: err = %v, want ErrNotFound", status, err)
		}
	}
}

func TestRateLimit(t *testing.T) {
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Retry-After", "120")
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}))
	_, err := r.Info(context.Background())
	var rle *backend.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want RateLimitError", err)
	}
	if until := time.Until(rle.Reset); until < 100*time.Second || until > 140*time.Second {
		t.Errorf("Reset = %v, want about 120s out", rle.Reset)
	}
}

func TestErrorsHideTokenAndHost(t *testing.T) {
	r, srv := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	_, err := r.Info(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "tok") && strings.Contains(err.Error(), "Bearer") {
		t.Errorf("error echoes the token: %v", err)
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error echoes the host: %v", err)
	}
}

func TestETagCache(t *testing.T) {
	var bodies atomic.Int64
	r, _ := newTestRepo(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		bodies.Add(1)
		w.Header().Set("ETag", `"v1"`)
		_, _ = fmt.Fprint(w, `{"cardData":{"short_description":"cached"}}`)
	}))
	ctx := context.Background()
	for i := range 3 {
		info, err := r.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.Description != "cached" {
			t.Errorf("call %d: Description = %q", i, info.Description)
		}
	}
	if n := bodies.Load(); n != 1 {
		t.Errorf("3 Info calls fetched %d bodies, want 1 plus 304s", n)
	}
}
