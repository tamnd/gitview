package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gitview/backend"
	"github.com/tamnd/gitview/gittest"
	"github.com/tamnd/gitview/localgit"
)

// newTestServer builds a server over one deterministic fixture repository.
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	g := gittest.New(t)
	g.Write("README.md", "# Demo\n\nHello from [docs](docs/guide.md).\n\n> [!NOTE]\n> Heads up.\n")
	g.Write("main.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")
	g.Write("docs/guide.md", "# Guide\n")
	g.Commit("first commit")
	g.Write("main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n")
	g.Commit("use fmt")
	g.Branch("feature/x")
	g.Checkout("feature/x")
	g.Write("extra.txt", "extra\n")
	g.Commit("add extra")
	g.Checkout("main")
	g.Tag("v1.0.0")
	g.SetOrigin("https://github.com/octo/demo.git")

	repo, err := localgit.New(context.Background(), g.Path())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), []backend.Repo{repo}, Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, "/octo/demo"
}

func get(t *testing.T, s *Server, path string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q", w)
		}
	}
}

func TestIndexRedirectsSingleRepo(t *testing.T) {
	s, base := newTestServer(t)
	res, _ := get(t, s, "/")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != base {
		t.Fatalf("location = %q, want %q", loc, base)
	}
}

func TestHome(t *testing.T) {
	s, base := newTestServer(t)
	res, body := get(t, s, base)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	mustContain(t, body,
		"README.md", "main.go", "docs",
		"use fmt",                         // latest commit subject in the bar
		`<article class="markdown-body">`, // readme rendered
		"markdown-alert-note",             // alert transform ran
		"branch-picker",                   // picker present
		"feature/x",                       // slashed branch listed
		"v1.0.0",                          // tag listed
	)
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' 'sha256-") {
		t.Errorf("missing CSP script hash, got %q", csp)
	}
}

func TestSymlinkAndSubmodulePages(t *testing.T) {
	g := gittest.New(t)
	g.Write("README.md", "# Demo\n")
	g.Symlink("README.md", "link.md")
	g.Submodule("deps", strings.Repeat("c", 40))
	g.Commit("first commit")
	g.SetOrigin("https://github.com/octo/sub.git")

	repo, err := localgit.New(context.Background(), g.Path())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), []backend.Repo{repo}, Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}

	// The submodule row is a badge with the pinned short SHA, never a link.
	_, body := get(t, s, "/octo/sub")
	mustContain(t, body, "deps", "@ ccccccc")
	for _, link := range []string{`href="/octo/sub/blob/main/deps"`, `href="/octo/sub/tree/main/deps"`} {
		if strings.Contains(body, link) {
			t.Errorf("submodule row links to %s", link)
		}
	}

	// The symlink blob page shows the target as plain text.
	res, body := get(t, s, "/octo/sub/blob/main/link.md")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("symlink blob status = %d", res.StatusCode)
	}
	mustContain(t, body, "symbolic link to README.md")
	if strings.Contains(body, `class="code-table`) {
		t.Error("symlink blob rendered a code table")
	}
}

func TestTreeSubdirAndSlashedBranch(t *testing.T) {
	s, base := newTestServer(t)
	_, body := get(t, s, base+"/tree/main/docs")
	mustContain(t, body, "guide.md", "breadcrumb")

	res, body := get(t, s, base+"/tree/feature/x")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("slashed branch status = %d", res.StatusCode)
	}
	mustContain(t, body, "extra.txt")
}

