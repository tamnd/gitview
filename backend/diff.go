package backend

import (
	"strconv"
	"strings"
)

// TooLargeLines is the per-file changed-line count beyond which hunks are
// dropped and the UI shows a folded "load diff" box, like github.com.
const TooLargeLines = 3000

// ParsePatch parses git unified diff output (git diff-tree -p / the patch
// strings in GitHub's commit API) into FileDiffs. It is forgiving: unknown
// header lines are skipped, truncated input yields what was parsed.
func ParsePatch(patch string) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var hunk *Hunk
	oldN, newN := 0, 0

	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			finishFile(cur)
			files = append(files, *cur)
		}
		cur = nil
	}

	for line := range strings.Lines(patch) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &FileDiff{}
			oldP, newP := parseDiffGitPaths(line)
			cur.OldPath, cur.NewPath = oldP, newP
		case cur == nil:
			// preamble noise
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			h, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			hunk = &h
			oldN, newN = h.OldStart, h.NewStart
		case hunk != nil && (line == "" || line[0] == ' ' || line[0] == '+' || line[0] == '-'):
			// A bare empty line is an empty context line; some tools
			// strip the trailing space git emits.
			if line == "" {
				line = " "
			}
			dl := DiffLine{Text: line[1:]}
			switch line[0] {
			case ' ':
				dl.Kind, dl.OldNum, dl.NewNum = Context, oldN, newN
				oldN++
				newN++
			case '+':
				dl.Kind, dl.NewNum = Addition, newN
				newN++
				cur.Additions++
			case '-':
				dl.Kind, dl.OldNum = Deletion, oldN
				oldN++
				cur.Deletions++
			}
			hunk.Lines = append(hunk.Lines, dl)
		case strings.HasPrefix(line, `\ No newline`):
			if hunk != nil && len(hunk.Lines) > 0 {
				hunk.Lines[len(hunk.Lines)-1].NoEOF = true
			}
		case hunk != nil:
			// stray content inside a hunk; ignore
		case strings.HasPrefix(line, "rename from "):
			cur.Status = Renamed
			cur.OldPath = line[len("rename from "):]
		case strings.HasPrefix(line, "rename to "):
			cur.NewPath = line[len("rename to "):]
		case strings.HasPrefix(line, "copy from "):
			cur.Status = Copied
			cur.OldPath = line[len("copy from "):]
		case strings.HasPrefix(line, "copy to "):
			cur.NewPath = line[len("copy to "):]
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = Added
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = Deleted
		case strings.HasPrefix(line, "Binary files ") || line == "GIT binary patch":
			cur.Binary = true
		case strings.HasPrefix(line, "--- "):
			if p, ok := stripPathPrefix(line[4:], "a/"); ok {
				cur.OldPath = p
			}
		case strings.HasPrefix(line, "+++ "):
			if p, ok := stripPathPrefix(line[4:], "b/"); ok {
				cur.NewPath = p
			}
		}
	}
	flushFile()
	return files
}

func finishFile(f *FileDiff) {
	if f.Status == "" {
		switch {
		case f.OldPath == "" && f.NewPath != "":
			f.Status = Added
		case f.NewPath == "" && f.OldPath != "":
			f.Status = Deleted
		default:
			f.Status = Modified
		}
	}
	if f.OldPath == "" {
		f.OldPath = f.NewPath
	}
	if f.NewPath == "" {
		f.NewPath = f.OldPath
	}
	if f.Additions+f.Deletions > TooLargeLines {
		f.TooLarge = true
		f.Hunks = nil
	}
}

// stripPathPrefix handles "a/path", "b/path", quoted forms, and /dev/null.
func stripPathPrefix(s, prefix string) (string, bool) {
	s = unquotePath(strings.TrimSuffix(s, "\t"))
	if s == "/dev/null" {
		return "", true
	}
	if p, ok := strings.CutPrefix(s, prefix); ok {
		return p, true
	}
	return "", false
}

// parseDiffGitPaths pulls best-effort paths out of `diff --git a/x b/y`.
// The ---/+++ and rename headers that follow are authoritative; this only
// matters for mode-only and binary changes, where those lines are absent.
func parseDiffGitPaths(line string) (string, string) {
	rest := line[len("diff --git "):]
	// Unambiguous when neither path contains " b/".
	if i := strings.Index(rest, " b/"); i >= 0 && strings.HasPrefix(rest, "a/") {
		return unquotePath(rest[2:i]), unquotePath(rest[i+3:])
	}
	return "", ""
}

func unquotePath(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// parseHunkHeader parses `@@ -12,7 +12,9 @@ context`.
func parseHunkHeader(line string) (Hunk, bool) {
	h := Hunk{Header: line}
	rest, ok := strings.CutPrefix(line, "@@ -")
	if !ok {
		return h, false
	}
	end := strings.Index(rest, " @@")
	if end < 0 {
		return h, false
	}
	parts := strings.SplitN(rest[:end], " +", 2)
	if len(parts) != 2 {
		return h, false
	}
	var okOld, okNew bool
	h.OldStart, h.OldLines, okOld = parseRange(parts[0])
	h.NewStart, h.NewLines, okNew = parseRange(parts[1])
	return h, okOld && okNew
}

func parseRange(s string) (start, n int, ok bool) {
	n = 1
	before, after, found := strings.Cut(s, ",")
	start, err := strconv.Atoi(before)
	if err != nil {
		return 0, 0, false
	}
	if found {
		if n, err = strconv.Atoi(after); err != nil {
			return 0, 0, false
		}
	}
	return start, n, true
}
