package backend

import "testing"

// FuzzParsePatch replays the seed corpus in testdata/fuzz/FuzzParsePatch
// during the normal test run. The property is freedom from panics on
// arbitrary input; correctness lives in the table tests.
func FuzzParsePatch(f *testing.F) {
	f.Fuzz(func(t *testing.T, patch string) {
		ParsePatch(patch)
	})
}
