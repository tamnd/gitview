package viewer

// mimeByExt is the one table behind both the raw endpoint's Content-Type
// and the media viewers' Match, so a previewable extension and a served
// type can never disagree (doc 18 section 4.1).
var mimeByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".svg":  "image/svg+xml",

	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".flac": "audio/flac",
	".m4a":  "audio/mp4",
	".opus": "audio/ogg",

	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".ogv":  "video/ogg",

	".pdf": "application/pdf",
}

// MIME returns the Content-Type for a lowercased extension with dot.
func MIME(ext string) (string, bool) {
	mt, ok := mimeByExt[ext]
	return mt, ok
}
