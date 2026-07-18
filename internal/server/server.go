// Package server exposes the repair-loop browser API and embedded page.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/run"
)

// New returns the browser handler for one configured task and its run store.
func New(store *run.Store, task domain.Task) (http.Handler, error) {
	if store == nil {
		return nil, errors.New("run store is required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /task", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, taskResponse{
			Task:      task.Name,
			Spec:      task.Spec,
			Signature: task.Signature,
			Oracle:    "authored",
			TestCode:  task.TestCode,
		})
	})
	mux.HandleFunc("POST /run", func(writer http.ResponseWriter, request *http.Request) {
		id, err := store.StartRun(task)
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(writer, http.StatusAccepted, startResponse{ID: id})
	})
	mux.HandleFunc("POST /run/{id}/cancel", func(writer http.ResponseWriter, request *http.Request) {
		id := strings.TrimSpace(request.PathValue("id"))
		found, canceled := store.CancelRun(id)
		if !found {
			writeJSON(writer, http.StatusNotFound, errorResponse{Error: "run not found"})
			return
		}
		if !canceled {
			writeJSON(writer, http.StatusConflict, errorResponse{Error: "run is no longer cancelable"})
			return
		}
		writeJSON(writer, http.StatusAccepted, startResponse{ID: id})
	})
	mux.HandleFunc("GET /run/{id}", func(writer http.ResponseWriter, request *http.Request) {
		id := strings.TrimSpace(request.PathValue("id"))
		snapshot, found := store.GetRun(id)
		if !found {
			writeJSON(writer, http.StatusNotFound, errorResponse{Error: "run not found"})
			return
		}
		writeJSON(writer, http.StatusOK, snapshot)
	})
	mux.HandleFunc("GET /", serveIndex)

	return mux, nil
}

type startResponse struct {
	ID string `json:"id"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type taskResponse struct {
	Task      string `json:"task"`
	Spec      string `json:"spec"`
	Signature string `json:"signature"`
	Oracle    string `json:"oracle"`
	TestCode  string `json:"testCode"`
}

func serveIndex(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}

	contents, err := embeddedFiles.ReadFile("index.html")
	if err != nil {
		http.Error(writer, "embedded UI is unavailable", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write(contents)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
