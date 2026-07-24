// Package server exposes the verification-platform browser API and embedded page.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/draft"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/run"
)

const (
	maxRunRequestBytes = 32 << 10
	maxTaskNameBytes   = 120
	maxSpecBytes       = 24 << 10
	maxSignatureBytes  = 2 << 10
	maxRequestIDBytes  = 128
)

// ModelDefaults supplies the selected browser defaults. Both values must belong to Models.
type ModelDefaults struct {
	CoderModel  string `json:"coderModel"`
	TesterModel string `json:"testerModel"`
}

// Preset is an editable browser starting point. It intentionally has no oracle source: every
// interactive task uses the same generated-oracle path after the user submits the form.
type Preset struct {
	Name      string `json:"name"`
	Spec      string `json:"spec"`
	Signature string `json:"signature"`
}

// Config wires the HTTP layer to an in-memory store and a safe, provider-configured model
// allowlist. It contains no provider credentials in any API response.
type Config struct {
	Store         *run.Store
	Models        *llm.ModelCatalog
	Draft         *draft.Service
	Defaults      ModelDefaults
	ReviewerModel string
	Presets       []Preset
}

// New returns the browser handler for interactive generated-oracle runs.
func New(config Config) (http.Handler, error) {
	if config.Store == nil {
		return nil, errors.New("run store is required")
	}
	if config.Models == nil {
		return nil, errors.New("model catalog is required")
	}
	if config.Draft == nil {
		return nil, errors.New("signature draft service is required")
	}
	if _, err := config.Models.Resolve(config.Defaults.CoderModel); err != nil {
		return nil, fmt.Errorf("resolve default coder model: %w", err)
	}
	if _, err := config.Models.Resolve(config.Defaults.TesterModel); err != nil {
		return nil, fmt.Errorf("resolve default test-writer model: %w", err)
	}
	if _, err := config.Models.Resolve(config.ReviewerModel); err != nil {
		return nil, fmt.Errorf("resolve default oracle reviewer model: %w", err)
	}
	for index, preset := range config.Presets {
		if err := validatePreset(preset); err != nil {
			return nil, fmt.Errorf("preset %d: %w", index+1, err)
		}
	}

	setup := setupResponse{
		Models:   config.Models.Options(),
		Defaults: config.Defaults,
		Presets:  append([]Preset(nil), config.Presets...),
	}
	starts := newStartRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, setup)
	})
	mux.HandleFunc("POST /signature-draft", func(writer http.ResponseWriter, request *http.Request) {
		input, err := decodeSignatureDraftRequest(writer, request)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		author, err := config.Models.Resolve(input.TesterModel)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: "unknown test-writer model"})
			return
		}
		signature, err := config.Draft.Suggest(request.Context(), author, input.Spec)
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, errorResponse{Error: "signature drafting failed"})
			return
		}
		writeJSON(writer, http.StatusOK, signatureDraftResponse{Signature: signature, Model: input.TesterModel})
	})
	mux.HandleFunc("POST /run", func(writer http.ResponseWriter, request *http.Request) {
		input, err := decodeRunRequest(writer, request)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}

		task := taskForRunRequest(input)

		coder, err := config.Models.Resolve(input.CoderModel)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: "unknown code-writer model"})
			return
		}

		tester, err := config.Models.Resolve(input.TesterModel)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: "unknown test-writer model"})
			return
		}
		reviewer, err := config.Models.Resolve(config.ReviewerModel)
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, errorResponse{Error: "configured oracle reviewer model is unavailable"})
			return
		}

		id, err := starts.start(input.RequestID, func() (string, error) {
			return config.Store.StartRun(
				task,
				run.Roles{
					Coder:         coder,
					Tester:        tester,
					Reviewer:      reviewer,
					CoderModel:    input.CoderModel,
					TesterModel:   input.TesterModel,
					ReviewerModel: config.ReviewerModel,
				},
			)
		})
		if err != nil {
			if errors.Is(err, run.ErrRunActive) {
				writeJSON(writer, http.StatusConflict, errorResponse{Error: err.Error()})
				return
			}
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(writer, http.StatusAccepted, startResponse{ID: id})
	})
	mux.HandleFunc("POST /run/{id}/cancel", func(writer http.ResponseWriter, request *http.Request) {
		id := strings.TrimSpace(request.PathValue("id"))
		found, canceled := config.Store.CancelRun(id)
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
		snapshot, found := config.Store.GetRun(id)
		if !found {
			writeJSON(writer, http.StatusNotFound, errorResponse{Error: "run not found"})
			return
		}
		writeJSON(writer, http.StatusOK, snapshot)
	})
	mux.HandleFunc("GET /", serveIndex)

	return mux, nil
}

type setupResponse struct {
	Models   []llm.ModelOption `json:"models"`
	Defaults ModelDefaults     `json:"defaults"`
	Presets  []Preset          `json:"presets"`
}