func TestTreeBlobRedirects(t *testing.T) {
	s, base := newTestServer(t)
	res, _ := get(t, s, base+"/tree/main/main.go")
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/blob/main/main.go") {
		t.Fatalf("tree-of-file: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = get(t, s, base+"/blob/main/docs")
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/tree/main/docs") {
		t.Fatalf("blob-of-dir: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestBlobCodeAndMarkdown(t *testing.T) {
	s, base := newTestServer(t)
	_, body := get(t, s, base+"/blob/main/main.go")
	mustContain(t, body, `id="L1"`, "code-table", "Raw", "Blame", "History")

	_, body = get(t, s, base+"/blob/main/README.md")
	mustContain(t, body, "markdown-body", "Preview")
	// Relative link rewritten to a blob URL.
	mustContain(t, body, base+"/blob/main/docs/guide.md")

	_, body = get(t, s, base+"/blob/main/README.md?plain=1")
	mustContain(t, body, "code-table")
}

func TestRaw(t *testing.T) {
	s, base := newTestServer(t)
	res, body := get(t, s, base+"/raw/main/main.go")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("raw response not sandboxed")
	}
	if !strings.Contains(body, "fmt.Println") {
		t.Errorf("raw body wrong: %q", body)
	}
}

func TestCommitsAndPagination(t *testing.T) {
	s, base := newTestServer(t)
	res, body := get(t, s, base+"/commits")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	mustContain(t, body, "Commits on", "use fmt", "first commit")

	// Path-scoped history.
	_, body = get(t, s, base+"/commits/main/main.go")
	mustContain(t, body, "use fmt", "first commit")
}

func TestCommitDetail(t *testing.T) {
	s, base := newTestServer(t)
	// Find the head SHA via the commits page link.
	_, body := get(t, s, base+"/commits")
	i := strings.Index(body, base+"/commit/")
	if i < 0 {
		t.Fatal("no commit link on commits page")
	}
	sha := body[i+len(base+"/commit/") : i+len(base+"/commit/")+40]
	res, body := get(t, s, base+"/commit/"+sha)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	mustContain(t, body, "use fmt", "changed file", "diff-table", "diff-addition", "main.go")
}

func TestBranchesAndTags(t *testing.T) {
	s, base := newTestServer(t)
	_, body := get(t, s, base+"/branches")
	mustContain(t, body, "main", "feature/x", "default", "branch-a-b")

	_, body = get(t, s, base+"/tags")
	mustContain(t, body, "v1.0.0", ".zip", ".tar.gz")
}

func TestBlame(t *testing.T) {
	s, base := newTestServer(t)
	res, body := get(t, s, base+"/blame/main/main.go")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	mustContain(t, body, "blame-table", "use fmt", "data-heat=")
}

func TestFindAndTreeList(t *testing.T) {
	s, base := newTestServer(t)
	rev := resolveRev(t, s, "main")
	_, body := get(t, s, base+"/find/main")
	mustContain(t, body, "finder", base+"/tree-list/"+rev)

	// The endpoint takes full SHAs only; ref names 404.
	res, _ := get(t, s, base+"/tree-list/main")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("ref name status = %d, want 404", res.StatusCode)
	}

	res, body = get(t, s, base+"/tree-list/"+rev)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("cache-control = %q, want immutable", cc)
	}
	var got struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"README.md", "docs/guide.md", "main.go"}
	if len(got.Paths) != len(want) {
		t.Fatalf("paths = %v, want %v", got.Paths, want)
	}
	for i := range want {
		if got.Paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", got.Paths, want)
		}
	}
}

func TestArchive(t *testing.T) {
	s, base := newTestServer(t)
	res, body := get(t, s, base+"/archive/refs/heads/main.zip")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if !strings.HasPrefix(body, "PK") {
		t.Errorf("not a zip: %q", body[:4])
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "demo-main.zip") {
		t.Errorf("content-disposition = %q", cd)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("branch archive cache-control = %q, want no-cache", cc)
	}

	res, body = get(t, s, base+"/archive/refs/tags/v1.0.0.tar.gz")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if body[0] != 0x1f || body[1] != 0x8b {
		t.Errorf("not gzip")
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "demo-1.0.0.tar.gz") {
		t.Errorf("content-disposition = %q", cd)
	}

	// Archives addressed by full SHA never change, so they cache forever.
	rev := resolveRev(t, s, "main")
	res, _ = get(t, s, base+"/archive/"+rev+".zip")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sha archive status = %d", res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("sha archive cache-control = %q, want immutable", cc)
	}
}

