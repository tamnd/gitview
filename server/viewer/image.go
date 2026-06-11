package viewer

import (
	"html/template"
	"strings"
)

// Image renders raster and vector images through an <img> over the raw
// URL. SVG stays behind <img>, where scripts never execute; the raw
// response behind it keeps its sandbox CSP for direct navigation.
func Image() Renderer { return imageRenderer{} }

type imageRenderer struct{}

func (imageRenderer) Kind() string { return "image" }

func (imageRenderer) Match(in Input) bool {
	mt, ok := MIME(in.Ext)
	return ok && strings.HasPrefix(mt, "image/")
}

func (imageRenderer) Render(in Input) (Output, error) {
	body := `<div class="blob-notice" data-kind="image"><img class="blob-image" src="` +
		template.HTMLEscapeString(in.RawURL) + `" alt="` +
		template.HTMLEscapeString(in.Name) + `"></div>`
	return Output{Kind: "image", Body: template.HTML(body)}, nil
}
