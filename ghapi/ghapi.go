// Package ghapi implements backend.Repo over the GitHub REST API v3 with
// a hand-rolled net/http client. It owns auth headers, conditional requests
// (ETag), Link-header pagination, and rate-limit errors. LastCommit and
// Blame return backend.ErrUnsupported: REST has no affordable shape for
// either (GraphQL does, later milestone).
package ghapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamnd/gitview/backend"
)

const apiBase = "https://api.github.com"

// maxCacheEntries bounds the ETag cache; eviction dumps everything, which
// is fine because a browsing session re-warms the few URLs it actually hits.
const maxCacheEntries = 512

// refPageCap bounds Refs pagination at 10 pages of 100, i.e. 1000 branches
// and 1000 tags; repos beyond that are outside gitview's browsing use case.
const refPageCap = 10

// RateLimitError is the shared backend type; the alias keeps the natural
// name available to ghapi callers.
type RateLimitError = backend.RateLimitError

// Repo is a backend.Repo bound to one owner/repo on one API host.
type Repo struct {
	owner  string
	repo   string
	token  string
	base   string
	client *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry // request URL -> last 200 response
}

type cacheEntry struct {
	etag string
	body []byte
	next string // rel="next" Link of the cached response
}

// New returns a Repo talking to api.github.com. An empty token means
// anonymous access (60 requests/hour instead of 5000).
func New(owner, repo, token string) *Repo {
	return NewWithBaseURL(owner, repo, token, apiBase, nil)
}

// NewWithBaseURL is New with the API host and HTTP client injectable, which
// tests use to point at a mock server. A nil client means http.DefaultClient.
func NewWithBaseURL(owner, repo, token, baseURL string, client *http.Client) *Repo {
	if client == nil {
		client = http.DefaultClient
	}
	return &Repo{
		owner:  owner,
		repo:   repo,
		token:  token,
		base:   strings.TrimSuffix(baseURL, "/"),
		client: client,
		cache:  make(map[string]cacheEntry),
	}
}

func (r *Repo) Info(ctx context.Context) (backend.Info, error) {
	body, _, err := r.getJSON(ctx, r.repoURL(""), true)
	if err != nil {
		return backend.Info{}, fmt.Errorf("info: %w", err)
	}
	var v struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		DefaultBranch string `json:"default_branch"`
		CloneURL      string `json:"clone_url"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return backend.Info{}, fmt.Errorf("info: %w", err)
	}
	return backend.Info{
		Owner:         v.Owner.Login,
		Name:          v.Name,
		Description:   v.Description,
		DefaultBranch: v.DefaultBranch,
		CloneURL:      v.CloneURL,
		Mirror:        true, // ghapi is always a read-only mirror of github.com
	}, nil
}

func (r *Repo) Refs(ctx context.Context) (backend.Refs, error) {
	branches, err := r.listRefs(ctx, "branches")
	if err != nil {
		return backend.Refs{}, fmt.Errorf("refs: %w", err)
	}
	tags, err := r.listRefs(ctx, "tags")
	if err != nil {
		return backend.Refs{}, fmt.Errorf("refs: %w", err)
	}
	// Tags keep API order, which is GitHub's own newest-first tag order.
	return backend.Refs{Branches: branches, Tags: tags}, nil
}

// listRefs pages through /branches or /tags following Link rel="next".
// The list endpoints carry no dates, so Ref.Date stays zero (the SPI
// allows "zero if unknown").
func (r *Repo) listRefs(ctx context.Context, kind string) ([]backend.Ref, error) {
	u := r.repoURL("/" + kind + "?per_page=100")
	var refs []backend.Ref
	for page := 0; page < refPageCap && u != ""; page++ {
		body, next, err := r.getJSON(ctx, u, true)
		if err != nil {
			return nil, err
		}
		var items []struct {
			Name   string `json:"name"`
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("%s: %w", kind, err)
		}
		for _, it := range items {
			refs = append(refs, backend.Ref{Name: it.Name, SHA: it.Commit.SHA})
		}
		u = next
	}
	return refs, nil
}

func (r *Repo) Resolve(ctx context.Context, ref string) (string, error) {
	if err := checkRefName(ref); err != nil {
		return "", err
	}
	body, _, err := r.getJSON(ctx, r.repoURL("/commits/"+url.PathEscape(ref)), true)
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

func (r *Repo) Tree(ctx context.Context, rev, pth string) ([]backend.TreeEntry, error) {
	if err := checkRefName(rev); err != nil {
		return nil, err
	}
	body, _, err := r.getJSON(ctx, r.contentsURL(pth, rev), true)
	if err != nil {
		return nil, fmt.Errorf("tree %s:%s: %w", rev, pth, err)
	}
	// A JSON object instead of an array means path is a file; the route
	// logic retries as Blob on ErrNotFound, mirroring localgit.
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("tree %s:%s: not a directory: %w", rev, pth, backend.ErrNotFound)
	}
	var items []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		SHA  string `json:"sha"`
		Size int64  `json:"size"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("tree %s:%s: %w", rev, pth, err)
	}
	entries := make([]backend.TreeEntry, 0, len(items))
	for _, it := range items {
		e := backend.TreeEntry{Name: it.Name, Path: it.Path, SHA: it.SHA, Size: -1}
		// Modes are synthesized: the contents API does not expose them and
		// the UI only displays kinds (executable bit loss is cosmetic).
		switch it.Type {
		case "dir":
			e.Kind, e.Mode = backend.KindDir, "040000"
		case "symlink":
			e.Kind, e.Mode, e.Size = backend.KindSymlink, "120000", it.Size
		case "submodule":
			e.Kind, e.Mode = backend.KindSubmodule, "160000"
		default: // "file"
			e.Kind, e.Mode, e.Size = backend.KindFile, "100644", it.Size
		}
		entries = append(entries, e)
	}
	sortEntries(entries)
	return entries, nil
}

