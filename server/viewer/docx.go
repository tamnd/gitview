package viewer

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"strconv"
	"strings"
)

const (
	docxMaxInflate = 8 << 20 // decompressed cap for word/document.xml
	docxMaxChars   = 200000  // emitted text characters before truncation
	docxMaxIlvl    = 8
)

// Docx extracts readable text from a docx file: headings, paragraphs,
// bold and italic runs, flat lists, one level of tables, external
// hyperlinks. Layout fidelity is a non-goal; the bar is strictly
// better than a binary notice, which is what github.com shows. Every
// text node passes through template escaping and the only markup in
// the output is the fixed mapping below, so nothing unsanitized can
// reach the page (spec 2011 doc 18 section 12).
func Docx() Renderer { return docxRenderer{} }

type docxRenderer struct{}

func (docxRenderer) Kind() string { return "docx" }

func (docxRenderer) Match(in Input) bool {
	if in.Ext != ".docx" || !bytes.HasPrefix(in.Content, []byte("PK\x03\x04")) {
		return false
	}
	zr, err := zip.NewReader(bytes.NewReader(in.Content), int64(len(in.Content)))
	if err != nil {
		return false
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			return true
		}
	}
	return false
}

func (docxRenderer) Render(in Input) (Output, error) {
	zr, err := zip.NewReader(bytes.NewReader(in.Content), int64(len(in.Content)))
	if err != nil {
		return Output{}, fmt.Errorf("docx: %w", err)
	}
	doc := zipEntry(zr, "word/document.xml")
	if doc == nil {
		return Output{}, fmt.Errorf("docx: no document.xml")
	}
	rels := docxRels(zr)

	rd, err := doc.Open()
	if err != nil {
		return Output{}, fmt.Errorf("docx: %w", err)
	}
	defer func() { _ = rd.Close() }()

	x := docxExtractor{rels: rels}
	if err := x.extract(xml.NewDecoder(io.LimitReader(rd, docxMaxInflate))); err != nil {
		return Output{}, fmt.Errorf("docx: %w", err)
	}

	info := group(x.words) + " words"
	if x.words == 1 {
		info = "1 word"
	}
	note := ""
	if x.truncated {
		info += "+"
		note = "Document truncated. Download the raw file for the rest."
	}
	body := `<article class="markdown-body docx-body" data-kind="docx">` + x.out.String() + `</article>`
	return Output{Kind: "docx", Body: template.HTML(body), Info: info, Note: note}, nil
}

