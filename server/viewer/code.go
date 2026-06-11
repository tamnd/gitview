package viewer

import (
	"fmt"
	"html/template"
	"regexp"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Highlighting caps, matching github.com behavior of falling back to plain
// text on very large files.
const (
	maxHighlightBytes = 512 << 10
	maxHighlightLines = 20000
)

// HighlightLines tokenizes content and returns one HTML fragment per line,
// using chroma class names so the github and github-dark styles switch with
// the page theme instead of re-rendering.
func HighlightLines(filename, content string) []template.HTML {
	lines := splitLines(content)
	if len(content) > maxHighlightBytes || len(lines) > maxHighlightLines {
		return escapeLines(lines)
	}
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		return escapeLines(lines)
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, content)
	if err != nil {
		return escapeLines(lines)
	}

	var out []template.HTML
	var b strings.Builder
	flush := func() {
		out = append(out, template.HTML(b.String()))
		b.Reset()
	}
	for tok := it(); tok != chroma.EOF; tok = it() {
		class := tokenClass(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, part := range parts {
			if i > 0 {
				flush()
			}
			if part == "" {
				continue
			}
			if class == "" {
				b.WriteString(template.HTMLEscapeString(part))
			} else {
				b.WriteString(`<span class="` + class + `">`)
				b.WriteString(template.HTMLEscapeString(part))
				b.WriteString(`</span>`)
			}
		}
	}
	if b.Len() > 0 {
		flush()
	}
	for len(out) < len(lines) {
		out = append(out, "")
	}
	return out[:len(lines)]
}

// tokenClass mirrors chroma's html formatter lookup: exact type, then
// subcategory, then category.
func tokenClass(t chroma.TokenType) string {
	for _, tt := range []chroma.TokenType{t, t.SubCategory(), t.Category()} {
		if c, ok := chroma.StandardTypes[tt]; ok && c != "" {
			return c
		}
	}
	return ""
}

// splitLines drops the trailing newline so a final "\n" does not render a
// phantom empty line, the way github.com counts lines.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	content = strings.TrimSuffix(content, "\n")
	return strings.Split(content, "\n")
}

func escapeLines(lines []string) []template.HTML {
	out := make([]template.HTML, len(lines))
	for i, l := range lines {
		out[i] = template.HTML(template.HTMLEscapeString(l))
	}
	return out
}

// ChromaCSS renders the github light style plus the github-dark
// style scoped to the dark color mode, with an auto-mode media query copy.
func ChromaCSS() string {
	light := styleCSS("github")
	dark := styleCSS("github-dark")

	var b strings.Builder
	b.WriteString(light)
	b.WriteString("\n")
	b.WriteString(scopeCSS(dark, `html[data-color-mode="dark"] `))
	b.WriteString("\n@media (prefers-color-scheme: dark) {\n")
	b.WriteString(scopeCSS(dark, `html[data-color-mode="auto"] `))
	b.WriteString("}\n")
	return b.String()
}

// cssSelLine matches one chroma CSS line: an optional leading comment,
// then the selector starting at the first class name.
var cssSelLine = regexp.MustCompile(`^(\s*(?:/\*.*?\*/\s*)?)(\..*)$`)

func styleCSS(name string) string {
	style := styles.Get(name)
	f := html.New(html.WithClasses(true))
	var b strings.Builder
	if err := f.WriteCSS(&b, style); err != nil {
		return ""
	}
	// Drop the body-level background rule chroma emits for .bg; our design
	// tokens own page backgrounds.
	var keep []string
	for _, line := range strings.Split(b.String(), "\n") {
		if m := cssSelLine.FindStringSubmatch(line); m != nil && strings.HasPrefix(m[2], ".bg") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

func scopeCSS(css, prefix string) string {
	var b strings.Builder
	for _, line := range strings.Split(css, "\n") {
		if m := cssSelLine.FindStringSubmatch(line); m != nil {
			b.WriteString(m[1])
			b.WriteString(prefix)
			b.WriteString(m[2])
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Code is the final text fallback: the chroma table with line ids that
// back the L{n} anchor contract (doc 08).
func Code() Renderer { return codeRenderer{} }

type codeRenderer struct{}

func (codeRenderer) Kind() string { return "code" }

func (codeRenderer) Match(in Input) bool { return !in.Binary }

func (codeRenderer) Render(in Input) (Output, error) {
	frags := HighlightLines(in.Name, string(in.Content))
	var b strings.Builder
	b.WriteString(`<table class="code-table js-code" id="blob" data-kind="code"><tbody>`)
	for i, f := range frags {
		n := strconv.Itoa(i + 1)
		b.WriteString(`<tr class="code-row" id="L` + n + `"><td class="code-num" data-line-number="` + n + `"></td><td class="code-cell">`)
		b.WriteString(string(f))
		b.WriteString("</td></tr>\n")
	}
	b.WriteString(`</tbody></table>`)
	info := fmt.Sprintf("%d lines", len(frags))
	if len(frags) == 1 {
		info = "1 line"
	}
	return Output{Kind: "code", Body: template.HTML(b.String()), Info: info}, nil
}
