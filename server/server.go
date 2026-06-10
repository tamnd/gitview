package server

import (
	"context"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/tamnd/gitview/backend"
)

//go:embed templates static
var assets embed.FS

// Options configures a Server.
type Options struct {
	// Dev enables verbose error pages.
	Dev bool
	// Version is shown in the footer and used for asset cache busting.
	Version string
}

// Server renders the gitview UI for one or more repositories.
type Server struct {
	mux    *http.ServeMux
	repos  map[string]*repoState // key "owner/name"
	order  []string              // registration order for the index page
	single *repoState            // set in single-repo mode
	opts   Options
	tpl    *templates
}

// New builds a server for the given repositories. With exactly one
// repository, / redirects straight to it; with several, / lists them.
func New(ctx context.Context, repos []backend.Repo, opts Options) (*Server, error) {
	if len(repos) == 0 {
		return nil, fmt.Errorf("no repositories")
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	tpl, err := loadTemplates(opts)
	if err != nil {
		return nil, err
	}
	s := &Server{
		mux:   http.NewServeMux(),
		repos: make(map[string]*repoState),
		opts:  opts,
		tpl:   tpl,
	}
	for _, repo := range repos {
		rs, err := newRepoState(ctx, repo)
		if err != nil {
			return nil, err
		}
		key := rs.key()
		if _, dup := s.repos[key]; dup {
			return nil, fmt.Errorf("duplicate repository %s", key)
		}
		s.repos[key] = rs
		s.order = append(s.order, key)
	}
	sort.Strings(s.order)
	if len(repos) == 1 {
		s.single = s.repos[s.order[0]]
	}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// routes registers literal per-repository patterns. Owners are known at
// startup and the keys are sanitized at repoState construction, so nothing
// wildcards the first path segment and /static can never collide with a
// repository route.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.Handle("GET /static/", s.staticHandler())

	for key, rs := range s.repos {
		p := "/" + key
		h := func(fn func(http.ResponseWriter, *http.Request, *repoState)) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { fn(w, r, rs) }
		}
		s.mux.HandleFunc("GET "+p+"/{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, p, http.StatusMovedPermanently)
		})
		s.mux.HandleFunc("GET "+p, h(s.handleHome))
		s.mux.HandleFunc("GET "+p+"/tree/{rest...}", h(s.handleTree))
		s.mux.HandleFunc("GET "+p+"/blob/{rest...}", h(s.handleBlob))
		s.mux.HandleFunc("GET "+p+"/raw/{rest...}", h(s.handleRaw))
		s.mux.HandleFunc("GET "+p+"/commits", h(s.handleCommits))
		s.mux.HandleFunc("GET "+p+"/commits/{rest...}", h(s.handleCommits))
		s.mux.HandleFunc("GET "+p+"/commit/{sha}", h(s.handleCommit))
		s.mux.HandleFunc("GET "+p+"/branches", h(s.handleBranches))
		s.mux.HandleFunc("GET "+p+"/tags", h(s.handleTags))
		s.mux.HandleFunc("GET "+p+"/blame/{rest...}", h(s.handleBlame))
		s.mux.HandleFunc("GET "+p+"/find/{rest...}", h(s.handleFind))
		s.mux.HandleFunc("GET "+p+"/tree-list/{rev}", h(s.handleTreeList))
		s.mux.HandleFunc("GET "+p+"/archive/{rest...}", h(s.handleArchive))
	}

	// Everything else, including the github.com surfaces gitview
	// deliberately does not implement, gets the styled 404.
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, r, http.StatusNotFound, "This is not the web page you are looking for.")
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if s.single != nil {
		http.Redirect(w, r, "/"+s.single.key(), http.StatusFound)
		return
	}
	type row struct {
		Key, Owner, Name, Desc, Branch string
	}
	var rows []row
	for _, key := range s.order {
		rs := s.repos[key]
		rows = append(rows, row{
			Key: key, Owner: rs.info.Owner, Name: rs.info.Name,
			Desc: rs.info.Description, Branch: rs.info.DefaultBranch,
		})
	}
	s.render(w, r, http.StatusOK, "index", map[string]any{
		"Page":  s.basePage(nil, "Repositories"),
		"Repos": rows,
	})
}

func (s *Server) staticHandler() http.Handler {
	fs := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/static/chroma.css" {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			_, _ = io.WriteString(w, s.tpl.chromaCSS)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		fs.ServeHTTP(w, r)
	})
}

// resolveRefPath splits the {rest...} tail of a URL into (ref, path) using
// the longest matching ref name, then resolves the ref. Branch names with
// slashes win over shorter matches; branches beat tags at the same length;
// a leading hex segment falls back to a commit id, all per github.com.
func (s *Server) resolveRefPath(r *http.Request, rs *repoState, rest string) (ref, rev, path string, err error) {
	refs, err := rs.cachedRefs(r.Context())
	if err != nil {
		return "", "", "", err
	}
	ref, path = splitRefPath(refs, rest)
	if ref == "" {
		return "", "", "", fmt.Errorf("no ref in %q: %w", rest, backend.ErrNotFound)
	}
	rev, err = rs.repo.Resolve(r.Context(), ref)
	if err != nil {
		return "", "", "", err
	}
	return ref, rev, path, nil
}

func splitRefPath(refs backend.Refs, rest string) (ref, path string) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", ""
	}
	best := ""
	isBranch := false
	match := func(name string, branch bool) {
		if name == rest || strings.HasPrefix(rest, name+"/") {
			if len(name) > len(best) || (len(name) == len(best) && branch && !isBranch) {
				best, isBranch = name, branch
			}
		}
	}
	for _, b := range refs.Branches {
		match(b.Name, true)
	}
	for _, t := range refs.Tags {
		match(t.Name, false)
	}
	if best != "" {
		return best, strings.Trim(strings.TrimPrefix(rest, best), "/")
	}
	// Fall back to the first segment: a SHA or short hex id.
	seg, rem, _ := strings.Cut(rest, "/")
	return seg, rem
}

func (s *Server) logError(r *http.Request, err error) {
	slog.Error("request failed", "path", r.URL.Path, "err", err)
}
