package server

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/tamnd/gitview/server/viewer"
)

// themeBootstrap runs before first paint so a stored dark preference never
// flashes light. Its hash is allow-listed in the CSP header.
const themeBootstrap = `(function(){try{var m=localStorage.getItem("gitview-color-mode");if(m==="light"||m==="dark")document.documentElement.setAttribute("data-color-mode",m);}catch(e){}})();`

// templates holds the parsed page templates plus assets computed at startup.
type templates struct {
	pages     map[string]*template.Template
	chromaCSS string
	csp       string
	version   string
}

func loadTemplates(opts Options) (*templates, error) {
	hash := sha256.Sum256([]byte(themeBootstrap))
	t := &templates{
		pages:     make(map[string]*template.Template),
		chromaCSS: viewer.ChromaCSS(),
		csp: "default-src 'none'; img-src 'self' data: https:; style-src 'self'; " +
			"script-src 'self' 'sha256-" + base64.StdEncoding.EncodeToString(hash[:]) + "'; " +
			"connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'",
		version: opts.Version,
	}

	base := template.New("layout").Funcs(templateFuncs())
	base, err := base.ParseFS(assets, "templates/layout.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	files, err := fs.Glob(assets, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		name := strings.TrimSuffix(path.Base(f), ".tmpl")
		if name == "layout" {
			continue
		}
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.ParseFS(assets, f); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		t.pages[name] = clone
	}
	return t, nil
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"octicon":   octicon,
		"timeago":   timeago,
		"bytesize":  bytesize,
		"short":     shortSHA,
		"datelong":  func(t time.Time) string { return t.Format("Jan 2, 2006") },
		"datefull":  func(t time.Time) string { return t.Format("Jan 2, 2006, 3:04 PM MST") },
		"bootstrap": func() template.JS { return template.JS(themeBootstrap) },
		"add":       func(a, b int) int { return a + b },
	}
}

func octicon(name string) template.HTML {
	return viewer.Octicon(name)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// timeago formats relative time the way github.com does, switching to an
// absolute date after a month.
func timeago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < 2*time.Minute:
		return "1 minute ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 2*time.Hour:
		return "1 hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case t.Year() == time.Now().Year():
		return "on " + t.Format("Jan 2")
	default:
		return "on " + t.Format("Jan 2, 2006")
	}
}

func bytesize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d Bytes", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}

// render writes a page through the named template.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	tpl, ok := s.tpl.pages[name]
	if !ok {
		s.logError(r, fmt.Errorf("no template %q", name))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var buf strings.Builder
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.logError(r, fmt.Errorf("render %s: %w", name, err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", s.tpl.csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}
