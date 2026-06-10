// Package hf implements backend.Repo over the Hugging Face Hub HTTP API
// with a hand-rolled net/http client, the same shape as the ghapi backend:
// bearer auth, conditional requests (ETag), Link-header pagination, and
// rate-limit errors. Commit detail, Blame, and Archive return
// backend.ErrUnsupported because the Hub API has no endpoint for them; the
// server degrades those views per the capability model.
package hf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamnd/gitview/backend"
)

const apiBase = "https://huggingface.co"

// maxCacheEntries bounds the ETag cache; eviction dumps everything, which
// is fine because a browsing session re-warms the few URLs it actually hits.
const maxCacheEntries = 512

// treePageCap bounds tree pagination at 10 pages; directories beyond that
// render truncated, which the tree page tolerates.
const treePageCap = 10

// commitPageCap bounds the commit-list walk; the Hub has no skip parameter,
// so deep pages mean walking, and 20 pages of 100 is as deep as the UI
// plausibly clicks.
const commitPageCap = 20

// maxLastCommitDirs bounds the per-(rev,dir) last-commit cache, same dumb
// full eviction as the ETag cache.
const maxLastCommitDirs = 64

// Repo is a backend.Repo bound to one Hub repository on one API host.
type Repo struct {
	kind  string // "model", "dataset", or "space"
	owner string
	name  string
	token string
	base  string

	client *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry // request URL -> last 200 response

	lcMu    sync.Mutex
	lcCache map[string]*lastCommitDir // rev "\x00" dir -> expanded listing
}

type cacheEntry struct {
	etag string
	body []byte
	next string // rel="next" Link of the cached response
}

// lastCommitDir is one directory's expanded tree listing, fetched once and
// shared: ready closes when index and err are set, so the server's parallel
// fill pool collapses to a single upstream fetch per directory.
type lastCommitDir struct {
	ready chan struct{}
	index map[string]backend.Commit
	err   error
}

// New returns a Repo talking to huggingface.co. kind is "model", "dataset",
// or "space"; anything else panics, because the CLI validates kinds before
// construction. An empty token means anonymous access.
func New(kind, owner, name, token string) *Repo {
	return NewWithBaseURL(kind, owner, name, token, apiBase, nil)
}

