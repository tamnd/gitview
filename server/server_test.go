package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	_, body := get(t, s, base+"/find/main")
	mustContain(t, body, "finder", base+"/tree-list/main")

	res, body := get(t, s, base+"/tree-list/main")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
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