// resolveRev resolves a ref through the fixture repository's backend.
func resolveRev(t *testing.T, s *Server, ref string) string {
	t.Helper()
	rev, err := s.repos["octo/demo"].repo.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func TestPageTitles(t *testing.T) {
	s, base := newTestServer(t)
	cases := []struct{ url, title string }{
		{base, "<title>octo/demo</title>"},
		{base + "/tree/feature/x", "<title>octo/demo at feature/x</title>"},
		{base + "/tree/main/docs", "<title>demo/docs at main · octo/demo</title>"},
		{base + "/blob/main/main.go", "<title>demo/main.go at main · octo/demo</title>"},
		{base + "/commits", "<title>Commits · octo/demo</title>"},
		{base + "/branches", "<title>Branches · octo/demo</title>"},
		{base + "/tags", "<title>Tags · octo/demo</title>"},
		{base + "/blame/main/main.go", "<title>Blaming demo/main.go at main · octo/demo</title>"},
		{base + "/find/main", "<title>Find file · octo/demo</title>"},
	}
	for _, c := range cases {
		_, body := get(t, s, c.url)
		mustContain(t, body, c.title)
	}
	rev := resolveRev(t, s, "main")
	_, body := get(t, s, base+"/commit/"+rev)
	mustContain(t, body, "<title>use fmt · octo/demo@"+rev[:7]+"</title>")
}

func TestFavicon(t *testing.T) {
	s, base := newTestServer(t)
	res, body := get(t, s, "/favicon.ico")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(body, "<svg") {
		t.Error("favicon is not an svg")
	}
	_, page := get(t, s, base)
	mustContain(t, page, `rel="icon"`, "/static/favicon.svg")
}

func TestHEADRef(t *testing.T) {
	s, base := newTestServer(t)
	res, body := get(t, s, base+"/tree/HEAD")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("tree status = %d", res.StatusCode)
	}
	mustContain(t, body, "README.md", "main.go")

	res, body = get(t, s, base+"/raw/HEAD/main.go")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("raw status = %d", res.StatusCode)
	}
	mustContain(t, body, "package main")
}

func TestCommitPrefixRedirect(t *testing.T) {
	s, base := newTestServer(t)
	rev := resolveRev(t, s, "main")
	res, _ := get(t, s, base+"/commit/"+rev[:8])
	if res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != base+"/commit/"+rev {
		t.Fatalf("location = %q, want %q", loc, base+"/commit/"+rev)
	}
}

func TestDiffAnchors(t *testing.T) {
	s, base := newTestServer(t)
	rev := resolveRev(t, s, "main")
	_, body := get(t, s, base+"/commit/"+rev)
	mustContain(t, body, fmt.Sprintf("diff-%x", sha256.Sum256([]byte("main.go"))))
}

func TestEmptyRepo(t *testing.T) {
	g := gittest.New(t)
	repo, err := localgit.New(context.Background(), g.Path())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), []backend.Repo{repo}, Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	res, body := get(t, s, "/"+s.order[0])
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	mustContain(t, body, "This repository is empty.")
	// The Code button keeps the clone path even with nothing to archive.
	mustContain(t, body, `class="clone-input"`)
	mustContain(t, body, "Local path")
	if strings.Contains(body, "Download ZIP") {
		t.Fatal("empty repo should not offer archive downloads")
	}
}

func TestCloneBox(t *testing.T) {
	s, base := newTestServer(t)
	_, body := get(t, s, base)
	mustContain(t, body, `class="clone-input"`)
	// The fixture has an https origin, so the box is labeled like github.
	mustContain(t, body, ">HTTPS<")
	mustContain(t, body, `value="https://github.com/octo/demo.git"`)
	mustContain(t, body, "data-copy-text=")
	mustContain(t, body, "Download ZIP")
}

