package backend

import "testing"

const samplePatch = `diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@ package main
 package main

-func main() {}
+func main() {
+}
diff --git a/old.txt b/new.txt
similarity index 90%
rename from old.txt
rename to new.txt
index 3333333..4444444 100644
--- a/old.txt
+++ b/new.txt
@@ -1 +1 @@
-hello
+hello world
\ No newline at end of file
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 5555555..0000000
--- a/gone.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-a
-b
diff --git a/logo.png b/logo.png
new file mode 100644
index 0000000..6666666
Binary files /dev/null and b/logo.png differ
`

func TestParsePatch(t *testing.T) {
	files := ParsePatch(samplePatch)
	if len(files) != 4 {
		t.Fatalf("got %d files, want 4", len(files))
	}

	m := files[0]
	if m.NewPath != "main.go" || m.Status != Modified {
		t.Errorf("file 0: %+v", m)
	}
	if m.Additions != 2 || m.Deletions != 1 {
		t.Errorf("file 0 counts: +%d -%d", m.Additions, m.Deletions)
	}
	if len(m.Hunks) != 1 {
		t.Fatalf("file 0 hunks: %d", len(m.Hunks))
	}
	h := m.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 3 || h.NewStart != 1 || h.NewLines != 4 {
		t.Errorf("hunk header parse: %+v", h)
	}
	if len(h.Lines) != 5 {
		t.Fatalf("hunk lines: %d (empty context line must count)", len(h.Lines))
	}
	if h.Lines[0].Kind != Context || h.Lines[0].OldNum != 1 || h.Lines[0].NewNum != 1 {
		t.Errorf("line 0: %+v", h.Lines[0])
	}
	if h.Lines[1].Kind != Context || h.Lines[1].Text != "" || h.Lines[1].OldNum != 2 {
		t.Errorf("empty context line: %+v", h.Lines[1])
	}
	last := h.Lines[len(h.Lines)-1]
	if last.Kind != Addition || last.NewNum != 4 || last.OldNum != 0 || last.Text != "}" {
		t.Errorf("last line: %+v", last)
	}

	r := files[1]
	if r.Status != Renamed || r.OldPath != "old.txt" || r.NewPath != "new.txt" {
		t.Errorf("rename: %+v", r)
	}
	if !r.Hunks[0].Lines[1].NoEOF {
		t.Error("missing NoEOF on final addition")
	}

	d := files[2]
	if d.Status != Deleted || d.OldPath != "gone.txt" || d.NewPath != "gone.txt" {
		t.Errorf("delete: %+v", d)
	}
	if d.Deletions != 2 || d.Additions != 0 {
		t.Errorf("delete counts: +%d -%d", d.Additions, d.Deletions)
	}

	b := files[3]
	if b.Status != Added || !b.Binary || b.NewPath != "logo.png" {
		t.Errorf("binary add: %+v", b)
	}
}

func TestParsePatchEmptyAndGarbage(t *testing.T) {
	if got := ParsePatch(""); len(got) != 0 {
		t.Errorf("empty patch: %v", got)
	}
	if got := ParsePatch("not a diff at all\njust noise\n"); len(got) != 0 {
		t.Errorf("garbage: %v", got)
	}
	// Truncated mid-hunk input still yields the file.
	got := ParsePatch("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-a\n")
	if len(got) != 1 || got[0].Deletions != 1 {
		t.Errorf("truncated: %+v", got)
	}
}

func TestParsePatchQuotedPaths(t *testing.T) {
	patch := "diff --git \"a/sp ce.txt\" \"b/sp ce.txt\"\n" +
		"--- \"a/sp ce.txt\"\n+++ \"b/sp ce.txt\"\n@@ -1 +1 @@\n-x\n+y\n"
	got := ParsePatch(patch)
	if len(got) != 1 || got[0].NewPath != "sp ce.txt" {
		t.Errorf("quoted path: %+v", got)
	}
}
