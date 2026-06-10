package server

import (
	"bytes"
	"fmt"
	"html/template"
	"path"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/tamnd/gitview/backend"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// mdContext tells the rewriter where the markdown file lives so relative
// links and images resolve to blob and raw URLs.
type mdContext struct {
	RepoPath string // "/owner/name"
	Ref      string
	Dir      string // directory of the source file, "" at the root
}

var mdCtxKey = parser.NewContextKey()

// renderMarkdown runs the pipeline: goldmark with GFM and our transformers,
// then bluemonday, then trusted post-passes that add heading anchors and
// alert icons. Sanitizing after rendering is the security boundary; the
// post-passes only touch markup the sanitizer already approved.
func renderMarkdown(mctx mdContext, src []byte) template.HTML {
	var buf bytes.Buffer
	pc := parser.NewContext()
	pc.Set(mdCtxKey, mctx)
	if err := mdRenderer.Convert(src, &buf, parser.WithContext(pc)); err != nil {
		return template.HTML("<p>markdown rendering failed</p>")
	}
	clean := mdPolicy.SanitizeBytes(buf.Bytes())
	clean = addHeadingAnchors(clean)
	clean = addAlertIcons(clean)
	return template.HTML(clean)
}

var mdRenderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Footnote),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
		parser.WithASTTransformers(
			util.Prioritized(&linkRewriter{}, 100),
			util.Prioritized(&alertTransformer{}, 200),
		),
	),
	goldmark.WithRendererOptions(
		gmhtml.WithUnsafe(),
		renderer.WithNodeRenderers(
			util.Prioritized(&alertRenderer{}, 100),
			util.Prioritized(&fenceRenderer{}, 100),
		),
	),
)

var mdPolicy = buildPolicy()

func buildPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// class is needed for chroma spans, alerts, and task lists. Hostile
	// markdown can borrow our cosmetic classes; CSP and readonly pages
	// keep that harmless.
	p.AllowAttrs("class").OnElements(
		"a", "blockquote", "code", "div", "h1", "h2", "h3", "h4", "h5", "h6",
		"input", "li", "ol", "p", "pre", "span", "table", "td", "th", "ul")
	p.AllowAttrs("id").OnElements("h1", "h2", "h3", "h4", "h5", "h6", "li", "a", "p", "div", "section")
	p.AllowElements("input", "details", "summary", "section")
	p.AllowAttrs("type", "disabled", "checked").OnElements("input")
	p.AllowAttrs("open").OnElements("details")
	p.AllowAttrs("align").OnElements("p", "div", "td", "th", "img")
	p.AllowAttrs("width", "height").OnElements("img", "td", "th")
	p.AllowAttrs("start").OnElements("ol")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoFollowOnLinks(false)
	return p
}

// linkRewriter resolves relative destinations against the source file.
type linkRewriter struct{}

func (t *linkRewriter) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	v, ok := pc.Get(mdCtxKey).(mdContext)
	if !ok {
		return
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Link:
			node.Destination = rewriteURL(v, node.Destination, "blob")
		case *ast.Image:
			node.Destination = rewriteURL(v, node.Destination, "raw")
		}
		return ast.WalkContinue, nil
	})
}

func rewriteURL(v mdContext, dest []byte, kind string) []byte {
	d := string(dest)
	if d == "" || strings.HasPrefix(d, "#") || strings.Contains(d, "://") ||
		strings.HasPrefix(d, "mailto:") || strings.HasPrefix(d, "data:") ||
		strings.HasPrefix(d, "//") {
		return dest
	}
	base := v.Dir
	if strings.HasPrefix(d, "/") {
		// Root-relative links resolve against the repository root: the
		// useful reading for a local tool, and our one stated divergence
		// from github.com, which treats them as site-relative.
		base = ""
		d = strings.TrimPrefix(d, "/")
	}
	frag := ""
	if i := strings.IndexAny(d, "#?"); i >= 0 {
		d, frag = d[:i], d[i:]
	}
	joined := path.Clean(path.Join(base, d))
	if joined == "." || strings.HasPrefix(joined, "..") {
		joined = ""
	}
	return []byte(v.RepoPath + "/" + kind + "/" + v.Ref + "/" + joined + frag)
}

// alertTransformer recognizes GitHub alert blockquotes: a blockquote whose
// first paragraph starts with [!NOTE] and friends on its own line.
type alertTransformer struct{}

var alertKinds = map[string]string{
	"[!NOTE]": "note", "[!TIP]": "tip", "[!IMPORTANT]": "important",
	"[!WARNING]": "warning", "[!CAUTION]": "caution",
}

