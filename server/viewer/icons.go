package viewer

import "html/template"

// Octicon returns the inline SVG for a named icon, "" markup for unknown
// names. Page templates use this through the server's octicon func; the
// markdown pipeline uses the map directly.
func Octicon(name string) template.HTML { return octicons[name] }