func (r *Repo) Blob(ctx context.Context, rev, pth string) (backend.Blob, error) {
	if err := checkRefName(rev); err != nil {
		return backend.Blob{}, err
	}
	body, _, err := r.getJSON(ctx, r.contentsURL(pth, rev), true)
	if err != nil {
		return backend.Blob{}, fmt.Errorf("blob %s:%s: %w", rev, pth, err)
	}
	// An array means path is a directory, not a file.
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return backend.Blob{}, fmt.Errorf("blob %s:%s: is a directory: %w", rev, pth, backend.ErrNotFound)
	}
	var v struct {
		Type     string `json:"type"`
		Size     int64  `json:"size"`
		SHA      string `json:"sha"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		Target   string `json:"target"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return backend.Blob{}, fmt.Errorf("blob %s:%s: %w", rev, pth, err)
	}
	b := backend.Blob{Path: pth, Size: v.Size}
	switch v.Type {
	case "symlink":
		// REST exposes the link target; render it as the blob content,
		// the same way git stores a symlink.
		b.Content = []byte(v.Target)
		b.Symlink = true
		return b, nil
	case "file":
	default: // "dir", "submodule", anything new
		return backend.Blob{}, fmt.Errorf("blob %s:%s: type %q: %w", rev, pth, v.Type, backend.ErrNotFound)
	}
	if v.Size > backend.MaxBlobBytes {
		b.TooBig = true
		return b, nil
	}
	var content []byte
	switch {
	case v.Encoding == "base64" && v.Content != "":
		content, err = decodeBase64(v.Content)
	case v.Content == "":
		// The contents API inlines content only up to 1 MB; between 1 MB
		// and 100 MB it returns metadata with empty content and the blob
		// must be fetched by SHA.
		content, err = r.gitBlob(ctx, v.SHA)
	default:
		content = []byte(v.Content)
	}
	if err != nil {
		return backend.Blob{}, fmt.Errorf("blob %s:%s: %w", rev, pth, err)
	}
	b.Content = content
	b.Binary = isBinary(content)
	b.LFS = parseLFSPointer(content)
	return b, nil
}

