package viewer

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"
)

const parquetMaxRows = 100

// Parquet renders the schema panel and the first rows of a parquet
// file. The schema comes first because for large datasets the schema
// is the document; the row count in the Note is free footer metadata,
// never a full scan. A corrupt or truncated file falls through to the
// structural binary notice per the registry's error semantics.
func Parquet() Renderer { return parquetRenderer{} }

type parquetRenderer struct{}

func (parquetRenderer) Kind() string { return "parquet" }

func (parquetRenderer) Match(in Input) bool {
	if in.Ext != ".parquet" && in.Ext != ".pq" {
		return false
	}
	return len(in.Content) >= 8 &&
		bytes.HasPrefix(in.Content, []byte("PAR1")) &&
		bytes.HasSuffix(in.Content, []byte("PAR1"))
}

// parquetLeaf is one leaf column of the schema, in depth-first order,
// which is also the order Value.Column() indexes use.
type parquetLeaf struct {
	Name       string // dotted path through groups
	Type       string // physical type plus logical annotation
	Repetition string
	typ        parquet.Type
	repeated   bool
}

type parquetCell struct {
	Text string
	Null bool
}

func (parquetRenderer) Render(in Input) (Output, error) {
	f, err := parquet.OpenFile(bytes.NewReader(in.Content), int64(len(in.Content)),
		parquet.SkipPageIndex(true), parquet.SkipBloomFilters(true))
	if err != nil {
		return Output{}, fmt.Errorf("parquet: %w", err)
	}

	var leaves []parquetLeaf
	collectLeaves("", f.Schema().Fields(), false, &leaves)
	if len(leaves) == 0 {
		return Output{}, fmt.Errorf("parquet: no columns")
	}
	shownCols := min(len(leaves), tableMaxCols)

	var rows []parquetRow
	for _, rg := range f.RowGroups() {
		if len(rows) >= parquetMaxRows {
			break
		}
		rows = append(rows, readGroupRows(rg, leaves, shownCols, parquetMaxRows-len(rows))...)
	}
	data := struct {
		Columns []parquetLeaf
		Header  []string
		Rows    []parquetRow
	}{Columns: leaves}
	for _, l := range leaves[:shownCols] {
		data.Header = append(data.Header, l.Name)
	}
	for i := range rows {
		rows[i].Num = i + 1
	}
	data.Rows = rows

	body, err := renderFragment("parquet", data)
	if err != nil {
		return Output{}, err
	}
	total := f.NumRows()
	var notes []string
	if total > int64(len(rows)) {
		notes = append(notes, fmt.Sprintf("Showing %s of %s rows",
			group(len(rows)), group64(total)))
	}
	if len(leaves) > tableMaxCols {
		if len(notes) == 0 {
			notes = append(notes, fmt.Sprintf("Showing %d of %s columns", tableMaxCols, group(len(leaves))))
		} else {
			notes = append(notes, fmt.Sprintf("and %d of %s columns", tableMaxCols, group(len(leaves))))
		}
	}
	groups := "row groups"
	if len(f.RowGroups()) == 1 {
		groups = "row group"
	}
	return Output{
		Kind: "parquet",
		Body: body,
		Info: fmt.Sprintf("%s rows x %s columns, %d %s",
			group64(total), group(len(leaves)), len(f.RowGroups()), groups),
		Note: strings.Join(notes, " "),
	}, nil
}

type parquetRow struct {
	Num   int
	Cells []parquetCell
}

func readGroupRows(rg parquet.RowGroup, leaves []parquetLeaf, shownCols, limit int) []parquetRow {
	rows := rg.Rows()
	defer func() { _ = rows.Close() }()
	buf := make([]parquet.Row, 32)
	var out []parquetRow
	for len(out) < limit {
		n, err := rows.ReadRows(buf)
		for _, r := range buf[:n] {
			if len(out) >= limit {
				break
			}
			out = append(out, parquetRow{Cells: formatRow(r, leaves, shownCols)})
		}
		if err != nil || n == 0 {
			break
		}
	}
	return out
}

// formatRow groups the flat value stream by leaf column and formats
// one cell per rendered column.
func formatRow(r parquet.Row, leaves []parquetLeaf, shownCols int) []parquetCell {
	byCol := make([][]parquet.Value, len(leaves))
	for _, v := range r {
		if c := v.Column(); c >= 0 && c < len(leaves) {
			byCol[c] = append(byCol[c], v)
		}
	}
	cells := make([]parquetCell, shownCols)
	for i := 0; i < shownCols; i++ {
		cells[i] = formatCell(byCol[i], leaves[i])
	}
	return cells
}