func TestCloneLabel(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"https://github.com/octo/demo.git", "HTTPS"},
		{"git@github.com:octo/demo.git", "HTTPS"},
		{"/home/me/src/demo", "Local path"},
	} {
		if got := cloneLabel(tc.url); got != tc.want {
			t.Errorf("cloneLabel(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestNotFound(t *testing.T) {
	s, base := newTestServer(t)
	for _, p := range []string{
		"/nope/nope",
		base + "/blob/main/missing.txt",
		base + "/tree/no-such-branch",
		base + "/pulls",
	} {
		res, body := get(t, s, p)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", p, res.StatusCode)
		}
		if !strings.Contains(body, "404") {
			t.Errorf("%s: not the styled error page", p)
		}
	}
}

func TestRateLimitPage(t *testing.T) {
	s, _ := newTestServer(t)
	reset := time.Date(2026, 6, 10, 12, 30, 0, 0, time.UTC)
	err := fmt.Errorf("github: /repos/octo/demo: %w", &backend.RateLimitError{Reset: reset})

	req := httptest.NewRequest("GET", "/octo/demo", nil)
	rec := httptest.NewRecorder()
	s.fail(rec, req, err)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.StatusCode)
	}
	mustContain(t, string(body), "rate limit", "12:30 UTC")
}

func TestStaticAssets(t *testing.T) {
	s, _ := newTestServer(t)
	for _, p := range []string{"/static/primer.css", "/static/app.js", "/static/chroma.css"} {
		res, body := get(t, s, p)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d", p, res.StatusCode)
		}
		if len(body) == 0 {
			t.Errorf("%s: empty", p)
		}
	}
	// The dark chroma style must be scoped, not global.
	_, body := get(t, s, "/static/chroma.css")
	if !strings.Contains(body, `html[data-color-mode="dark"] .chroma`) {
		t.Error("chroma.css missing scoped dark style")
	}
}

func TestSplitRefPath(t *testing.T) {
	refs := backend.Refs{
		Branches: []backend.Ref{{Name: "main"}, {Name: "feature/x"}, {Name: "feature/x/y"}},
		Tags:     []backend.Ref{{Name: "v1"}, {Name: "feature/x"}},
	}
	cases := []struct {
		rest, ref, path string
	}{
		{"main", "main", ""},
		{"main/docs/guide.md", "main", "docs/guide.md"},
		{"feature/x", "feature/x", ""},
		{"feature/x/y", "feature/x/y", ""}, // longest wins
		{"feature/x/y/z.txt", "feature/x/y", "z.txt"},
		{"v1/file", "v1", "file"},
		{"abc123/file", "abc123", "file"}, // hex fallback
	}
	for _, c := range cases {
		ref, path := splitRefPath(refs, c.rest)
		if ref != c.ref || path != c.path {
			t.Errorf("splitRefPath(%q) = (%q, %q), want (%q, %q)", c.rest, ref, path, c.ref, c.path)
		}
	}
}

func TestMultiRepoIndex(t *testing.T) {
	g1 := gittest.New(t)
	g1.Write("a.txt", "a\n")
	g1.Commit("a")
	g1.SetOrigin("https://github.com/octo/alpha.git")
	g2 := gittest.New(t)
	g2.Write("b.txt", "b\n")
	g2.Commit("b")
	g2.SetOrigin("https://github.com/octo/beta.git")

	r1, err := localgit.New(context.Background(), g1.Path())
	if err != nil {
		t.Fatal(err)
	}
	r2, err := localgit.New(context.Background(), g2.Path())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), []backend.Repo{r1, r2}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	res, body := get(t, s, "/")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	mustContain(t, body, "alpha", "beta", "Repositories")
}

// stubRepo is the one hand-written backend stub doc 14 allows; it exists to
// reach the degradation and status-table rows no real fixture can produce.
// Everything else tests against real localgit repositories.
type stubRepo struct {
	contentErr error // when set, content methods fail with it
}

const stubSHA = "0123456789abcdef0123456789abcdef01234567"

func (s *stubRepo) Info(context.Context) (backend.Info, error) {
	return backend.Info{Owner: "stub", Name: "repo", DefaultBranch: "main",
		CloneURL: "https://example.com/stub.git"}, nil
}

