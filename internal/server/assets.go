package server

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

// embeddedFiles contains the small static documents that make up the browser application.
//
//go:embed *.html *.css
var embeddedFiles embed.FS

func servePage(name string) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
		contents, err := embeddedFiles.ReadFile(name)
		if err != nil {
			http.Error(writer, "embedded UI is unavailable", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write(contents)
	}
}

func serveAsset(writer http.ResponseWriter, request *http.Request) {
	name := path.Base(strings.TrimSpace(request.PathValue("name")))
	if name != "styles.css" {
		http.NotFound(writer, request)
		return
	}
	contents, err := embeddedFiles.ReadFile(name)
	if err != nil {
		http.Error(writer, "embedded UI is unavailable", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = writer.Write(contents)
}