// gitBlob fetches base64 content by blob SHA. Not ETag-cached: this path
// only runs for bodies over 1 MB, which the cache must not hold.
func (r *Repo) gitBlob(ctx context.Context, sha string) ([]byte, error) {
	if sha == "" {
		return nil, fmt.Errorf("empty blob sha: %w", backend.ErrNotFound)
	}
	body, _, err := r.getJSON(ctx, r.repoURL("/git/blobs/"+url.PathEscape(sha)), false)
	if err != nil {
		return nil, err
	}
	var v struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("git blob %s: %w", sha, err)
	}
	if v.Encoding != "base64" {
		return []byte(v.Content), nil
	}
	return decodeBase64(v.Content)
}

func (r *Repo) Commits(ctx context.Context, rev, pth string, n, skip int) ([]backend.Commit, bool, error) {
	if err := checkRefName(rev); err != nil {
		return nil, false, err
	}
	if n <= 0 || n > 100 {
		n = 100
	}
	if skip < 0 {
		skip = 0
	}
	// The API pages, the SPI skips. Aligned skips map exactly; misaligned
	// skips fetch one larger first page and slice, which is good enough at
	// our page sizes (the server pages by fixed strides of n).
	perPage, page, offset := n, skip/n+1, 0
	if skip%n != 0 {
		perPage, page, offset = n+skip, 1, skip
	}
	q := url.Values{
		"sha":      {rev},
		"per_page": {strconv.Itoa(perPage)},
		"page":     {strconv.Itoa(page)},
	}
	if pth != "" {
		q.Set("path", pth)
	}
	body, next, err := r.getJSON(ctx, r.repoURL("/commits?"+q.Encode()), true)
	if err != nil {
		return nil, false, fmt.Errorf("commits %s: %w", rev, err)
	}
	var items []apiCommit
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, false, fmt.Errorf("commits %s: %w", rev, err)
	}
	more := next != "" || len(items) > offset+n
	if offset > len(items) {
		offset = len(items)
	}
	end := min(offset+n, len(items))
	commits := make([]backend.Commit, 0, end-offset)
	for i := offset; i < end; i++ {
		commits = append(commits, items[i].toCommit())
	}
	return commits, more, nil
}

