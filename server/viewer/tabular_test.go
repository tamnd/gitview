package viewer

import (
	"fmt"
	"strings"
	"testing"
)

func renderTabular(t *testing.T, name, content string) Output {
	t.Helper()
	r := Tabular()
	in := Input{Name: name, Ext: ext(name), Content: []byte(content)}
	if !r.Match(in) {
		t.Fatalf("%s did not match", name)
	}
	out, err := r.Render(in)
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return out
}

func ext(name string) string {
	i := strings.LastIndex(name, ".")
	return strings.ToLower(name[i:])
}

func TestTabularComma(t *testing.T) {
	out := renderTabular(t, "x.csv", "a,b,c\n1,2,3\n4,5,6\n")
	body := string(out.Body)
	for _, w := range []string{`data-dialect="csv"`, "<th>a</th>", "<td>6</td>", `data-kind="csv"`} {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q", w)
		}
	}
	if out.Info != "2 rows x 3 columns" {
		t.Errorf("info = %q", out.Info)
	}
	if !out.Toggle {
		t.Error("tabular should offer the code toggle")
	}
	if out.Note != "" {
		t.Errorf("unexpected note %q", out.Note)
	}
}

func TestTabularTabAndSemicolon(t *testing.T) {
	out := renderTabular(t, "x.tsv", "a\tb\n1\t2\n")
	if !strings.Contains(string(out.Body), `data-dialect="tsv"`) || !strings.Contains(string(out.Body), "<th>b</th>") {
		t.Errorf("tsv body wrong: %s", out.Body)
	}

	out = renderTabular(t, "euro.csv", "a;b\n1;2\n")
	if !strings.Contains(string(out.Body), "<th>b</th>") {
		t.Errorf("semicolon heuristic failed: %s", out.Body)
	}

	// Commas win over semicolons when both appear.
	out = renderTabular(t, "mixed.csv", "a,b;c\n1,2\n")
	if !strings.Contains(string(out.Body), "<th>b;c</th>") {
		t.Errorf("comma should win: %s", out.Body)
	}
}

func TestTabularRaggedAndQuotes(t *testing.T) {
	// Ragged rows pad to the widest row; stray quotes are data.
	out := renderTabular(t, "x.csv", "a,b\n1\n2,3,4\nsay \"hi,x\n")
	body := string(out.Body)
	if out.Info != "3 rows x 3 columns" {
		t.Errorf("info = %q", out.Info)
	}
	if !strings.Contains(body, "<td>4</td>") {
		t.Errorf("wide row lost a cell: %s", body)
	}
	if !strings.Contains(body, "say &#34;hi") {
		t.Errorf("lazy quote not preserved as data: %s", body)
	}
}

func TestTabularEmbeddedNewline(t *testing.T) {
	out := renderTabular(t, "x.csv", "a,b\n\"line1\nline2\",2\n")
	if !strings.Contains(string(out.Body), "<td>line1\nline2</td>") {
		t.Errorf("embedded newline lost: %s", out.Body)
	}
}

func TestTabularCaps(t *testing.T) {
	var b strings.Builder
	cols := make([]string, 120)
	for i := range cols {
		cols[i] = fmt.Sprintf("c%d", i)
	}
	b.WriteString(strings.Join(cols, ","))
	b.WriteByte('\n')
	row := strings.Join(cols, ",")
	for i := 0; i < 1500; i++ {
		b.WriteString(row)
		b.WriteByte('\n')
	}
	out := renderTabular(t, "big.csv", b.String())
	if out.Note != "Showing 1,000 of 1,500 rows and 100 of 120 columns" {
		t.Errorf("note = %q", out.Note)
	}
	if out.Info != "1,500 rows x 120 columns" {
		t.Errorf("info = %q", out.Info)
	}
	if strings.Contains(string(out.Body), "<th>c100</th>") {
		t.Error("column cap not applied")
	}
	if got := strings.Count(string(out.Body), "<tr>"); got != 1001 {
		t.Errorf("rendered %d rows, want 1001 including header", got)
	}
}

func TestTabularCellTruncation(t *testing.T) {
	long := strings.Repeat("x", 500)
	out := renderTabular(t, "x.csv", "a\n"+long+"\n")
	body := string(out.Body)
	if strings.Contains(body, long) {
		t.Error("long cell not truncated")
	}
	if !strings.Contains(body, strings.Repeat("x", 400)+"...") {
		t.Error("truncation suffix missing")
	}
}

func TestTabularDeclinesPlainBinaryAndGarbage(t *testing.T) {
	r := Tabular()
	if r.Match(Input{Ext: ".csv", Plain: true}) {
		t.Error("matched plain view")
	}
	if r.Match(Input{Ext: ".csv", Binary: true}) {
		t.Error("matched binary")
	}
	if r.Match(Input{Ext: ".txt"}) {
		t.Error("matched txt")
	}
	// First record unparseable even lazily: error falls to code viewer.
	if _, err := r.Render(Input{Ext: ".csv", Content: []byte("")}); err == nil {
		t.Error("empty input should error")
	}
}

func TestTabularHostileContentEscaped(t *testing.T) {
	out := renderTabular(t, "x.csv", "<script>,b\n<img src=x>,2\n")
	body := string(out.Body)
	if strings.Contains(body, "<script>") || strings.Contains(body, "<img") {
		t.Errorf("hostile cells not escaped: %s", body)
	}
}

func FuzzTabular(f *testing.F) {
	f.Add("a,b\n1,2\n")
	f.Add("a\tb\n1\t2\n")
	f.Add("a;b\n1;2\n")
	f.Add("\"unterminated\n")
	f.Add(strings.Repeat("x,", 200) + "\n")
	f.Fuzz(func(t *testing.T, s string) {
		r := Tabular()
		in := Input{Name: "f.csv", Ext: ".csv", Content: []byte(s)}
		out, err := r.Render(in)
		if err != nil {
			return
		}
		if !strings.Contains(string(out.Body), "<table") {
			t.Errorf("rendered without a table: %q", out.Body)
		}
	})
}
