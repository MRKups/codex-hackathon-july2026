// Package web contained the first embedded-asset placement sketch.
//
// The active page lives in internal/server so the HTTP layer does not import upward into a UI
// package. _archive is excluded from normal Go builds.
package web

import "embed"

// Files contained the single-page browser demo.
//
//go:embed index.html
var Files embed.FS