type startResponse struct {
	ID string `json:"id"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type signatureDraftRequest struct {
	Spec        string `json:"spec"`
	TesterModel string `json:"testerModel"`
}

type signatureDraftResponse struct {
	Signature string `json:"signature"`
	Model     string `json:"model"`
}

type runRequest struct {
	RequestID   string `json:"requestId"`
	Name        string `json:"name"`
	Spec        string `json:"spec"`
	Signature   string `json:"signature"`
	CoderModel  string `json:"coderModel"`
	TesterModel string `json:"testerModel"`
}

func taskForRunRequest(input runRequest) domain.Task {
	return domain.Task{
		Name:      input.Name,
		Spec:      input.Spec,
		Signature: input.Signature,
		Oracle:    domain.OracleGenerated,
	}
}

func decodeRunRequest(writer http.ResponseWriter, request *http.Request) (runRequest, error) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRunRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var input runRequest
	if err := decoder.Decode(&input); err != nil {
		return runRequest{}, fmt.Errorf("invalid run request: %w", err)
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return runRequest{}, err
	}
	return normalizeRunRequest(input)
}

func decodeSignatureDraftRequest(writer http.ResponseWriter, request *http.Request) (signatureDraftRequest, error) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRunRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input signatureDraftRequest
	if err := decoder.Decode(&input); err != nil {
		return signatureDraftRequest{}, fmt.Errorf("invalid signature draft request: %w", err)
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return signatureDraftRequest{}, err
	}
	input.Spec = strings.TrimSpace(input.Spec)
	input.TesterModel = strings.TrimSpace(input.TesterModel)
	if input.Spec == "" {
		return signatureDraftRequest{}, errors.New("task specification is required")
	}
	if len(input.Spec) > maxSpecBytes {
		return signatureDraftRequest{}, fmt.Errorf("task specification exceeds %d bytes", maxSpecBytes)
	}
	if input.TesterModel == "" {
		return signatureDraftRequest{}, errors.New("test-writer model is required")
	}
	return input, nil
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("invalid run request: %w", err)
	}
	return errors.New("invalid run request: multiple JSON values")
}

func normalizeRunRequest(input runRequest) (runRequest, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Spec = strings.TrimSpace(input.Spec)
	input.Signature = strings.TrimSpace(input.Signature)
	input.CoderModel = strings.TrimSpace(input.CoderModel)
	input.TesterModel = strings.TrimSpace(input.TesterModel)
	if input.Name == "" {
		input.Name = "custom-task"
	}
	if err := validateRequestID(input.RequestID); err != nil {
		return runRequest{}, err
	}
	if len(input.Name) > maxTaskNameBytes {
		return runRequest{}, fmt.Errorf("task name exceeds %d bytes", maxTaskNameBytes)
	}
	if input.Spec == "" {
		return runRequest{}, errors.New("task specification is required")
	}
	if len(input.Spec) > maxSpecBytes {
		return runRequest{}, fmt.Errorf("task specification exceeds %d bytes", maxSpecBytes)
	}
	if input.Signature == "" {
		return runRequest{}, errors.New("Go function signature is required")
	}
	if len(input.Signature) > maxSignatureBytes {
		return runRequest{}, fmt.Errorf("Go function signature exceeds %d bytes", maxSignatureBytes)
	}
	if err := validateFunctionSignature(input.Signature); err != nil {
		return runRequest{}, err
	}
	if input.CoderModel == "" {
		return runRequest{}, errors.New("code-writer model is required")
	}
	if input.TesterModel == "" {
		return runRequest{}, errors.New("test-writer model is required")
	}
	return input, nil
}

func validateRequestID(requestID string) error {
	if requestID == "" {
		return errors.New("request ID is required")
	}
	if len(requestID) > maxRequestIDBytes {
		return fmt.Errorf("request ID exceeds %d bytes", maxRequestIDBytes)
	}
	for _, character := range requestID {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' {
			continue
		}
		return errors.New("request ID may contain only letters, digits, hyphens, and underscores")
	}
	return nil
}

func validatePreset(preset Preset) error {
	_, err := normalizeRunRequest(runRequest{
		RequestID:   "preset-validation",
		Name:        preset.Name,
		Spec:        preset.Spec,
		Signature:   preset.Signature,
		CoderModel:  "preset-validation",
		TesterModel: "preset-validation",
	})
	return err
}

func validateFunctionSignature(signature string) error {
	if err := domain.ValidateSignature(signature); err != nil {
		return fmt.Errorf("Go function signature is invalid: %w", err)
	}
	return nil
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

// startRegistry makes browser start requests idempotent for the lifetime of the process. If a
// local connection loses the accepted response, the browser can safely retry the same token and
// recover the existing run ID rather than starting another paid provider run.
type startRegistry struct {
	mu   sync.Mutex
	runs map[string]string
}

func newStartRegistry() *startRegistry {
	return &startRegistry{runs: make(map[string]string)}
}

func (registry *startRegistry) start(requestID string, start func() (string, error)) (string, error) {
	if requestID == "" {
		return "", errors.New("request ID is required")
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if id, found := registry.runs[requestID]; found {
		return id, nil
	}

	id, err := start()
	if err != nil {
		return "", err
	}
	registry.runs[requestID] = id
	return id, nil
}
