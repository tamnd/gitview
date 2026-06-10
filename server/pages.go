package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/gitview/backend"
)

const commitsPerPage = 35

// commitCounter is implemented by backends that can count history cheaply.
type commitCounter interface {
	CommitCount(ctx context.Context, rev string) (int, error)
}

// aheadBehinder is implemented by backends that can compare branches.
type aheadBehinder interface {
	AheadBehind(ctx context.Context, rev, base string) (ahead, behind int, err error)
}

// handleHome renders the repository front page: file table plus readme at
// the default branch.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request, rs *repoState) {
	refs, err := rs.cachedRefs(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(refs.Branches) == 0 {
		title := rs.key()
		if rs.info.Description != "" {
			title += ": " + rs.info.Description
		}
		p := s.repoPage(r, rs, title, "code", "", "")
		s.render(w, r, http.StatusOK, "empty", map[string]any{
			"Page":       p,
			"CloneURL":   rs.info.CloneURL,
			"CloneLabel": cloneLabel(rs.info.CloneURL),
		})
		return
	}
	ref := rs.info.DefaultBranch
	rev, err := rs.repo.Resolve(r.Context(), ref)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderTree(w, r, rs, ref, rev, "")
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request, rs *repoState) {
	ref, rev, pth, err := s.resolveRefPath(r, rs, r.PathValue("rest"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderTree(w, r, rs, ref, rev, pth)
}

func (s *Server) renderTree(w http.ResponseWriter, r *http.Request, rs *repoState, ref, rev, pth string) {
	entries, err := rs.repo.Tree(r.Context(), rev, pth)
	if err != nil {
		// A blob URL typed as a tree URL redirects, like github.com.
		if _, berr := rs.repo.Blob(r.Context(), rev, pth); berr == nil {
			http.Redirect(w, r, "/"+rs.key()+"/blob/"+ref+"/"+pth, http.StatusFound)
			return
		}
		s.fail(w, r, err)
		return
	}

	key := rs.key()
	paths := make([]string, 0, len(entries)+1)
	paths = append(paths, pth)
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	last := rs.fillLastCommits(r.Context(), rev, paths)

	rows := make([]treeRow, 0, len(entries))
	for _, e := range entries {
		kind := "blob"
		if e.Kind == backend.KindDir {
			kind = "tree"
		}
		row := treeRow{
			Name:  e.Name,
			URL:   "/" + key + "/" + kind + "/" + ref + "/" + e.Path,
			Icon:  entryIcon(e.Kind),
			IsDir: e.Kind == backend.KindDir,
		}
		if e.Kind == backend.KindSubmodule {
			// Submodules never link anywhere (doc 08 §5.8): the row is a
			// badge showing the pinned commit.
			row.URL = ""
			row.SubSHA = shortSHA(e.SHA)
		}
		if c, ok := last[e.Path]; ok {
			row.LastMsg = c.Subject
			row.LastURL = "/" + key + "/commit/" + c.SHA
			row.LastAt = timeago(c.Author.Date)
		}
		rows = append(rows, row)
	}

	// Latest commit bar for the directory itself.
	var head map[string]any
	if c, ok := last[pth]; ok {
		head = map[string]any{
			"SHA":     c.SHA,
			"Short":   shortSHA(c.SHA),
			"URL":     "/" + key + "/commit/" + c.SHA,
			"Subject": c.Subject,
			"Author":  c.Author.Name,
			"At":      timeago(c.Author.Date),
		}
	}

	commitCount := 0
	if cc, ok := rs.repo.(commitCounter); ok && pth == "" {
		if n, err := cc.CommitCount(r.Context(), rev); err == nil {
			commitCount = n
		}
	}

	var readmeName string
	var readme template.HTML
	if name, ok := findReadme(entries); ok && isMarkdownFile(name) {
		full := path.Join(pth, name)
		if b, err := rs.repo.Blob(r.Context(), rev, full); err == nil && !b.Binary && b.LFS == nil && !b.TooBig {
			readmeName = name
			readme = renderMarkdown(mdContext{RepoPath: "/" + key, Ref: ref, Dir: pth}, b.Content)
		}
	}

	title := rs.key()
	switch {
	case pth != "":
		title = rs.info.Name + "/" + pth + " at " + ref + " · " + rs.key()
	case ref != rs.info.DefaultBranch:
		title = rs.key() + " at " + ref
	case rs.info.Description != "":
		title = rs.key() + ": " + rs.info.Description
	}
	p := s.repoPage(r, rs, title, "code", ref, pth)
	s.render(w, r, http.StatusOK, "tree", map[string]any{
		"Page":        p,
		"IsRoot":      pth == "",
		"CloneURL":    rs.info.CloneURL,
		"CloneLabel":  cloneLabel(rs.info.CloneURL),
		"Crumbs":      crumbs(key, "tree", ref, pth),
		"ParentURL":   parentURL(key, ref, pth),
		"Rows":        rows,
		"Head":        head,
		"CommitCount": commitCount,
		"CommitsURL":  commitsURL(key, ref, pth),
		"ReadmeName":  readmeName,
		"Readme":      readme,
	})
}

// cloneLabel picks the clone-box caption: a remote URL gets "HTTPS" like
// github.com, a bare filesystem path gets the honest "Local path".
func cloneLabel(u string) string {
	if strings.Contains(u, "://") || strings.HasPrefix(u, "git@") {
		return "HTTPS"
	}
	return "Local path"
}

func parentURL(key, ref, pth string) string {
	if pth == "" {
		return ""
	}
	dir := path.Dir(pth)
	if dir == "." || dir == "/" {
		return "/" + key + "/tree/" + ref
	}
	return "/" + key + "/tree/" + ref + "/" + dir
}

func commitsURL(key, ref, pth string) string {
	u := "/" + key + "/commits/" + ref
	if pth != "" {
		u += "/" + pth
	}
	return u
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request, rs *repoState) {
	ref, rev, pth, err := s.resolveRefPath(r, rs, r.PathValue("rest"))
	if err != nil || pth == "" {
		if err == nil {
			err = fmt.Errorf("blob needs a path: %w", backend.ErrNotFound)
		}
		s.fail(w, r, err)
		return
	}
	blob, err := rs.repo.Blob(r.Context(), rev, pth)
	if err != nil {
		// A directory typed as a blob URL redirects to the tree.
		if _, terr := rs.repo.Tree(r.Context(), rev, pth); terr == nil {
			http.Redirect(w, r, "/"+rs.key()+"/tree/"+ref+"/"+pth, http.StatusFound)
			return
		}
		s.fail(w, r, err)
		return
	}

	key := rs.key()
	name := path.Base(pth)
	kind := classifyBlob(name, blob, r.URL.Query().Get("plain") == "1")
	data := map[string]any{
		"Page":     s.repoPage(r, rs, rs.info.Name+"/"+pth+" at "+ref+" · "+key, "code", ref, pth),
		"Crumbs":   crumbs(key, "blob", ref, pth),
		"Name":     name,
		"Size":     blob.Size,
		"RawURL":   "/" + key + "/raw/" + ref + "/" + pth,
		"BlameURL": "/" + key + "/blame/" + ref + "/" + pth,
		"HistURL":  commitsURL(key, ref, pth),
		"PlainURL": "/" + key + "/blob/" + ref + "/" + pth + "?plain=1",
		"RichURL":  "/" + key + "/blob/" + ref + "/" + pth,
		"Markdown": isMarkdownFile(name),
	}
	switch kind {
	case blobSymlink:
		data["Kind"] = "symlink"
		data["Target"] = strings.TrimSpace(string(blob.Content))
	case blobMarkdown:
		data["Kind"] = "markdown"
		data["Rendered"] = renderMarkdown(mdContext{RepoPath: "/" + key, Ref: ref, Dir: path.Dir(strings.TrimSuffix(pth, name))}, blob.Content)
	case blobImage:
		data["Kind"] = "image"
	case blobLFS:
		data["Kind"] = "lfs"
		data["Size"] = blob.LFS.Size
		data["OID"] = blob.LFS.OID
	case blobTooBig:
		data["Kind"] = "toobig"
	case blobBinary:
		data["Kind"] = "binary"
	case blobEmpty:
		data["Kind"] = "empty"
	default:
		data["Kind"] = "code"
		lines := codeLines(name, string(blob.Content))
		data["Lines"] = lines
		data["LineCount"] = len(lines)
	}
	if mctx, ok := data["Kind"].(string); ok && mctx == "markdown" {
		// Markdown line count still shown in the toolbar.
		data["LineCount"] = len(splitLines(string(blob.Content)))
	}
	s.render(w, r, http.StatusOK, "blob", data)
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request, rs *repoState) {
	_, rev, pth, err := s.resolveRefPath(r, rs, r.PathValue("rest"))
	if err != nil || pth == "" {
		http.NotFound(w, r)
		return
	}
	blob, err := rs.repo.Blob(r.Context(), rev, pth)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if blob.TooBig {
		http.Error(w, "file exceeds the raw size limit", http.StatusRequestEntityTooLarge)
		return
	}
	h := w.Header()
	ct := "text/plain; charset=utf-8"
	if mime, ok := imageExts[strings.ToLower(path.Ext(pth))]; ok {
		ct = mime
	} else if blob.Binary {
		ct = "application/octet-stream"
	}
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(blob.Content)
}

func (s *Server) handleCommits(w http.ResponseWriter, r *http.Request, rs *repoState) {
	rest := r.PathValue("rest")
	var ref, rev, pth string
	var err error
	if rest == "" {
		ref = rs.info.DefaultBranch
		rev, err = rs.repo.Resolve(r.Context(), ref)
	} else {
		ref, rev, pth, err = s.resolveRefPath(r, rs, rest)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}

	pageNum, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pageNum < 1 {
		pageNum = 1
	}
	commits, more, err := rs.repo.Commits(r.Context(), rev, pth, commitsPerPage, (pageNum-1)*commitsPerPage)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	type commitRow struct {
		SHA, Short, URL, Subject, Body, Author, At string
		TreeURL                                    string
	}
	type dayGroup struct {
		Day  string
		Rows []commitRow
	}
	key := rs.key()
	var groups []dayGroup
	for _, c := range commits {
		day := "Commits on " + c.Author.Date.Format("Jan 2, 2006")
		row := commitRow{
			SHA: c.SHA, Short: shortSHA(c.SHA),
			URL: "/" + key + "/commit/" + c.SHA, Subject: c.Subject, Body: c.Body,
			Author: c.Author.Name, At: timeago(c.Author.Date),
			TreeURL: "/" + key + "/tree/" + c.SHA,
		}
		if len(groups) == 0 || groups[len(groups)-1].Day != day {
			groups = append(groups, dayGroup{Day: day})
		}
		groups[len(groups)-1].Rows = append(groups[len(groups)-1].Rows, row)
	}

	base := commitsURL(key, ref, pth)
	prev, next := "", ""
	if pageNum > 2 {
		prev = base + "?page=" + strconv.Itoa(pageNum-1)
	} else if pageNum == 2 {
		prev = base
	}
	if more {
		next = base + "?page=" + strconv.Itoa(pageNum+1)
	}

	p := s.repoPage(r, rs, "Commits · "+key, "commits", ref, pth)
	s.render(w, r, http.StatusOK, "commits", map[string]any{
		"Page": p, "Groups": groups, "Prev": prev, "Next": next, "Empty": len(commits) == 0,
	})
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{4,40}$`)

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request, rs *repoState) {
	sha := strings.ToLower(r.PathValue("sha"))
	if !shaRe.MatchString(sha) {
		s.fail(w, r, backend.ErrNotFound)
		return
	}
	rev, err := rs.repo.Resolve(r.Context(), sha)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Abbreviated ids redirect to the full-SHA canonical URL, like
	// github.com.
	if rev != sha {
		http.Redirect(w, r, "/"+rs.key()+"/commit/"+rev, http.StatusMovedPermanently)
		return
	}
	detail, err := rs.repo.Commit(r.Context(), rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	key := rs.key()
	type fileView struct {
		Anchor    string
		OldPath   string
		Path      string
		Status    backend.DiffStatus
		Renamed   bool
		Additions int
		Deletions int
		Binary    bool
		TooLarge  bool
		Hunks     []backend.Hunk
		BlobURL   string
	}
	files := make([]fileView, 0, len(detail.Files))
	for _, f := range detail.Files {
		fv := fileView{
			OldPath:   f.OldPath,
			Path:      f.NewPath,
			Status:    f.Status,
			Renamed:   f.Status == backend.Renamed || f.Status == backend.Copied,
			Additions: f.Additions,
			Deletions: f.Deletions,
			Binary:    f.Binary,
			TooLarge:  f.TooLarge,
			Hunks:     f.Hunks,
		}
		if f.Status == backend.Deleted {
			fv.Path = f.OldPath
		} else {
			fv.BlobURL = "/" + key + "/blob/" + detail.SHA + "/" + f.NewPath
		}
		// The anchor github.com uses: sha256 of the displayed path.
		fv.Anchor = fmt.Sprintf("diff-%x", sha256.Sum256([]byte(fv.Path)))
		files = append(files, fv)
	}

	parents := make([]map[string]string, 0, len(detail.Parents))
	for _, par := range detail.Parents {
		parents = append(parents, map[string]string{
			"Short": shortSHA(par), "URL": "/" + key + "/commit/" + par,
		})
	}

	p := s.repoPage(r, rs, detail.Subject+" · "+key+"@"+shortSHA(detail.SHA), "commits", "", "")
	s.render(w, r, http.StatusOK, "commit", map[string]any{
		"Page":    p,
		"Commit":  detail.Commit,
		"Short":   shortSHA(detail.SHA),
		"Parents": parents,
		"Stats":   detail.Stats,
		"Files":   files,
		"TreeURL": "/" + key + "/tree/" + detail.SHA,
	})
}

func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request, rs *repoState) {
	refs, err := rs.cachedRefs(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type branchRow struct {
		Name, URL, SHA, At string
		Ahead, Behind      int
		HasCompare         bool
		Default            bool
	}
	key := rs.key()
	ab, hasAB := rs.repo.(aheadBehinder)
	var def *branchRow
	var rows []branchRow
	for _, b := range refs.Branches {
		row := branchRow{
			Name: b.Name, SHA: b.SHA, At: timeago(b.Date),
			URL:     "/" + key + "/tree/" + b.Name,
			Default: b.Name == rs.info.DefaultBranch,
		}
		if row.Default {
			d := row
			def = &d
			continue
		}
		// Compare against the default branch, capped so a giant branch
		// list stays cheap.
		if hasAB && len(rows) < 100 {
			if a, behind, err := ab.AheadBehind(r.Context(), b.Name, rs.info.DefaultBranch); err == nil {
				row.Ahead, row.Behind, row.HasCompare = a, behind, true
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	p := s.repoPage(r, rs, "Branches · "+key, "branches", "", "")
	s.render(w, r, http.StatusOK, "branches", map[string]any{
		"Page": p, "Default": def, "Rows": rows,
	})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request, rs *repoState) {
	refs, err := rs.cachedRefs(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type tagRow struct {
		Name, URL, SHA, Short, At, Zip, Tgz string
	}
	key := rs.key()
	rows := make([]tagRow, 0, len(refs.Tags))
	for _, t := range refs.Tags {
		rows = append(rows, tagRow{
			Name: t.Name, SHA: t.SHA, Short: shortSHA(t.SHA), At: timeago(t.Date),
			URL: "/" + key + "/tree/" + t.Name,
			Zip: "/" + key + "/archive/refs/tags/" + t.Name + ".zip",
			Tgz: "/" + key + "/archive/refs/tags/" + t.Name + ".tar.gz",
		})
	}
	p := s.repoPage(r, rs, "Tags · "+key, "tags", "", "")
	s.render(w, r, http.StatusOK, "tags", map[string]any{"Page": p, "Rows": rows})
}

func (s *Server) handleBlame(w http.ResponseWriter, r *http.Request, rs *repoState) {
	ref, rev, pth, err := s.resolveRefPath(r, rs, r.PathValue("rest"))
	if err != nil || pth == "" {
		if err == nil {
			err = backend.ErrNotFound
		}
		s.fail(w, r, err)
		return
	}
	hunks, err := rs.repo.Blame(r.Context(), rev, pth)
	if rs.markCap(&rs.blameCap, err) {
		s.renderError(w, r, http.StatusNotFound, "Blame is not available on this backend.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	blob, err := rs.repo.Blob(r.Context(), rev, pth)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	lines := codeLines(path.Base(pth), string(blob.Content))

	// Age heat: rank distinct commit dates, oldest 10 to newest 1, the
	// scale github uses for its blame gutter bar.
	var oldest, newest time.Time
	for _, h := range hunks {
		d := h.Commit.Author.Date
		if oldest.IsZero() || d.Before(oldest) {
			oldest = d
		}
		if d.After(newest) {
			newest = d
		}
	}
	heat := func(d time.Time) int {
		span := newest.Sub(oldest)
		if span <= 0 {
			return 1
		}
		f := float64(newest.Sub(d)) / float64(span)
		return 1 + int(f*9+0.5)
	}

	key := rs.key()
	type blameView struct {
		SHA, Short, URL, Subject, Author, At string
		ReblameURL                           string
		Heat                                 int
		Lines                                []codeLine
	}
	views := make([]blameView, 0, len(hunks))
	for _, h := range hunks {
		v := blameView{
			SHA: h.Commit.SHA, Short: shortSHA(h.Commit.SHA),
			URL:     "/" + key + "/commit/" + h.Commit.SHA,
			Subject: h.Commit.Subject, Author: h.Commit.Author.Name,
			At:   timeago(h.Commit.Author.Date),
			Heat: heat(h.Commit.Author.Date),
		}
		if h.Prev != "" {
			v.ReblameURL = "/" + key + "/blame/" + h.Prev + "/" + pth
		}
		lo := h.StartLine - 1
		hi := lo + h.Lines
		if lo < 0 {
			lo = 0
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		if lo < hi {
			v.Lines = lines[lo:hi]
		}
		views = append(views, v)
	}

	p := s.repoPage(r, rs, "Blaming "+rs.info.Name+"/"+pth+" at "+ref+" · "+key, "code", ref, pth)
	s.render(w, r, http.StatusOK, "blame", map[string]any{
		"Page":    p,
		"Crumbs":  crumbs(key, "blame", ref, pth),
		"Name":    path.Base(pth),
		"BlobURL": "/" + key + "/blob/" + ref + "/" + pth,
		"RawURL":  "/" + key + "/raw/" + ref + "/" + pth,
		"Hunks":   views,
	})
}

func (s *Server) handleFind(w http.ResponseWriter, r *http.Request, rs *repoState) {
	rest := strings.Trim(r.PathValue("rest"), "/")
	refs, err := rs.cachedRefs(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ref, _ := splitRefPath(refs, rest)
	if ref == "" {
		ref = rs.info.DefaultBranch
	}
	// The tree-list endpoint takes a full SHA only, so the index a client
	// fetches is immutable and cacheable for a year.
	rev, err := rs.repo.Resolve(r.Context(), ref)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.repoPage(r, rs, "Find file · "+rs.key(), "code", ref, "")
	s.render(w, r, http.StatusOK, "find", map[string]any{
		"Page":        p,
		"TreeListURL": "/" + rs.key() + "/tree-list/" + rev,
		"BlobBase":    "/" + rs.key() + "/blob/" + ref + "/",
	})
}

var fullShaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (s *Server) handleTreeList(w http.ResponseWriter, r *http.Request, rs *repoState) {
	rev := r.PathValue("rev")
	if !fullShaRe.MatchString(rev) {
		http.NotFound(w, r)
		return
	}
	files, err := rs.repo.Files(r.Context(), rev)
	if rs.markCap(&rs.filesCap, err) || err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// The listing is keyed by a full SHA, so it can never change.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_ = json.NewEncoder(w).Encode(map[string]any{"paths": files})
}

// handleArchive serves /archive/refs/heads/main.zip style downloads, the
// exact URL shape github.com uses.
func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request, rs *repoState) {
	rest := r.PathValue("rest")
	format := ""
	switch {
	case strings.HasSuffix(rest, ".zip"):
		format, rest = "zip", strings.TrimSuffix(rest, ".zip")
	case strings.HasSuffix(rest, ".tar.gz"):
		format, rest = "tar.gz", strings.TrimSuffix(rest, ".tar.gz")
	default:
		http.NotFound(w, r)
		return
	}
	ref := rest
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/tags/")
	rev, err := rs.repo.Resolve(r.Context(), ref)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	stem := rs.info.Name + "-" + strings.ReplaceAll(strings.TrimPrefix(ref, "v"), "/", "-")
	h := w.Header()
	if format == "zip" {
		h.Set("Content-Type", "application/zip")
	} else {
		h.Set("Content-Type", "application/gzip")
	}
	h.Set("Content-Disposition", `attachment; filename="`+stem+`.`+format+`"`)
	if fullShaRe.MatchString(ref) {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	err = rs.repo.Archive(r.Context(), rev, format, stem+"/", w)
	if rs.markCap(&rs.archCap, err) {
		// Headers may already be gone; nothing more to do.
		return
	}
	if err != nil {
		s.logError(r, err)
	}
}
