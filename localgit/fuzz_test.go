package localgit

import "testing"

// FuzzParseBlame replays the seed corpus in testdata/fuzz/FuzzParseBlame
// during the normal test run. The property is freedom from panics on
// arbitrary porcelain streams; correctness lives in TestBlame.
func FuzzParseBlame(f *testing.F) {
	f.Fuzz(func(t *testing.T, out []byte) {
		parseBlame(out)
	})
}
