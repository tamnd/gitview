package viewer

import (
	"bytes"
	"embed"
	"html/template"
)

// Structured viewers render through fragment templates living next to
// the code; a new kind.tmpl in templates/ is picked up automatically.
//
//go:embed templates/*.tmpl
var fragmentFS embed.FS

var fragments = template.Must(template.New("fragments").ParseFS(fragmentFS, "templates/*.tmpl"))

func renderFragment(name string, data any) (template.HTML, error) {
	var buf bytes.Buffer
	if err := fragments.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}
