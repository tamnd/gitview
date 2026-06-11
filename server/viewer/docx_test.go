package viewer

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildDocx assembles a minimal docx in memory: no binary fixtures in
// the repo, per the testing rules of spec 2011 doc 18.
func buildDocx(t testing.TB, documentXML string, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, content string) {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if documentXML != "" {
		add("word/document.xml", documentXML)
	}
	for name, content := range extra {
		add(name, content)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func docBody(inner string) string {
	return `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>` + inner + `</w:body></w:document>`
}

func renderDocx(t *testing.T, content []byte) Output {
	t.Helper()
	r := Docx()
	in := Input{Name: "x.docx", Ext: ".docx", Content: content, Binary: true}
	if !r.Match(in) {
		t.Fatal("fixture did not match")
	}
	out, err := r.Render(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

func TestDocxHeadingsAndRuns(t *testing.T) {
	content := buildDocx(t, docBody(
		`<w:p><w:pPr><w:pStyle w:val="Title"/></w:pPr><w:r><w:t>The Title</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>Section</w:t></w:r></w:p>`+
			`<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>bold</w:t></w:r><w:r><w:rPr><w:i/></w:rPr><w:t xml:space="preserve"> italic </w:t></w:r><w:r><w:rPr><w:b w:val="0"/></w:rPr><w:t>plain</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>a</w:t><w:tab/><w:t>b</w:t><w:br/><w:t>c</w:t></w:r></w:p>`,
	), nil)
	out := renderDocx(t, content)
	body := string(out.Body)
	for _, w := range []string{
		"<h1>The Title</h1>", "<h2>Section</h2>",
		"<strong>bold</strong>", "<em> italic </em>", ">plain</",
		"a\tb<br>c",
		`class="markdown-body docx-body"`,
	} {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q in %s", w, body)
		}
	}
	if out.Info != "9 words" {
		t.Errorf("info = %q", out.Info)
	}
	if out.Toggle {
		t.Error("docx must not offer a code toggle")
	}
}

func TestDocxListsAndTable(t *testing.T) {
	content := buildDocx(t, docBody(
		`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>one</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:numPr><w:ilvl w:val="2"/></w:numPr></w:pPr><w:r><w:t>deep</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:numPr><w:ilvl w:val="99"/></w:numPr></w:pPr><w:r><w:t>capped</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>after</w:t></w:r></w:p>`+
			`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>cell</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`,
	), nil)
	body := string(renderDocx(t, content).Body)
	for _, w := range []string{
		`<ul><li class="docx-ilvl-0">one</li>`,
		`<li class="docx-ilvl-2">deep</li>`,
		`<li class="docx-ilvl-8">capped</li></ul>`,
		"<p>after</p>",
		"<table><tr><td><p>cell</p></td></tr></table>",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q in %s", w, body)
		}
	}
}

func TestDocxHyperlinks(t *testing.T) {
	rels := `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.com/x" TargetMode="External"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="javascript:alert(1)" TargetMode="External"/>` +
		`</Relationships>`
	content := buildDocx(t, docBody(
		`<w:p><w:hyperlink r:id="rId1"><w:r><w:t>good</w:t></w:r></w:hyperlink></w:p>`+
			`<w:p><w:hyperlink r:id="rId2"><w:r><w:t>bad</w:t></w:r></w:hyperlink></w:p>`+
			`<w:p><w:hyperlink w:anchor="local"><w:r><w:t>anchor</w:t></w:r></w:hyperlink></w:p>`,
	), map[string]string{"word/_rels/document.xml.rels": rels})
	body := string(renderDocx(t, content).Body)
	if !strings.Contains(body, `<a href="https://example.com/x" rel="noopener nofollow ugc">good</a>`) {
		t.Errorf("external link missing: %s", body)
	}
	if strings.Contains(body, "javascript:") {
		t.Errorf("javascript target survived: %s", body)
	}
	if !strings.Contains(body, "<p>bad</p>") || !strings.Contains(body, "<p>anchor</p>") {
		t.Errorf("dropped-link text lost: %s", body)
	}
}

func TestDocxImagePlaceholderAndEscaping(t *testing.T) {
	content := buildDocx(t, docBody(
		`<w:p><w:r><w:drawing><wp:inline><a:graphic/></wp:inline></w:drawing></w:r><w:r><w:t>&lt;script&gt;alert(1)&lt;/script&gt;</w:t></w:r></w:p>`,
	), nil)
	body := string(renderDocx(t, content).Body)
	if !strings.Contains(body, "[image]") {
		t.Errorf("image placeholder missing: %s", body)
	}
	if strings.Contains(body, "<script>") {
		t.Errorf("script not escaped: %s", body)
	}
}

func TestDocxTruncation(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteString(`<w:p><w:r><w:t>` + strings.Repeat("word ", 20) + `</w:t></w:r></w:p>`)
	}
	out := renderDocx(t, buildDocx(t, docBody(b.String()), nil))
	if out.Note != "Document truncated. Download the raw file for the rest." {
		t.Errorf("note = %q", out.Note)
	}
	if !strings.HasSuffix(out.Info, "words+") {
		t.Errorf("info = %q", out.Info)
	}
}

func TestDocxNonMatches(t *testing.T) {
	r := Docx()
	if r.Match(Input{Ext: ".docx", Content: []byte("not a zip")}) {
		t.Error("matched non-zip")
	}
	// A zip without word/document.xml (an epub, say) is not a docx.
	other := buildDocx(t, "", map[string]string{"mimetype": "application/epub+zip"})
	if r.Match(Input{Ext: ".docx", Content: other}) {
		t.Error("matched zip without document.xml")
	}
	if r.Match(Input{Ext: ".doc", Content: []byte("PK\x03\x04")}) {
		t.Error("matched .doc")
	}
}

func FuzzDocx(f *testing.F) {
	f.Add(buildDocx(f, docBody(`<w:p><w:r><w:t>hello</w:t></w:r></w:p>`), nil))
	f.Add(buildDocx(f, `<w:document><w:body><w:p>`, nil))
	f.Add([]byte("PK\x03\x04 not really"))
	f.Fuzz(func(t *testing.T, content []byte) {
		r := Docx()
		in := Input{Name: "f.docx", Ext: ".docx", Content: content, Binary: true}
		if !r.Match(in) {
			return
		}
		out, err := r.Render(in)
		if err != nil {
			return
		}
		body := string(out.Body)
		if !strings.HasPrefix(body, `<article class="markdown-body docx-body"`) {
			t.Errorf("unexpected wrapper: %q", body)
		}
	})
}
