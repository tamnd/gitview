package server

import (
	"errors"
	"html/template"
	"net/http"
	"path"
	"strings"

	"github.com/tamnd/gitview/backend"
)

// page carries everything the layout shell needs; handlers wrap it in a map
// together with page-specific data.
type page struct {
	Title   string
	Version string
	Dev     bool
	Repo    *repoHeader // nil outside repository pages
}

// repoHeader feeds the repo nav bar and the branch picker.
type repoHeader struct {
	Key           string
	Owner         string
	Name          string
	Description   string
	DefaultBranch string
	Nav           string // active tab: code, commits, branches, tags
	Ref           string // current ref, "" before resolution
	Path          string
	Branches      []backend.Ref
	Tags          []backend.Ref
	FindURL       string
}

func (s *Server) basePage(rs *repoState, title string) page {
	p := page{Title: title, Version: s.opts.Version, Dev: s.opts.Dev}
	if rs != nil {
		p.Repo = &repoHeader{
			Key:           rs.key(),
			Owner:         rs.info.Owner,
			Name:          rs.info.Name,
			Description:   rs.info.Description,
			DefaultBranch: rs.info.DefaultBranch,
			Nav:           "code",
		}
	}
	return p
}

// repoPage builds the base page plus refs for the branch picker.
func (s *Server) repoPage(r *http.Request, rs *repoState, title, nav, ref, pth string) page {
	p := s.basePage(rs, title)
	p.Repo.Nav = nav
	p.Repo.Ref = ref
	p.Repo.Path = pth
	p.Repo.FindURL = "/" + rs.key() + "/find/" + ref
	if refs, err := rs.cachedRefs(r.Context()); err == nil {
		p.Repo.Branches = refs.Branches
		p.Repo.Tags = refs.Tags
	}
	return p
}

// crumb is one segment of the breadcrumb path line.
type crumb struct {
	Name string
	URL  string // "" for the final segment
}

func crumbs(repoKey, kind, ref, pth string) []crumb {
	if pth == "" {
		return nil
	}
	parts := strings.Split(pth, "/")
	out := make([]crumb, 0, len(parts))
	for i, part := range parts {
		c := crumb{Name: part}
		if i < len(parts)-1 {
			c.URL = "/" + repoKey + "/tree/" + ref + "/" + strings.Join(parts[:i+1], "/")
		} else if kind != "" {
			c.URL = ""
		}
		out = append(out, c)
	}
	return out
}

// treeRow is one line of the file table.
type treeRow struct {
	Name    string
	URL     string
	Icon    string
	IsDir   bool
	LastMsg string
	LastURL string
	LastAt  string // pre-rendered timeago, "" when unknown
}

func entryIcon(k backend.EntryKind) string {
	switch k {
	case backend.KindDir:
		return "file-directory-fill"
	case backend.KindSymlink:
		return "file-symlink-file"
	case backend.KindSubmodule:
		return "file-submodule"
	default:
		return "file"
	}
}

// blobKind classifies a blob for the viewer.
type blobKind int

const (
	blobCode blobKind = iota
	blobMarkdown
	blobImage
	blobBinary
	blobLFS
	blobTooBig
	blobEmpty
)

var imageExts = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".ico": "image/x-icon",
	".bmp": "image/bmp", ".avif": "image/avif", ".svg": "image/svg+xml",
}

func classifyBlob(name string, b backend.Blob, plain bool) blobKind {
	switch {
	case b.LFS != nil:
		return blobLFS
	case b.TooBig:
		return blobTooBig
	}
	if _, ok := imageExts[strings.ToLower(path.Ext(name))]; ok {
		return blobImage
	}
	switch {
	case b.Binary:
		return blobBinary
	case len(b.Content) == 0:
		return blobEmpty
	case isMarkdownFile(name) && !plain:
		return blobMarkdown
	}
	return blobCode
}

// codeLine pairs a line number with its highlighted fragment.
type codeLine struct {
	Num  int
	HTML template.HTML
}

func codeLines(filename, content string) []codeLine {
	frags := highlightLines(filename, content)
	out := make([]codeLine, len(frags))
	for i, f := range frags {
		out[i] = codeLine{Num: i + 1, HTML: f}
	}
	return out
}

// renderError shows the styled error page. The 404 message matches the
// github.com text users know.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.render(w, r, status, "error", map[string]any{
		"Page":    s.basePage(nil, http.StatusText(status)),
		"Status":  status,
		"Message": msg,
	})
}

// fail maps backend errors onto HTTP responses.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var rl *backend.RateLimitError
	switch {
	case errors.As(err, &rl):
		msg := "The backend API rate limit is exhausted. Try again shortly, or pass a token."
		if !rl.Reset.IsZero() {
			msg = "The backend API rate limit is exhausted. It resets at " +
				rl.Reset.UTC().Format("15:04 MST") + ". A token raises the limit."
		}
		s.renderError(w, r, http.StatusServiceUnavailable, msg)
	case errors.Is(err, backend.ErrNotFound):
		s.renderError(w, r, http.StatusNotFound, "This is not the web page you are looking for.")
	case errors.Is(err, backend.ErrUnsupported):
		s.renderError(w, r, http.StatusNotFound, "This backend does not support that view.")
	default:
		s.logError(r, err)
		msg := "Something went wrong."
		if s.opts.Dev {
			msg = err.Error()
		}
		s.renderError(w, r, http.StatusInternalServerError, msg)
	}
}