func (s *stubRepo) Refs(context.Context) (backend.Refs, error) {
	return backend.Refs{Branches: []backend.Ref{{Name: "main", SHA: stubSHA}}}, nil
}

func (s *stubRepo) Resolve(_ context.Context, ref string) (string, error) {
	return stubSHA, nil
}

func (s *stubRepo) Tree(_ context.Context, rev, pth string) ([]backend.TreeEntry, error) {
	if s.contentErr != nil {
		return nil, s.contentErr
	}
	if pth != "" {
		return nil, backend.ErrNotFound
	}
	return []backend.TreeEntry{{Name: "file.txt", Path: "file.txt",
		Kind: backend.KindFile, SHA: stubSHA, Size: 5, Mode: "100644"}}, nil
}

func (s *stubRepo) Blob(_ context.Context, rev, pth string) (backend.Blob, error) {
	if s.contentErr != nil {
		return backend.Blob{}, s.contentErr
	}
	if pth != "file.txt" {
		return backend.Blob{}, backend.ErrNotFound
	}
	return backend.Blob{Path: pth, Size: 5, Content: []byte("hello")}, nil
}

func (s *stubRepo) Commits(_ context.Context, rev, pth string, n, skip int) ([]backend.Commit, bool, error) {
	if s.contentErr != nil {
		return nil, false, s.contentErr
	}
	return []backend.Commit{{SHA: stubSHA, Subject: "stub commit"}}, false, nil
}

func (s *stubRepo) Commit(_ context.Context, sha string) (backend.CommitDetail, error) {
	if s.contentErr != nil {
		return backend.CommitDetail{}, s.contentErr
	}
	return backend.CommitDetail{Commit: backend.Commit{SHA: stubSHA, Subject: "stub commit"}}, nil
}

func (s *stubRepo) LastCommit(_ context.Context, rev, pth string) (backend.Commit, error) {
	return backend.Commit{}, fmt.Errorf("stub: %w", backend.ErrUnsupported)
}

func (s *stubRepo) Blame(_ context.Context, rev, pth string) ([]backend.BlameHunk, error) {
	return nil, fmt.Errorf("stub: %w", backend.ErrUnsupported)
}

func (s *stubRepo) Files(_ context.Context, rev string) ([]string, error) {
	return nil, fmt.Errorf("stub: %w", backend.ErrUnsupported)
}

func (s *stubRepo) Archive(_ context.Context, rev, format, prefix string, w io.Writer) error {
	return fmt.Errorf("stub: %w", backend.ErrUnsupported)
}

func newStubServer(t *testing.T, stub *stubRepo, dev bool) *Server {
	t.Helper()
	s, err := New(context.Background(), []backend.Repo{stub}, Options{Dev: dev})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestStatusTable covers the redirect and method rows of the doc 05 §9
// status policy; the error rows live in TestTimeoutAndErrorPages.
func TestStatusTable(t *testing.T) {
	s, base := newTestServer(t)

	redirects := []struct{ from, to string }{
		{base + "/", base},
		{base + "/tree/main/docs/", base + "/tree/main/docs"},
		{base + "//tree//main/docs", base + "/tree/main/docs"},
		{base + "/tree/main/docs/?plain=1", base + "/tree/main/docs?plain=1"},
		{base + "/tree/main", base}, // default branch home alias
	}
	for _, rd := range redirects {
		res, _ := get(t, s, rd.from)
		if res.StatusCode != http.StatusMovedPermanently {
			t.Errorf("GET %s status = %d, want 301", rd.from, res.StatusCode)
		}
		if loc := res.Header.Get("Location"); loc != rd.to {
			t.Errorf("GET %s location = %q, want %q", rd.from, loc, rd.to)
		}
	}

	// A non-default ref at the tree root stays a page of its own.
	res, _ := get(t, s, base+"/tree/feature/x")
	if res.StatusCode != http.StatusOK {
		t.Errorf("non-default tree root status = %d, want 200", res.StatusCode)
	}

	// A decoded dot-dot segment never reaches a handler.
	res, _ = get(t, s, base+"/blob/main/../../etc/passwd")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("dot-dot status = %d, want 404", res.StatusCode)
	}

	// Method other than GET/HEAD: the mux default 405 with Allow.
	req := httptest.NewRequest("POST", base, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q, want GET", allow)
	}

	// HEAD serves like GET; net/http suppresses the body.
	req = httptest.NewRequest("HEAD", base, nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, want 200", rec.Code)
	}
}

