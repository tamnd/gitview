package viewer

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	tabularMaxRows = 1000
	tableMaxCols   = 100
	tableMaxCell   = 400
)

// Tabular renders csv and tsv files as a table, github.com style: the
// first record is always the header, rows get a number gutter, and the
// Code view stays one toggle away. A file whose first record does not
// parse even lazily falls through to the code viewer.
func Tabular() Renderer { return tabularRenderer{} }

type tabularRenderer struct{}

func (tabularRenderer) Kind() string { return "csv" }

func (tabularRenderer) Match(in Input) bool {
	if in.Binary || in.Plain {
		return false
	}
	switch in.Ext {
	case ".csv", ".tsv", ".tab":
		return true
	}
	return false
}

func (tabularRenderer) Render(in Input) (Output, error) {
	text := string(in.Content)
	rd := csv.NewReader(strings.NewReader(text))
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	rd.Comma = delimiterFor(in.Ext, text)

	header, err := rd.Read()
	if err != nil {
		return Output{}, fmt.Errorf("tabular: %w", err)
	}

	// Keep the first cap's worth of rows, then keep counting: the input
	// is at most the 10 MB blob cap, so the full count is cheap and the
	// Note can be honest about what was cut.
	type row struct {
		Num   int
		Cells []string
	}
	var rows []row
	totalRows, maxCols := 0, len(header)
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A record broken beyond lazy parsing ends the table; what
			// rendered so far is still worth showing.
			break
		}
		totalRows++
		if len(rec) > maxCols {
			maxCols = len(rec)
		}
		if totalRows <= tabularMaxRows {
			rows = append(rows, row{Num: totalRows, Cells: rec})
		}
	}

	shownCols := min(maxCols, tableMaxCols)
	data := struct {
		Dialect string
		Header  []string
		Rows    []row
	}{
		Dialect: strings.TrimPrefix(in.Ext, "."),
		Header:  padCells(header, shownCols),
	}
	for _, r := range rows {
		r.Cells = padCells(r.Cells, shownCols)
		data.Rows = append(data.Rows, r)
	}

	body, err := renderFragment("tabular", data)
	if err != nil {
		return Output{}, err
	}
	var notes []string
	if totalRows > tabularMaxRows {
		notes = append(notes, fmt.Sprintf("Showing %s of %s rows", group(tabularMaxRows), group(totalRows)))
	}
	if maxCols > tableMaxCols {
		if len(notes) == 0 {
			notes = append(notes, fmt.Sprintf("Showing %d of %s columns", tableMaxCols, group(maxCols)))
		} else {
			notes = append(notes, fmt.Sprintf("and %d of %s columns", tableMaxCols, group(maxCols)))
		}
	}
	return Output{
		Kind:   "csv",
		Body:   body,
		Info:   fmt.Sprintf("%s rows x %s columns", group(totalRows), group(maxCols)),
		Toggle: true,
		Note:   strings.Join(notes, " "),
	}, nil
}

// delimiterFor picks tab for tsv extensions and otherwise comma, with
// one heuristic: a first line carrying semicolons but no commas is
// European csv.
func delimiterFor(ext, text string) rune {
	if ext == ".tsv" || ext == ".tab" {
		return '\t'
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.Contains(line, ",") && strings.Contains(line, ";") {
			return ';'
		}
		break
	}
	return ','
}

// padCells truncates long cells and pads ragged rows to the rendered
// width so every table row has the same shape.
func padCells(cells []string, width int) []string {
	out := make([]string, width)
	for i := 0; i < width && i < len(cells); i++ {
		out[i] = truncateCell(cells[i])
	}
	return out
}

func truncateCell(s string) string {
	if utf8.RuneCountInString(s) <= tableMaxCell {
		return s
	}
	return string([]rune(s)[:tableMaxCell]) + "..."
}

// group formats an integer with thousands separators for Note text.
func group(n int) string { return group64(int64(n)) }