func formatCell(vals []parquet.Value, leaf parquetLeaf) parquetCell {
	if len(vals) == 0 {
		return parquetCell{Null: true}
	}
	if len(vals) == 1 && !leaf.repeated && vals[0].RepetitionLevel() == 0 {
		if vals[0].IsNull() {
			return parquetCell{Null: true}
		}
		return parquetCell{Text: truncateCell(formatScalar(vals[0], leaf.typ))}
	}
	// Repeated or reconstructed-from-levels values render as compact
	// JSON of the leaf's scalars.
	parts := make([]any, 0, len(vals))
	for _, v := range vals {
		if v.IsNull() {
			parts = append(parts, nil)
		} else {
			parts = append(parts, formatScalar(v, leaf.typ))
		}
	}
	if len(parts) == 1 && parts[0] == nil {
		return parquetCell{Null: true}
	}
	j, err := json.Marshal(parts)
	if err != nil {
		return parquetCell{Null: true}
	}
	return parquetCell{Text: truncateCell(string(j))}
}

func formatScalar(v parquet.Value, t parquet.Type) string {
	lt := t.LogicalType()
	switch v.Kind() {
	case parquet.Boolean:
		return strconv.FormatBool(v.Boolean())
	case parquet.Int32:
		if lt != nil && lt.Date != nil {
			return time.Unix(int64(v.Int32())*86400, 0).UTC().Format("2006-01-02")
		}
		return strconv.FormatInt(int64(v.Int32()), 10)
	case parquet.Int64:
		if lt != nil && lt.Timestamp != nil {
			return formatTimestamp(v.Int64(), lt.Timestamp.Unit)
		}
		return strconv.FormatInt(v.Int64(), 10)
	case parquet.Float:
		return strconv.FormatFloat(float64(v.Float()), 'g', -1, 32)
	case parquet.Double:
		return strconv.FormatFloat(v.Double(), 'g', -1, 64)
	case parquet.ByteArray, parquet.FixedLenByteArray:
		b := v.ByteArray()
		if isTextual(lt) || utf8.Valid(b) {
			return string(b)
		}
		n := min(len(b), 8)
		return "0x" + hex.EncodeToString(b[:n]) + fmt.Sprintf("... (%d bytes)", len(b))
	default:
		return v.String()
	}
}

func isTextual(lt *format.LogicalType) bool {
	return lt != nil && (lt.UTF8 != nil || lt.Json != nil || lt.Enum != nil)
}

func formatTimestamp(n int64, unit format.TimeUnit) string {
	var ts time.Time
	switch {
	case unit.Micros != nil:
		ts = time.UnixMicro(n)
	case unit.Nanos != nil:
		ts = time.Unix(0, n)
	default:
		ts = time.UnixMilli(n)
	}
	return ts.UTC().Format(time.RFC3339)
}

func collectLeaves(prefix string, fields []parquet.Field, parentRepeated bool, out *[]parquetLeaf) {
	for _, f := range fields {
		name := f.Name()
		if prefix != "" {
			name = prefix + "." + name
		}
		rep := "required"
		switch {
		case f.Optional():
			rep = "optional"
		case f.Repeated():
			rep = "repeated"
		}
		if f.Leaf() {
			*out = append(*out, parquetLeaf{
				Name:       name,
				Type:       leafTypeString(f.Type()),
				Repetition: rep,
				typ:        f.Type(),
				repeated:   parentRepeated || f.Repeated(),
			})
		} else {
			collectLeaves(name, f.Fields(), parentRepeated || f.Repeated(), out)
		}
	}
}

func leafTypeString(t parquet.Type) string {
	s := t.Kind().String()
	lt := t.LogicalType()
	if lt == nil {
		return s
	}
	switch {
	case lt.UTF8 != nil:
		return s + " (STRING)"
	case lt.Timestamp != nil:
		return s + " (TIMESTAMP " + timeUnitString(lt.Timestamp.Unit) + ")"
	case lt.Time != nil:
		return s + " (TIME " + timeUnitString(lt.Time.Unit) + ")"
	case lt.Date != nil:
		return s + " (DATE)"
	case lt.Decimal != nil:
		return s + fmt.Sprintf(" (DECIMAL %d.%d)", lt.Decimal.Precision, lt.Decimal.Scale)
	case lt.Integer != nil:
		sign := "UINT"
		if lt.Integer.IsSigned {
			sign = "INT"
		}
		return s + fmt.Sprintf(" (%s %d)", sign, lt.Integer.BitWidth)
	case lt.Json != nil:
		return s + " (JSON)"
	case lt.Bson != nil:
		return s + " (BSON)"
	case lt.UUID != nil:
		return s + " (UUID)"
	case lt.Enum != nil:
		return s + " (ENUM)"
	default:
		return s
	}
}

func timeUnitString(unit format.TimeUnit) string {
	switch {
	case unit.Micros != nil:
		return "us"
	case unit.Nanos != nil:
		return "ns"
	default:
		return "ms"
	}
}

func group64(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