func (t *alertTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	src := reader.Source()
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Kind() != ast.KindBlockquote {
			return ast.WalkContinue, nil
		}
		para, ok := n.FirstChild().(*ast.Paragraph)
		if !ok || para.Lines().Len() == 0 {
			return ast.WalkContinue, nil
		}
		// The marker must occupy the whole first line. By transform time
		// the brackets are split across inline nodes, so read the raw
		// line and then drop every inline node that lies inside it.
		firstLine := para.Lines().At(0)
		marker := strings.TrimSpace(string(firstLine.Value(src)))
		kind, ok := alertKinds[marker]
		if !ok {
			return ast.WalkContinue, nil
		}
		n.SetAttributeString("alert", []byte(kind))
		for c := para.FirstChild(); c != nil; {
			next := c.NextSibling()
			t, isText := c.(*ast.Text)
			if !isText || t.Segment.Start >= firstLine.Stop {
				break
			}
			para.RemoveChild(para, c)
			c = next
		}
		// A bare marker leaves an empty first paragraph behind.
		if para.ChildCount() == 0 {
			n.RemoveChild(n, para)
		}
		return ast.WalkSkipChildren, nil
	})
}

// alertRenderer renders alert blockquotes as the markdown-alert div github
// emits; plain blockquotes fall through to the default renderer.
type alertRenderer struct{}

func (r *alertRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindBlockquote, r.render)
}

func (r *alertRenderer) render(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	kind, ok := n.AttributeString("alert")
	if !ok {
		if entering {
			_, _ = w.WriteString("<blockquote>\n")
		} else {
			_, _ = w.WriteString("</blockquote>\n")
		}
		return ast.WalkContinue, nil
	}
	k := string(kind.([]byte))
	if entering {
		_, _ = fmt.Fprintf(w, `<div class="markdown-alert markdown-alert-%s"><p class="markdown-alert-title">%s</p>`+"\n",
			k, strings.ToUpper(k[:1])+k[1:])
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}

// fenceRenderer highlights fenced code blocks with the same chroma class
// pipeline as blob pages, one configuration for the whole app.
type fenceRenderer struct{}

func (r *fenceRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.render)
}

func (r *fenceRenderer) render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	lang := string(n.Language(source))
	var content bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		content.Write(line.Value(source))
	}
	_, _ = w.WriteString(`<pre class="chroma"><code>`)
	filename := "x." + lang
	if lang == "" {
		filename = "x.txt"
	}
	for i, l := range highlightLines(filename, content.String()) {
		if i > 0 {
			_, _ = w.WriteString("\n")
		}
		_, _ = w.WriteString(string(l))
	}
	_, _ = w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

var headingRe = regexp.MustCompile(`<(h[1-6]) id="([^"]+)">`)

// addHeadingAnchors injects the hover anchor link github puts on headings.
// It runs after sanitization on markup we just approved.
func addHeadingAnchors(b []byte) []byte {
	return headingRe.ReplaceAll(b,
		[]byte(`<$1 id="$2"><a class="anchor" href="#$2" aria-hidden="true">`+string(octicons["link"])+`</a>`))
}

var alertTitleRe = regexp.MustCompile(`<p class="markdown-alert-title">(Note|Tip|Important|Warning|Caution)</p>`)

var alertIcons = map[string]string{
	"Note": "info", "Tip": "light-bulb", "Important": "report",
	"Warning": "alert", "Caution": "stop",
}

func addAlertIcons(b []byte) []byte {
	return alertTitleRe.ReplaceAllFunc(b, func(m []byte) []byte {
		title := string(alertTitleRe.FindSubmatch(m)[1])
		return []byte(`<p class="markdown-alert-title">` + string(octicons[alertIcons[title]]) + title + `</p>`)
	})
}

// isMarkdownFile reports whether the blob should get a rendered preview.
func isMarkdownFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkdn":
		return true
	}
	return false
}

// findReadme picks the readme github would show for a directory listing.
func findReadme(entries []backend.TreeEntry) (string, bool) {
	preference := []string{"readme.md", "readme.markdown", "readme.txt", "readme"}
	byLower := map[string]string{}
	for _, e := range entries {
		if e.Kind == backend.KindFile {
			byLower[strings.ToLower(e.Name)] = e.Name
		}
	}
	for _, want := range preference {
		if name, ok := byLower[want]; ok {
			return name, true
		}
	}
	return "", false
}
