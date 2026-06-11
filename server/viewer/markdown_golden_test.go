package viewer

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the markdown golden files")

// TestMarkdownGolden renders every testdata/markdown fixture through the
// production pipeline and byte-compares with its golden. The goldens are
// the contract for the sanitizer's exact output; run with -update after a
// deliberate pipeline change and review the diff like code.
func TestMarkdownGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "markdown", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no markdown fixtures found")
	}
	mctx := MarkdownContext{RepoPath: "/octo/demo", Ref: "main", Dir: "docs"}
	for _, md := range files {
		name := strings.TrimSuffix(filepath.Base(md), ".md")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(md)
			if err != nil {
				t.Fatal(err)
			}
			got := string(RenderMarkdown(mctx, src))
			golden := strings.TrimSuffix(md, ".md") + ".html.golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden (run go test ./server -run TestMarkdownGolden -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("output drifted from %s\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
			}
		})
	}
}