func TestTimeoutAndErrorPages(t *testing.T) {
	// DeadlineExceeded maps to the styled 504.
	s := newStubServer(t, &stubRepo{contentErr: fmt.Errorf("tree: %w", context.DeadlineExceeded)}, true)
	res, body := get(t, s, "/stub/repo")
	if res.StatusCode != http.StatusGatewayTimeout {
		t.Errorf("timeout status = %d, want 504", res.StatusCode)
	}
	mustContain(t, body, "took too long")

	// Canceled is logged only: nothing is rendered for a vanished client.
	s = newStubServer(t, &stubRepo{contentErr: fmt.Errorf("tree: %w", context.Canceled)}, true)
	res, body = get(t, s, "/stub/repo")
	if res.StatusCode != http.StatusOK || body != "" {
		t.Errorf("canceled wrote status %d body %q, want nothing", res.StatusCode, body)
	}

	// Other errors: 500, with the text only under -dev.
	s = newStubServer(t, &stubRepo{contentErr: errors.New("kaput-sentinel")}, true)
	res, body = get(t, s, "/stub/repo")
	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("dev 500 status = %d", res.StatusCode)
	}
	mustContain(t, body, "kaput-sentinel")

	s = newStubServer(t, &stubRepo{contentErr: errors.New("kaput-sentinel")}, false)
	res, body = get(t, s, "/stub/repo")
	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("prod 500 status = %d", res.StatusCode)
	}
	mustContain(t, body, "Something went wrong.")
	if strings.Contains(body, "kaput-sentinel") {
		t.Error("prod 500 leaks the error text")
	}
}

// TestDegradation walks the doc 02 §4 table against the stub backend.
func TestDegradation(t *testing.T) {
	s := newStubServer(t, &stubRepo{}, true)

	// LastCommit unsupported: the file table renders with placeholder
	// cells and no commit bar, still a 200.
	res, body := get(t, s, "/stub/repo")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("home status = %d", res.StatusCode)
	}
	mustContain(t, body, "file.txt", "&ndash;")
	if strings.Contains(body, "commit-subject") {
		t.Error("latest-commit bar rendered without LastCommit support")
	}

	// Blame: the page 404s with the explanatory banner...
	res, body = get(t, s, "/stub/repo/blame/main/file.txt")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("blame status = %d, want 404", res.StatusCode)
	}
	mustContain(t, body, "Blame is not available")
	// ...and afterwards the blob page hides the Blame button.
	_, body = get(t, s, "/stub/repo/blob/main/file.txt")
	if strings.Contains(body, ">Blame<") {
		t.Error("Blame button shown after ErrUnsupported")
	}

	// Files: tree-list 404s, then the finder page says so server-side.
	res, _ = get(t, s, "/stub/repo/tree-list/"+stubSHA)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("tree-list status = %d, want 404", res.StatusCode)
	}
	_, body = get(t, s, "/stub/repo/find/main")
	mustContain(t, body, "not available for this backend")
	if strings.Contains(body, "data-tree-list") {
		t.Error("finder still offers the tree-list fetch")
	}

	// Archive: the download 404s, then the menu entries disappear.
	res, _ = get(t, s, "/stub/repo/archive/refs/heads/main.zip")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("archive status = %d, want 404", res.StatusCode)
	}
	_, body = get(t, s, "/stub/repo")
	if strings.Contains(body, "Download ZIP") {
		t.Error("download menu shown after ErrUnsupported")
	}
}
