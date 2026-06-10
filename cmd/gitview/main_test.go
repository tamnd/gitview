package main

import "testing"

func TestParseHub(t *testing.T) {
	for _, tc := range []struct {
		target            string
		kind, owner, name string
		wantErr           bool
	}{
		{target: "hf:openai-community/gpt2", kind: "model", owner: "openai-community", name: "gpt2"},
		{target: "hf:datasets/squad/squad", kind: "dataset", owner: "squad", name: "squad"},
		{target: "hf:spaces/o/demo", kind: "space", owner: "o", name: "demo"},
		{target: "https://huggingface.co/openai-community/gpt2", kind: "model", owner: "openai-community", name: "gpt2"},
		{target: "https://huggingface.co/datasets/squad/squad", kind: "dataset", owner: "squad", name: "squad"},
		{target: "http://huggingface.co/spaces/o/demo", kind: "space", owner: "o", name: "demo"},
		{target: "huggingface.co/o/n", kind: "model", owner: "o", name: "n"},
		// Pasted deep links keep working; extra segments are ignored.
		{target: "https://huggingface.co/o/n/tree/main/configs", kind: "model", owner: "o", name: "n"},
		// Rootless legacy ids and overlong shorthands are malformed.
		{target: "hf:gpt2", wantErr: true},
		{target: "hf:o/n/extra", wantErr: true},
		{target: "hf:datasets/squad", wantErr: true},
		{target: "hf:", wantErr: true},
		{target: "https://huggingface.co/onlyowner", wantErr: true},
		// Non-Hub targets pass through untouched, no error.
		{target: "gh:torvalds/linux"},
		{target: "./some/dir"},
		{target: "https://github.com/o/r"},
	} {
		kind, owner, name, err := parseHub(tc.target)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseHub(%q): no error", tc.target)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHub(%q): %v", tc.target, err)
			continue
		}
		if kind != tc.kind || owner != tc.owner || name != tc.name {
			t.Errorf("parseHub(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.target, kind, owner, name, tc.kind, tc.owner, tc.name)
		}
	}
}