func (r *Repo) Commit(ctx context.Context, sha string) (backend.CommitDetail, error) {
	if err := checkRefName(sha); err != nil {
		return backend.CommitDetail{}, err
	}
	body, _, err := r.getJSON(ctx, r.repoURL("/commits/"+url.PathEscape(sha)), true)
	if err != nil {
		return backend.CommitDetail{}, fmt.Errorf("commit %s: %w", sha, err)
	}
	var v struct {
		apiCommit
		Stats struct {
			Additions int `json:"additions"`
			Deletions int `json:"deletions"`
		} `json:"stats"`
		Files []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
			Status           string `json:"status"`
			Additions        int    `json:"additions"`
			Deletions        int    `json:"deletions"`
			Changes          int    `json:"changes"`
			Patch            string `json:"patch"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return backend.CommitDetail{}, fmt.Errorf("commit %s: %w", sha, err)
	}
	detail := backend.CommitDetail{
		Commit: v.toCommit(),
		Stats: backend.StatTotal{
			FilesChanged: len(v.Files),
			Additions:    v.Stats.Additions,
			Deletions:    v.Stats.Deletions,
		},
	}
	for _, f := range v.Files {
		fd := backend.FileDiff{
			OldPath:   f.Filename,
			NewPath:   f.Filename,
			Status:    statusLetter(f.Status),
			Additions: f.Additions,
			Deletions: f.Deletions,
		}
		if f.PreviousFilename != "" {
			fd.OldPath = f.PreviousFilename
		}
		if f.Patch != "" {
			fd.Hunks, fd.TooLarge = parseFilePatch(fd.OldPath, fd.NewPath, f.Patch)
		} else if f.Changes > 0 || (fd.Status != backend.Renamed && fd.Status != backend.Copied) {
			// No patch text means a binary file, unless this is a pure
			// rename/copy with zero changed lines.
			fd.Binary = true
		}
		detail.Files = append(detail.Files, fd)
	}
	return detail, nil
}

func (r *Repo) LastCommit(ctx context.Context, rev, pth string) (backend.Commit, error) {
	// REST needs one /commits call per file-table row, which detonates the
	// rate budget; GraphQL batches a directory in one call (later milestone).
	return backend.Commit{}, fmt.Errorf("last commit needs one request per row over REST: %w", backend.ErrUnsupported)
}

func (r *Repo) Blame(ctx context.Context, rev, pth string) ([]backend.BlameHunk, error) {
	// REST v3 has no blame endpoint; GraphQL's Blob.blame does.
	return nil, fmt.Errorf("blame has no REST endpoint: %w", backend.ErrUnsupported)
}

func (r *Repo) Files(ctx context.Context, rev string) ([]string, error) {
	if err := checkRefName(rev); err != nil {
		return nil, err
	}
	body, _, err := r.getJSON(ctx, r.repoURL("/git/trees/"+url.PathEscape(rev)+"?recursive=1"), true)
	if err != nil {
		return nil, fmt.Errorf("files %s: %w", rev, err)
	}
	var v struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("files %s: %w", rev, err)
	}
	// truncated==true past ~100k entries: return the partial index rather
	// than fail; the finder shows a partial-index note.
	var files []string
	for _, e := range v.Tree {
		if e.Type == "blob" {
			files = append(files, e.Path)
		}
	}
	sort.Strings(files)
	return files, nil
}

func (r *Repo) Archive(ctx context.Context, rev, format, prefix string, w io.Writer) error {
	if err := checkRefName(rev); err != nil {
		return err
	}
	var ep string
	switch format {
	case "zip":
		ep = "zipball"
	case "tar.gz":
		ep = "tarball"
	default:
		return fmt.Errorf("archive format %q: %w", format, backend.ErrUnsupported)
	}
	// prefix is ignored: codeload fixes the top-level directory name to
	// {owner}-{repo}-{shortsha}/ and re-rolling the archive would mean
	// buffering the whole thing.
	u := r.repoURL("/" + ep + "/" + url.PathEscape(rev))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	r.setHeaders(req)
	// The API answers 302 to codeload; the client's default redirect
	// policy follows it and net/http strips Authorization cross-host.
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("archive %s: %w", rev, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp, u); err != nil {
		return fmt.Errorf("archive %s: %w", rev, err)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("archive %s: %w", rev, err)
	}
	return nil
}

// apiCommit is the shared element shape of /commits and /commits/{sha}.
type apiCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message   string    `json:"message"`
		Author    apiGitSig `json:"author"`
		Committer apiGitSig `json:"committer"`
	} `json:"commit"`
	Author    *apiUser `json:"author"`
	Committer *apiUser `json:"committer"`
	Parents   []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}

type apiGitSig struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

type apiUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

func (c *apiCommit) toCommit() backend.Commit {
	subject, body := splitMessage(c.Commit.Message)
	out := backend.Commit{
		SHA:       c.SHA,
		Subject:   subject,
		Body:      body,
		Author:    c.Commit.Author.toSignature(),
		Committer: c.Commit.Committer.toSignature(),
	}
	// The top-level user objects are null when GitHub cannot map the email
	// to an account; login and avatar exist only when they are non-null.
	if c.Author != nil {
		out.Author.Login, out.Author.AvatarURL = c.Author.Login, c.Author.AvatarURL
	}
	if c.Committer != nil {
		out.Committer.Login, out.Committer.AvatarURL = c.Committer.Login, c.Committer.AvatarURL
	}
	for _, p := range c.Parents {
		out.Parents = append(out.Parents, p.SHA)
	}
	return out
}

func (s apiGitSig) toSignature() backend.Signature {
	sig := backend.Signature{Name: s.Name, Email: s.Email}
	sig.Date, _ = time.Parse(time.RFC3339, s.Date)
	return sig
}

// splitMessage takes the first line as subject and everything after the
// first newline, trimmed, as body.
func splitMessage(msg string) (subject, body string) {
	subject, body, _ = strings.Cut(msg, "\n")
	return strings.TrimRight(subject, "\r"), strings.TrimSpace(body)
}

func statusLetter(s string) backend.DiffStatus {
	switch s {
	case "added":
		return backend.Added
	case "removed":
		return backend.Deleted
	case "renamed":
		return backend.Renamed
	case "copied":
		return backend.Copied
	default: // "modified", "changed", anything new
		return backend.Modified
	}
}

// parseFilePatch reuses backend.ParsePatch on the bare "@@" hunk text the
// commits API returns by prepending a synthetic diff --git header.
func parseFilePatch(oldPath, newPath, patch string) (hunks []backend.Hunk, tooLarge bool) {
	syn := fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n%s\n",
		oldPath, newPath, oldPath, newPath, patch)
	files := backend.ParsePatch(syn)
	if len(files) == 0 {
		return nil, false
	}
	return files[0].Hunks, files[0].TooLarge
}

// getJSON performs one GET with the standard headers and returns the body
// plus the rel="next" Link, serving and refreshing the ETag cache when
// cacheable. Archives and over-1MB blob bodies bypass the cache.
func (r *Repo) getJSON(ctx context.Context, u string, cacheable bool) (body []byte, next string, err error) {
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
		// 304 costs zero rate-limit points; serve the cached body.
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

func (r *Repo) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "gitview")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
}

// checkStatus maps non-2xx responses to the SPI error model: 404, 422
// (bad ref) and 409 (empty repository) wrap ErrNotFound; 429, and 403 when
// the quota is spent, wrap RateLimitError; everything else carries status
// and a trimmed body.
func checkStatus(resp *http.Response, u string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	switch {
	case resp.StatusCode == http.StatusNotFound ||
		resp.StatusCode == http.StatusUnprocessableEntity ||
		resp.StatusCode == http.StatusConflict:
		return fmt.Errorf("github: %s: %w", errPath(u), backend.ErrNotFound)
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden &&
			(resp.Header.Get("X-Ratelimit-Remaining") == "0" || resp.Header.Get("Retry-After") != ""):
		return fmt.Errorf("github: %s: %w", errPath(u), &RateLimitError{Reset: rateReset(resp.Header)})
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	return fmt.Errorf("github: %s: status %d: %s", errPath(u), resp.StatusCode, strings.TrimSpace(string(b)))
}

func rateReset(h http.Header) time.Time {
	if v := h.Get("X-Ratelimit-Reset"); v != "" {
		if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Unix(sec, 0)
		}
	}
	if v := h.Get("Retry-After"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil {
			return time.Now().Add(time.Duration(sec) * time.Second)
		}
	}
	return time.Time{}
}

// linkNext extracts the rel="next" target from an RFC 5988 Link header.
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

func (r *Repo) repoURL(suffix string) string {
	return r.base + "/repos/" + url.PathEscape(r.owner) + "/" + url.PathEscape(r.repo) + suffix
}

func (r *Repo) contentsURL(pth, rev string) string {
	u := r.repoURL("/contents")
	if pth != "" {
		u += "/" + escapePath(pth)
	}
	return u + "?ref=" + url.QueryEscape(rev)
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

// decodeBase64 decodes the contents API form, which embeds newlines.
func decodeBase64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode base64 content: %w", err)
	}
	return b, nil
}

// checkRefName rejects revision-walk syntax and flag-shaped input before
// it ever becomes a URL path segment. Copied from localgit so both
// backends accept the same ref grammar.
func checkRefName(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "~^:?*[\\ \t\n@{") {
		return fmt.Errorf("ref %q: %w", ref, backend.ErrNotFound)
	}
	return nil
}

// sortEntries orders dirs and submodules first, then files and symlinks,
// case-insensitive within each group, the way github.com lists them.
// Copied from localgit so every backend agrees.
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

// parseLFSPointer detects the git-lfs pointer text the contents API serves
// in place of the object, which is exactly what the LFS banner wants.
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
