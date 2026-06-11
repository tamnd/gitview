package viewer

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func writeParquet[T any](t *testing.T, rows []T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[T](&buf)
	if _, err := w.Write(rows); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func renderParquet(t *testing.T, content []byte) Output {
	t.Helper()
	r := Parquet()
	in := Input{Name: "x.parquet", Ext: ".parquet", Content: content, Binary: true}
	if !r.Match(in) {
		t.Fatal("fixture did not match")
	}
	out, err := r.Render(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

type flatRow struct {
	ID    int64     `parquet:"id"`
	Name  string    `parquet:"name"`
	Score float64   `parquet:"score"`
	OK    bool      `parquet:"ok"`
	TS    time.Time `parquet:"ts"`
	Blob  []byte    `parquet:"blob"`
	Note  *string   `parquet:"note,optional"`
}

func TestParquetFlat(t *testing.T) {
	note := "hello"
	content := writeParquet(t, []flatRow{
		{ID: 1, Name: "alpha", Score: 1.5, OK: true,
			TS:   time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
			Blob: []byte{0xff, 0xfe, 0x00, 0x01}, Note: &note},
		{ID: 2, Name: "beta"},
	})
	out := renderParquet(t, content)
	body := string(out.Body)

	// Schema panel lists every leaf with type and repetition.
	for _, w := range []string{
		"<td>id</td>", "<td>name</td>",
		"BYTE_ARRAY (STRING)", "<td>required</td>", "<td>optional</td>",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("schema panel missing %q", w)
		}
	}
	if !strings.Contains(body, "TIMESTAMP") {
		t.Errorf("timestamp logical type missing from schema: %s", body)
	}

	// Values: text, float, bool, ISO timestamp, hex preview, null.
	for _, w := range []string{
		"<td>alpha</td>", "<td>1.5</td>", "<td>true</td>",
		"2026-06-01T12:00:00", "0xfffe0001... (4 bytes)", "<em>null</em>",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("rows missing %q", w)
		}
	}
	if !strings.HasPrefix(out.Info, "2 rows x 7 columns") {
		t.Errorf("info = %q", out.Info)
	}
	if out.Toggle {
		t.Error("parquet must not offer a code toggle")
	}
	if out.Note != "" {
		t.Errorf("unexpected note %q", out.Note)
	}
}

type nestedRow struct {
	ID   int64    `parquet:"id"`
	Tags []string `parquet:"tags,list"`
}

func TestParquetNested(t *testing.T) {
	content := writeParquet(t, []nestedRow{
		{ID: 1, Tags: []string{"a", "b"}},
		{ID: 2},
	})
	out := renderParquet(t, content)
	body := string(out.Body)
	if !strings.Contains(body, "tags.") {
		t.Errorf("nested leaf not dotted: %s", body)
	}
	if !strings.Contains(body, `[&#34;a&#34;,&#34;b&#34;]`) {
		t.Errorf("repeated values not compact JSON: %s", body)
	}
}

func TestParquetRowCap(t *testing.T) {
	rows := make([]flatRow, 1000)
	for i := range rows {
		rows[i] = flatRow{ID: int64(i)}
	}
	out := renderParquet(t, writeParquet(t, rows))
	if out.Note != "Showing 100 of 1,000 rows" {
		t.Errorf("note = %q", out.Note)
	}
	if !strings.HasPrefix(out.Info, "1,000 rows x 7 columns") {
		t.Errorf("info = %q", out.Info)
	}
	if got := strings.Count(string(out.Body), `<td class="data-num">`); got != 100 {
		t.Errorf("rendered %d rows, want 100", got)
	}
}

func TestParquetCorruptAndMagic(t *testing.T) {
	r := Parquet()

	// Wrong magic never matches; the registry moves on.
	if r.Match(Input{Ext: ".parquet", Content: []byte("not a parquet file")}) {
		t.Error("matched without magic")
	}
	if r.Match(Input{Ext: ".csv", Content: []byte("PAR1xxxxPAR1")}) {
		t.Error("matched wrong extension")
	}

	// Right magic, garbage middle: OpenFile fails, Render errors, and
	// the registry's fall-through shows the binary notice instead.
	in := Input{Ext: ".parquet", Content: []byte("PAR1garbagegarbagePAR1")}
	if !r.Match(in) {
		t.Fatal("magic fixture should match")
	}
	if _, err := r.Render(in); err == nil {
		t.Error("corrupt file should error")
	}

	// A valid file truncated mid-footer also errors instead of panicking.
	content := writeParquet(t, []flatRow{{ID: 1}})
	trunc := append([]byte{}, content[:len(content)-12]...)
	trunc = append(trunc, []byte("PAR1")...)
	if _, err := r.Render(Input{Ext: ".parquet", Content: trunc}); err == nil {
		t.Error("truncated file should error")
	}
}
