package server

import "embed"

// embeddedFiles contains the single-file browser demo served by this package.
//
//go:embed index.html
var embeddedFiles embed.FS
