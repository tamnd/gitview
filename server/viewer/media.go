package viewer

import (
	"html/template"
	"strings"
)

// Audio renders an <audio> element over the raw URL. preload="metadata"
// keeps a directory crawl from pulling whole files; never autoplay.
// Codec support is the browser's problem: the element shows its native
// error UI when it cannot play the file.
func Audio() Renderer { return audioRenderer{} }

type audioRenderer struct{}

func (audioRenderer) Kind() string { return "audio" }

func (audioRenderer) Match(in Input) bool {
	mt, ok := MIME(in.Ext)
	return ok && strings.HasPrefix(mt, "audio/")
}

func (audioRenderer) Render(in Input) (Output, error) {
	body := `<div class="blob-media" data-kind="audio"><audio controls preload="metadata" src="` +
		template.HTMLEscapeString(in.RawURL) + `"></audio></div>`
	return Output{Kind: "audio", Body: template.HTML(body)}, nil
}

// Video renders a <video> element over the raw URL. Same rules as audio:
// metadata preload, no autoplay, no poster. CSS caps the element at the
// blob box width and 70vh.
func Video() Renderer { return videoRenderer{} }

type videoRenderer struct{}

func (videoRenderer) Kind() string { return "video" }

func (videoRenderer) Match(in Input) bool {
	mt, ok := MIME(in.Ext)
	return ok && strings.HasPrefix(mt, "video/")
}

func (videoRenderer) Render(in Input) (Output, error) {
	body := `<div class="blob-media" data-kind="video"><video controls preload="metadata" src="` +
		template.HTMLEscapeString(in.RawURL) + `"></video></div>`
	return Output{Kind: "video", Body: template.HTML(body)}, nil
}