// NewWithBaseURL is New with the API host and HTTP client injectable, which
// tests use to point at a mock server. A nil client means http.DefaultClient.
func NewWithBaseURL(kind, owner, name, token, baseURL string, client *http.Client) *Repo {
	switch kind {
	case "model", "dataset", "space":
	default:
		panic("hf: unknown repo kind " + kind)
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Repo{
		kind:    kind,
		owner:   owner,
		name:    name,
		token:   token,
		base:    strings.TrimSuffix(baseURL, "/"),
		client:  client,
		cache:   make(map[string]cacheEntry),
		lcCache: make(map[string]*lastCommitDir),
	}
}

// apiPrefix is the path under /api/ for this repo kind.
func (r *Repo) apiPrefix() string {
	switch r.kind {
	case "dataset":
		return "/api/datasets"
	case "space":
		return "/api/spaces"
	default:
		return "/api/models"
	}
}

// contentPrefix is the path prefix of the repo's web and raw-content URLs:
// empty for models, /datasets or /spaces otherwise.
func (r *Repo) contentPrefix() string {
	switch r.kind {
	case "dataset":
		return "/datasets"
	case "space":
		return "/spaces"
	default:
		return ""
	}
}

func (r *Repo) apiURL(suffix string) string {
	return r.base + r.apiPrefix() + "/" + url.PathEscape(r.owner) + "/" + url.PathEscape(r.name) + suffix
}

// rawURL addresses git blob content. The raw/ endpoint serves the blob
// bytes exactly (an LFS pointer file stays pointer text); resolve/ would
// redirect to the CDN object and is deliberately not used.
func (r *Repo) rawURL(rev, pth string) string {
	return r.base + r.contentPrefix() + "/" + url.PathEscape(r.owner) + "/" + url.PathEscape(r.name) +
		"/raw/" + url.PathEscape(rev) + "/" + escapePath(pth)
}

func (r *Repo) Info(ctx context.Context) (backend.Info, error) {
	body, _, err := r.get(ctx, r.apiURL(""), true)
	if err != nil {
		return backend.Info{}, fmt.Errorf("info: %w", err)
	}
	var v struct {
		CardData struct {
			ShortDescription string `json:"short_description"`
		} `json:"cardData"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return backend.Info{}, fmt.Errorf("info: %w", err)
	}
	return backend.Info{
		// Identity comes from the constructor, not the response, so a moved
		// repo keeps the name the user asked for.
		Owner:       r.owner,
		Name:        r.name,
		Description: v.CardData.ShortDescription,
		// The Hub does not expose a default-branch field; every Hub repo
		// defaults to main.
		DefaultBranch: "main",
		CloneURL:      r.base + r.contentPrefix() + "/" + r.owner + "/" + r.name,
		Mirror:        true,
	}, nil
}

func (r *Repo) Refs(ctx context.Context) (backend.Refs, error) {
	body, _, err := r.get(ctx, r.apiURL("/refs"), true)
	if err != nil {
		return backend.Refs{}, fmt.Errorf("refs: %w", err)
	}
	var v struct {
		Branches []apiRef `json:"branches"`
		Tags     []apiRef `json:"tags"`
		// converts and pullRequests are deliberately ignored.
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return backend.Refs{}, fmt.Errorf("refs: %w", err)
	}
	toRefs := func(in []apiRef) []backend.Ref {
		out := make([]backend.Ref, 0, len(in))
		for _, it := range in {
			out = append(out, backend.Ref{Name: it.Name, SHA: it.TargetCommit})
		}
		return out
	}
	return backend.Refs{Branches: toRefs(v.Branches), Tags: toRefs(v.Tags)}, nil
}

type apiRef struct {
	Name         string `json:"name"`
	TargetCommit string `json:"targetCommit"`
}

func (r *Repo) Resolve(ctx context.Context, ref string) (string, error) {
	if err := checkRefName(ref); err != nil {
		return "", err
	}
	body, _, err := r.get(ctx, r.apiURL("/revision/"+url.PathEscape(ref)), true)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", ref, err)
	}
	var v struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(body, &v); err != nil || v.SHA == "" {
		return "", fmt.Errorf("resolve %q: no sha in response: %w", ref, backend.ErrNotFound)
	}
	return v.SHA, nil
}

// apiTreeEntry is the element shape of /tree and /paths-info responses.
type apiTreeEntry struct {
	Type string  `json:"type"` // "file" or "directory"
	OID  string  `json:"oid"`
	Size int64   `json:"size"`
	Path string  `json:"path"`
	LFS  *apiLFS `json:"lfs"`
	Last *struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Date  string `json:"date"`
	} `json:"lastCommit"`
}

type apiLFS struct {
	OID  string `json:"oid"`
	Size int64  `json:"size"`
}

func (e *apiTreeEntry) toTreeEntry() backend.TreeEntry {
	out := backend.TreeEntry{Name: path.Base(e.Path), Path: e.Path, SHA: e.OID, Size: -1}
	// The Hub stores symlinks and gitlinks as plain files, and exposes no
	// modes; Mode stays empty and the UI shows kinds only.
	if e.Type == "directory" {
		out.Kind = backend.KindDir
		return out
	}
	out.Kind = backend.KindFile
	out.Size = e.Size
	if e.LFS != nil {
		// The real content size, which is what the file table wants.
		out.Size = e.LFS.Size
	}
	return out
}

// listTree pages through a /tree listing following Link rel="next", up to
// treePageCap pages.
func (r *Repo) listTree(ctx context.Context, u string) ([]apiTreeEntry, error) {
	var entries []apiTreeEntry
	for page := 0; page < treePageCap && u != ""; page++ {
		body, next, err := r.get(ctx, u, true)
		if err != nil {
			return nil, err
		}
		var items []apiTreeEntry
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, err
		}
		entries = append(entries, items...)
		u = next
	}
	return entries, nil
}

func (r *Repo) Tree(ctx context.Context, rev, pth string) ([]backend.TreeEntry, error) {
	if err := checkRefName(rev); err != nil {
		return nil, err
	}
	u := r.apiURL("/tree/" + url.PathEscape(rev))
	if pth != "" {
		u += "/" + escapePath(pth)
	}
	items, err := r.listTree(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("tree %s:%s: %w", rev, pth, err)
	}
	// A file path answers with a single-entry listing of itself; the route
	// logic retries as Blob on ErrNotFound, mirroring the other backends.
	if len(items) == 1 && items[0].Path == pth && items[0].Type == "file" {
		return nil, fmt.Errorf("tree %s:%s: not a directory: %w", rev, pth, backend.ErrNotFound)
	}
	entries := make([]backend.TreeEntry, 0, len(items))
	for i := range items {
		entries = append(entries, items[i].toTreeEntry())
	}
	sortEntries(entries)
	return entries, nil
}

func (r *Repo) Blob(ctx context.Context, rev, pth string) (backend.Blob, error) {
	if err := checkRefName(rev); err != nil {
		return backend.Blob{}, err
	}
	entry, err := r.pathInfo(ctx, rev, pth)
	if err != nil {
		return backend.Blob{}, fmt.Errorf("blob %s:%s: %w", rev, pth, err)
	}
	b := backend.Blob{Path: pth, Size: entry.Size}
	if entry.LFS != nil {
		// The LFS banner needs only the pointer; never touch the CDN object.
		b.Size = entry.LFS.Size
		b.LFS = &backend.LFSPointer{
			OID:  strings.TrimPrefix(entry.LFS.OID, "sha256:"),
			Size: entry.LFS.Size,
		}
		return b, nil
	}
	if entry.Size > backend.MaxBlobBytes {
		b.TooBig = true
		return b, nil
	}
	// Cache small bodies only; a near-10MB blob must not sit in the ETag map.
	content, _, err := r.get(ctx, r.rawURL(rev, pth), entry.Size <= 1<<20)
	if err != nil {
		return backend.Blob{}, fmt.Errorf("blob %s:%s: %w", rev, pth, err)
	}
	b.Content = content
	b.Binary = isBinary(content)
	return b, nil
}

// pathInfo resolves one path to its tree entry via POST /paths-info, which
// answers type, size, oid, and the LFS object in a single call.
func (r *Repo) pathInfo(ctx context.Context, rev, pth string) (apiTreeEntry, error) {
	body, err := r.postForm(ctx, r.apiURL("/paths-info/"+url.PathEscape(rev)), url.Values{"paths": {pth}})
	if err != nil {
		return apiTreeEntry{}, err
	}
	var items []apiTreeEntry
	if err := json.Unmarshal(body, &items); err != nil {
		return apiTreeEntry{}, err
	}
	for i := range items {
		if items[i].Path == pth && items[i].Type == "file" {
			return items[i], nil
		}
	}
	return apiTreeEntry{}, fmt.Errorf("not a file: %w", backend.ErrNotFound)
}

func (r *Repo) Commits(ctx context.Context, rev, pth string, n, skip int) ([]backend.Commit, bool, error) {
	if err := checkRefName(rev); err != nil {
		return nil, false, err
	}
	if pth != "" {
		// The commits endpoint has no path filter.
		return nil, false, fmt.Errorf("commits filtered by path: %w", backend.ErrUnsupported)
	}
	if n <= 0 || n > 100 {
		n = 100
	}
	if skip < 0 {
		skip = 0
	}
	// The Hub has no skip parameter, so skipping means walking Link pages
	// until the window plus one lookahead item is in hand.
	u := r.apiURL("/commits/" + url.PathEscape(rev) + "?limit=100")
	var items []apiCommit
	for page := 0; page < commitPageCap && u != "" && len(items) <= skip+n; page++ {
		body, next, err := r.get(ctx, u, true)
		if err != nil {
			return nil, false, fmt.Errorf("commits %s: %w", rev, err)
		}
		var batch []apiCommit
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, false, fmt.Errorf("commits %s: %w", rev, err)
		}
		items = append(items, batch...)
		u = next
	}
	more := len(items) > skip+n
	if skip > len(items) {
		skip = len(items)
	}
	end := min(skip+n, len(items))
	commits := make([]backend.Commit, 0, end-skip)
	for i := skip; i < end; i++ {
		commits = append(commits, items[i].toCommit())
	}
	return commits, more, nil
}

func (r *Repo) Commit(ctx context.Context, sha string) (backend.CommitDetail, error) {
	// The Hub API exposes no per-commit diff or patch.
	return backend.CommitDetail{}, fmt.Errorf("commit diffs have no hub endpoint: %w", backend.ErrUnsupported)
}

func (r *Repo) LastCommit(ctx context.Context, rev, pth string) (backend.Commit, error) {
	if err := checkRefName(rev); err != nil {
		return backend.Commit{}, err
	}
	dir := path.Dir(pth)
	if dir == "." {
		dir = ""
	}
	idx, err := r.lastCommitIndex(ctx, rev, dir)
	if err != nil {
		return backend.Commit{}, fmt.Errorf("last commit %s:%s: %w", rev, pth, err)
	}
	c, ok := idx[pth]
	if !ok {
		return backend.Commit{}, fmt.Errorf("last commit %s:%s: %w", rev, pth, backend.ErrNotFound)
	}
	return c, nil
}

// lastCommitIndex returns the path -> last-commit map for one directory,
// fetched via the expanded tree listing. The first caller fetches; everyone
// else (notably the server's parallel file-table fill) waits on the same
// entry, so one directory costs one upstream fetch.
func (r *Repo) lastCommitIndex(ctx context.Context, rev, dir string) (map[string]backend.Commit, error) {
	key := rev + "\x00" + dir
	r.lcMu.Lock()
	e, ok := r.lcCache[key]
	if !ok {
		if len(r.lcCache) >= maxLastCommitDirs {
			clear(r.lcCache)
		}
		e = &lastCommitDir{ready: make(chan struct{})}
		r.lcCache[key] = e
	}
	r.lcMu.Unlock()
	if ok {
		select {
		case <-e.ready:
			return e.index, e.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	u := r.apiURL("/tree/" + url.PathEscape(rev))
	if dir != "" {
		u += "/" + escapePath(dir)
	}
	items, err := r.listTree(ctx, u+"?expand=true")
	if err != nil {
		// Drop the failed entry so a later request retries instead of
		// serving a cached transient error.
		r.lcMu.Lock()
		delete(r.lcCache, key)
		r.lcMu.Unlock()
		e.err = err
		close(e.ready)
		return nil, err
	}
	idx := make(map[string]backend.Commit, len(items))
	for _, it := range items {
		if it.Last == nil {
			continue
		}
		date, _ := time.Parse(time.RFC3339, it.Last.Date)
		idx[it.Path] = backend.Commit{
			SHA:       it.Last.ID,
			Subject:   it.Last.Title,
			Author:    backend.Signature{Date: date},
			Committer: backend.Signature{Date: date},
		}
	}
	e.index = idx
	close(e.ready)
	return idx, nil
}

func (r *Repo) Blame(ctx context.Context, rev, pth string) ([]backend.BlameHunk, error) {
	return nil, fmt.Errorf("blame has no hub endpoint: %w", backend.ErrUnsupported)
}

func (r *Repo) Files(ctx context.Context, rev string) ([]string, error) {
	if err := checkRefName(rev); err != nil {
		return nil, err
	}
	items, err := r.listTree(ctx, r.apiURL("/tree/"+url.PathEscape(rev)+"?recursive=true"))
	if err != nil {
		return nil, fmt.Errorf("files %s: %w", rev, err)
	}
	var files []string
	for i := range items {
		if items[i].Type == "file" {
			files = append(files, items[i].Path)
		}
	}
	sort.Strings(files)
	return files, nil
}

func (r *Repo) Archive(ctx context.Context, rev, format, prefix string, w io.Writer) error {
	return fmt.Errorf("archives have no hub endpoint: %w", backend.ErrUnsupported)
}

// apiCommit is the element shape of /commits responses.
type apiCommit struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Authors []struct {
		User string `json:"user"`
	} `json:"authors"`
	Date string `json:"date"`
}

func (c *apiCommit) toCommit() backend.Commit {
	date, _ := time.Parse(time.RFC3339, c.Date)
	// The Hub exposes no commit emails and no separate committer; the same
	// stamp serves both sides, which is what the commit list renders.
	sig := backend.Signature{Date: date}
	if len(c.Authors) > 0 {
		sig.Name = c.Authors[0].User
		sig.Login = c.Authors[0].User
	}
	return backend.Commit{
		SHA:       c.ID,
		Subject:   c.Title,
		Body:      strings.TrimSpace(c.Message),
		Author:    sig,
		Committer: sig,
	}
}

// get performs one GET with the standard headers and returns the body plus
// the rel="next" Link, serving and refreshing the ETag cache when cacheable.
func (r *Repo) get(ctx context.Context, u string, cacheable bool) (body []byte, next string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	r.setHeaders(req)
	var cached cacheEntry
	var haveCached bool
	if cacheable {
		r.mu.Lock()
		cached, haveCached = r.cache[u]
		r.mu.Unlock()
		if haveCached {
			req.Header.Set("If-None-Match", cached.etag)
		}
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified && haveCached {
		return cached.body, cached.next, nil
	}
	if err := checkStatus(resp, u); err != nil {
		return nil, "", err
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", errPath(u), err)
	}
	next = linkNext(resp.Header.Get("Link"))
	if etag := resp.Header.Get("ETag"); cacheable && etag != "" {
		r.mu.Lock()
		if len(r.cache) >= maxCacheEntries {
			clear(r.cache) // dumb full eviction; see maxCacheEntries
		}
		r.cache[u] = cacheEntry{etag: etag, body: body, next: next}
		r.mu.Unlock()
	}
	return body, next, nil
}

// postForm performs one POST with form-encoded values; not cached.
func (r *Repo) postForm(ctx context.Context, u string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	r.setHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp, u); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", errPath(u), err)
	}
	return body, nil
}

func (r *Repo) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "gitview")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
}

// checkStatus maps non-2xx responses to the SPI error model: 404 wraps
// ErrNotFound, and so do 401 and 403, because a gated or private repo
// without a token must read as "not here" rather than a server error; 429
// wraps RateLimitError with the Retry-After reset; everything else carries
// status and a trimmed body.
func checkStatus(resp *http.Response, u string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("hub: %s: %w", errPath(u), backend.ErrNotFound)
	case http.StatusTooManyRequests:
		return fmt.Errorf("hub: %s: %w", errPath(u), &backend.RateLimitError{Reset: rateReset(resp.Header)})
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	return fmt.Errorf("hub: %s: status %d: %s", errPath(u), resp.StatusCode, strings.TrimSpace(string(b)))
}

func rateReset(h http.Header) time.Time {
	v := h.Get("Retry-After")
	if v == "" {
		return time.Time{}
	}
	if sec, err := strconv.Atoi(v); err == nil {
		return time.Now().Add(time.Duration(sec) * time.Second)
	}
	if t, err := http.ParseTime(v); err == nil {
		return t
	}
	return time.Time{}
}

// linkNext extracts the rel="next" target from an RFC 5988 Link header.
// Copied from the ghapi backend; backends do not import each other.
func linkNext(h string) string {
	for part := range strings.SplitSeq(h, ",") {
		var target string
		var isNext bool
		for p := range strings.SplitSeq(part, ";") {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "<") && strings.HasSuffix(p, ">") {
				target = p[1 : len(p)-1]
			} else if p == `rel="next"` {
				isNext = true
			}
		}
		if isNext && target != "" {
			return target
		}
	}
	return ""
}

// escapePath escapes each path segment while keeping the separators.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// errPath strips query and host so errors never echo tokens or full URLs.
func errPath(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Path
	}
	return u
}

// checkRefName rejects revision-walk syntax and flag-shaped input before
// it ever becomes a URL path segment. Copied from the local backend so all
// backends accept the same ref grammar.
func checkRefName(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "~^:?*[\\ \t\n@{") {
		return fmt.Errorf("ref %q: %w", ref, backend.ErrNotFound)
	}
	return nil
}

// sortEntries orders dirs first, then files, case-insensitive within each
// group, the way github.com lists them. Copied so every backend agrees.
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

// isBinary sniffs for a NUL byte in the first 8000 bytes, like git.
func isBinary(content []byte) bool {
	head := content
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) >= 0
}