func zipEntry(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// docxRels maps relationship ids to external targets from the
// document rels part; a missing or broken part just means no links.
func docxRels(zr *zip.Reader) map[string]string {
	out := map[string]string{}
	f := zipEntry(zr, "word/_rels/document.xml.rels")
	if f == nil {
		return out
	}
	rd, err := f.Open()
	if err != nil {
		return out
	}
	defer func() { _ = rd.Close() }()
	raw, err := io.ReadAll(io.LimitReader(rd, 1<<20))
	if err != nil {
		return out
	}
	var rels struct {
		Relationship []struct {
			ID         string `xml:"Id,attr"`
			Target     string `xml:"Target,attr"`
			TargetMode string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if xml.Unmarshal(raw, &rels) != nil {
		return out
	}
	for _, r := range rels.Relationship {
		if r.TargetMode == "External" {
			out[r.ID] = r.Target
		}
	}
	return out
}

// docxExtractor is the one-pass state machine over the document body.
type docxExtractor struct {
	rels map[string]string

	out       strings.Builder
	chars     int
	words     int
	truncated bool

	para     strings.Builder // current paragraph content
	paraTag  string          // h1..h6, li, p
	paraIlvl int
	inList   bool

	run     strings.Builder // current run content
	runBold bool
	runItal bool

	linkOpen bool
	tblDepth int
}

func (x *docxExtractor) extract(d *xml.Decoder) error {
	inText := false
	for !x.truncated {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				x.paraTag, x.paraIlvl = "p", 0
			case "pStyle":
				x.paraTag = headingTag(attrVal(t, "val"), x.paraTag)
			case "numPr":
				if x.paraTag == "p" {
					x.paraTag = "li"
				}
			case "ilvl":
				if n, err := strconv.Atoi(attrVal(t, "val")); err == nil {
					x.paraIlvl = min(max(n, 0), docxMaxIlvl)
				}
			case "r":
				x.run.Reset()
				x.runBold, x.runItal = false, false
			case "b":
				x.runBold = flagOn(t)
			case "i":
				x.runItal = flagOn(t)
			case "t":
				inText = true
			case "tab":
				x.run.WriteByte('\t')
			case "br":
				x.run.WriteString("<br>")
			case "hyperlink":
				x.openLink(attrVal(t, "id"))
			case "tbl":
				x.tblDepth++
				if x.tblDepth == 1 {
					x.out.WriteString("<table>")
				}
			case "tr":
				if x.tblDepth == 1 {
					x.out.WriteString("<tr>")
				}
			case "tc":
				if x.tblDepth == 1 {
					x.out.WriteString("<td>")
				}
			case "drawing", "pict":
				x.run.WriteString("[image]")
				if err := d.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p":
				x.endParagraph()
			case "r":
				x.endRun()
			case "t":
				inText = false
			case "hyperlink":
				x.closeLink()
			case "tbl":
				if x.tblDepth == 1 {
					x.out.WriteString("</table>")
				}
				x.tblDepth--
			case "tr":
				if x.tblDepth == 1 {
					x.out.WriteString("</tr>")
				}
			case "tc":
				if x.tblDepth == 1 {
					x.out.WriteString("</td>")
				}
			}
		case xml.CharData:
			if inText {
				x.text(string(t))
			}
		}
	}
	if x.truncated {
		// Flush what the cut left behind and balance any open tags so
		// the fragment cannot bleed into the page that embeds it.
		x.endRun()
		x.closeLink()
		x.endParagraph()
		if x.tblDepth > 0 {
			x.out.WriteString("</td></tr></table>")
			x.tblDepth = 0
		}
	}
	if x.inList {
		x.out.WriteString("</ul>")
	}
	return nil
}

func (x *docxExtractor) text(s string) {
	if x.chars+len(s) > docxMaxChars {
		s = s[:docxMaxChars-x.chars]
		x.truncated = true
	}
	x.chars += len(s)
	x.words += len(strings.Fields(s))
	x.run.WriteString(template.HTMLEscapeString(s))
}

func (x *docxExtractor) endRun() {
	s := x.run.String()
	x.run.Reset()
	if s == "" {
		return
	}
	if x.runBold {
		s = "<strong>" + s + "</strong>"
	}
	if x.runItal {
		s = "<em>" + s + "</em>"
	}
	x.para.WriteString(s)
}

func (x *docxExtractor) endParagraph() {
	content := x.para.String()
	x.para.Reset()
	tag := x.paraTag
	if tag == "li" {
		if !x.inList {
			x.out.WriteString("<ul>")
			x.inList = true
		}
		fmt.Fprintf(&x.out, `<li class="docx-ilvl-%d">%s</li>`, x.paraIlvl, content)
		return
	}
	if x.inList {
		x.out.WriteString("</ul>")
		x.inList = false
	}
	if content == "" {
		return
	}
	x.out.WriteString("<" + tag + ">" + content + "</" + tag + ">")
}

func (x *docxExtractor) openLink(id string) {
	target, ok := x.rels[id]
	if !ok {
		return
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return
	}
	// Links wrap runs, so flush whatever the paragraph holds and tag
	// the anchor open until the matching end element.
	fmt.Fprintf(&x.para, `<a href="%s" rel="noopener nofollow ugc">`, template.HTMLEscapeString(u.String()))
	x.linkOpen = true
}

func (x *docxExtractor) closeLink() {
	if x.linkOpen {
		x.para.WriteString("</a>")
		x.linkOpen = false
	}
}

func headingTag(style, current string) string {
	if style == "Title" {
		return "h1"
	}
	if n, ok := strings.CutPrefix(style, "Heading"); ok {
		if lvl, err := strconv.Atoi(n); err == nil && lvl >= 1 && lvl <= 6 {
			return "h" + n
		}
	}
	return current
}

// flagOn reads a w:b / w:i toggle: present means on unless the val
// attribute says otherwise.
func flagOn(t xml.StartElement) bool {
	switch attrVal(t, "val") {
	case "0", "false", "none":
		return false
	}
	return true
}

func attrVal(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
