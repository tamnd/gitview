package viewer

import (
	"errors"
	"strings"
	"testing"
)

type fakeRenderer struct {
	kind  string
	match bool
	err   error
}

func (f fakeRenderer) Kind() string     { return f.kind }
func (f fakeRenderer) Match(Input) bool { return f.match }
func (f fakeRenderer) Render(Input) (Output, error) {
	if f.err != nil {
		return Output{}, f.err
	}
	return Output{Kind: f.kind}, nil
}

func TestRegistryFirstMatchWins(t *testing.T) {
	g := NewRegistry(
		fakeRenderer{kind: "a", match: false},
		fakeRenderer{kind: "b", match: true},
		fakeRenderer{kind: "c", match: true},
	)
	out, ok := g.Render(Input{})
	if !ok || out.Kind != "b" {
		t.Fatalf("got %q ok=%v, want b", out.Kind, ok)
	}
}

func TestRegistryErrorFallsThrough(t *testing.T) {
	g := NewRegistry(
		fakeRenderer{kind: "broken", match: true, err: errors.New("corrupt")},
		fakeRenderer{kind: "fallback", match: true},
	)
	out, ok := g.Render(Input{})
	if !ok || out.Kind != "fallback" {
		t.Fatalf("got %q ok=%v, want fallback", out.Kind, ok)
	}
}

func TestRegistryNothingMatches(t *testing.T) {
	g := NewRegistry(fakeRenderer{kind: "a", match: false})
	if _, ok := g.Render(Input{}); ok {
		t.Fatal("expected no render")
	}
}

func TestProbeDoesNotRender(t *testing.T) {
	g := NewRegistry(
		fakeRenderer{kind: "broken", match: true, err: errors.New("corrupt")},
		fakeRenderer{kind: "next", match: true},
	)
	// Probe reports the first match even when its Render would fail;
	// it answers "what kind claims this", not "what would succeed".
	k, ok := g.Probe(Input{})
	if !ok || k != "broken" {
		t.Fatalf("probe = %q ok=%v, want broken", k, ok)
	}
}

func TestMediaRenderersEscapeAttributes(t *testing.T) {
	in := Input{
		Name:   `x"><script>.mp4`,
		Ext:    ".mp4",
		RawURL: `/r/raw/main/x"><script>.mp4`,
	}
	for _, r := range []Renderer{Audio(), Video(), PDF(), Image()} {
		out, err := r.Render(in)
		if err != nil {
			t.Fatalf("%s: %v", r.Kind(), err)
		}
		if strings.Contains(string(out.Body), "<script>") {
			t.Errorf("%s body not escaped: %s", r.Kind(), out.Body)
		}
	}
}

func TestMediaMatch(t *testing.T) {
	cases := []struct {
		ext  string
		kind string
	}{
		{".mp3", "audio"},
		{".flac", "audio"},
		{".opus", "audio"},
		{".mp4", "video"},
		{".webm", "video"},
		{".mov", "video"},
		{".pdf", "pdf"},
		{".png", "image"},
		{".svg", "image"},
	}
	g := NewRegistry(Image(), Audio(), Video(), PDF(), Markdown(), Code())
	for _, c := range cases {
		k, ok := g.Probe(Input{Ext: c.ext, Binary: true})
		if !ok || k != c.kind {
			t.Errorf("%s -> %q ok=%v, want %q", c.ext, k, ok, c.kind)
		}
	}
	// Plain never disables a binary-backed viewer.
	if k, _ := g.Probe(Input{Ext: ".mp4", Binary: true, Plain: true}); k != "video" {
		t.Errorf("plain mp4 -> %q, want video", k)
	}
}
