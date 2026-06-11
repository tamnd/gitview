package viewer

import (
	"html/template"
)

// PDF embeds the browser's own viewer in an iframe over the raw URL.
// No magic check: a text file named x.pdf shows the viewer's broken
// document message, which is more honest than code-viewing it. The
// iframe carries no sandbox attribute on purpose: a sandboxed frame
// blocks Chromium's PDF plugin outright, and the raw response behind
// it already drops the CSP sandbox for application/pdf with the
// frame-ancestors 'self' analysis from spec 2011 doc 18 section 4.3.
func PDF() Renderer { return pdfRenderer{} }

type pdfRenderer struct{}

func (pdfRenderer) Kind() string { return "pdf" }

func (pdfRenderer) Match(in Input) bool { return in.Ext == ".pdf" }

func (pdfRenderer) Render(in Input) (Output, error) {
	body := `<div class="blob-frame" data-kind="pdf"><iframe src="` +
		template.HTMLEscapeString(in.RawURL) + `" title="` +
		template.HTMLEscapeString(in.Name) + `"></iframe></div>`
	return Output{Kind: "pdf", Body: template.HTML(body)}, nil
}
