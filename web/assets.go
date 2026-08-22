// Package web embeds the templates and static assets into the compiled
// binary, so deploying the app is copying one file.
package web

import "embed"

//go:embed templates static
var Files embed.FS
