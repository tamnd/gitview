// Package viewer renders blob content for the file viewer page. Each file
// type is one Renderer behind a small interface; the server asks the
// Registry and never learns what file types exist. Structural blob states
// (symlink, LFS pointer, too big, empty) stay in the server: they are
// states of the record, not content (spec 2011 doc 18).
package viewer

import "html/template"

// Input is everything a renderer may look at. Content is the full blob,
// already capped at backend.MaxBlobBytes by the backend; TooBig blobs
// never reach the registry.
type Input struct {
	Name     string // base name, "model.parquet"
	Path     string // repo-relative path, "data/model.parquet"
	Ext      string // lowercased extension with dot, ".parquet"
	Content  []byte
	Size     int64  // true blob size, equals len(Content) here
	Binary   bool   // backend's NUL-byte verdict
	RawURL   string // "/{key}/raw/{ref}/{path}", for media and frame sources
	RepoPath string // "/{owner}/{repo}", for markdown link rewriting
	Ref      string // resolved display ref, same purpose
	Plain    bool   // ?plain=1: the user asked for source, not preview
}

// Output is a rendered fragment plus toolbar facts.
type Output struct {
	Kind   string        // stable id: "code", "csv", "video", ...
	Body   template.HTML // the content area, safe by construction
	Info   string        // toolbar text: "183 lines", "100 rows x 12 columns"
	Toggle bool          // offer the Preview / Code segmented control
	Note   string        // cap warning shown above the body, "" for none
}

// Renderer is one viewer. Match must be cheap (extension checks, magic
// bytes); Render may parse. A Renderer that matched but cannot render
// returns an error and the registry falls through to the next match.
type Renderer interface {
	Kind() string
	Match(in Input) bool
	Render(in Input) (Output, error)
}

// Registry is an ordered list of renderers; the first Match wins.
type Registry struct {
	renderers []Renderer
}

func NewRegistry(rs ...Renderer) *Registry {
	return &Registry{renderers: rs}
}

// Render walks renderers in order. A renderer error is a degradation,
// never a failure: the walk continues, so a corrupt container falls
// through to whatever would have claimed the file otherwise. ok reports
// whether anything rendered; the server shows the structural binary or
// empty notice when nothing did.
func (g *Registry) Render(in Input) (Output, bool) {
	for _, r := range g.renderers {
		if !r.Match(in) {
			continue
		}
		out, err := r.Render(in)
		if err != nil {
			continue
		}
		return out, true
	}
	return Output{}, false
}

// Probe reports the kind that would claim the input, without rendering.
// The server probes with Plain unset to keep the Preview / Code toggle
// visible while the plain view is showing.
func (g *Registry) Probe(in Input) (string, bool) {
	for _, r := range g.renderers {
		if r.Match(in) {
			return r.Kind(), true
		}
	}
	return "", false
}
