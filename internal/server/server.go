// Package server exposes the verification-platform browser API and embedded page.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/draft"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/run"
	"codex-hackathon-july2026/internal/template"
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
	Templates     *template.Repository
	Defaults      ModelDefaults
	ReviewerModel string
	Presets       []Preset
	Logger        *slog.Logger
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
	if config.Templates == nil {
		return nil, errors.New("template repository is required")
	}
	if config.Logger == nil {
		return nil, errors.New("server logger is required")
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

	setup := setupResponse{Models: config.Models.Options(), Defaults: config.Defaults}
	starts := newStartRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/setup", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, setup)
	})
	mux.HandleFunc("POST /api/signature-draft", signatureDraftHandler(config))
	mux.HandleFunc("GET /api/templates", func(writer http.ResponseWriter, _ *http.Request) {
		templates, err := config.Templates.List()
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, errorResponse{Error: "template library is unavailable"})
			return
		}
		writeJSON(writer, http.StatusOK, templates)
	})
	mux.HandleFunc("POST /api/templates", func(writer http.ResponseWriter, request *http.Request) {
		input, err := decodeTemplateCreateRequest(writer, request)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		created, err := config.Templates.Create(template.CreateInput(input))
		if err != nil {
			if errors.Is(err, template.ErrAlreadyExists) {
				writeJSON(writer, http.StatusConflict, errorResponse{Error: err.Error()})
				return
			}
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(writer, http.StatusCreated, created)
	})
	mux.HandleFunc("GET /api/templates/{id}", func(writer http.ResponseWriter, request *http.Request) {
		id := strings.TrimSpace(request.PathValue("id"))
		selected, err := config.Templates.Load(id)
		if err != nil {
			writeTemplateError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, selected)
	})
	mux.HandleFunc("PUT /api/templates/{id}", func(writer http.ResponseWriter, request *http.Request) {
		id := strings.TrimSpace(request.PathValue("id"))
		input, err := decodeTemplateUpdateRequest(writer, request)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		updated, err := config.Templates.Update(id, template.UpdateInput(input))
		if err != nil {
			writeTemplateError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	})
	mux.HandleFunc("POST /api/templates/{id}/runs", func(writer http.ResponseWriter, request *http.Request) {
		id := strings.TrimSpace(request.PathValue("id"))
		input, err := decodeTemplateRunRequest(writer, request)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		selected, err := config.Templates.Load(id)
		if err != nil {
			writeTemplateError(writer, err)
			return
		}
		roles, err := rolesForTemplateRun(config, input)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}

		runID, err := starts.start(input.RequestID, func() (string, error) {
			return config.Store.StartRunWithTemplate(taskForTemplate(selected), roles, run.TemplateProvenance{ID: selected.ID, Digest: selected.Digest})
		})
		if err != nil {
			if errors.Is(err, run.ErrRunActive) {
				writeJSON(writer, http.StatusConflict, errorResponse{Error: err.Error()})
				return
			}
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(writer, http.StatusAccepted, startResponse{ID: runID})
	})
	mux.HandleFunc("GET /api/runs", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, config.Store.ListRuns())
	})
	mux.HandleFunc("POST /api/runs/{id}/cancel", func(writer http.ResponseWriter, request *http.Request) {
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
	mux.HandleFunc("GET /api/runs/{id}", func(writer http.ResponseWriter, request *http.Request) {
		id := strings.TrimSpace(request.PathValue("id"))
		snapshot, found := config.Store.GetRun(id)
		if !found {
			writeJSON(writer, http.StatusNotFound, errorResponse{Error: "run not found"})
			return
		}
		writeJSON(writer, http.StatusOK, snapshot)
	})
	mux.HandleFunc("GET /templates", servePage("templates.html"))
	mux.HandleFunc("GET /templates/new", servePage("template.html"))
	mux.HandleFunc("GET /templates/{id}", servePage("template.html"))
	mux.HandleFunc("GET /runs", servePage("runs.html"))
	mux.HandleFunc("GET /runs/{id}", servePage("run.html"))
	mux.HandleFunc("GET /assets/{name}", serveAsset)
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		http.Redirect(writer, request, "/templates", http.StatusSeeOther)
	})
	registerLegacyAPI(mux, config, starts)

	return withRequestLogging(mux, config.Logger), nil
}

// registerLegacyAPI preserves the original programmatic browser API while the visible product
// moves to saved templates and plural page routes. It remains generated-oracle only and keeps the
// old strict request boundary intact; new browser pages use /api and server-loaded templates.
func registerLegacyAPI(mux *http.ServeMux, config Config, starts *startRegistry) {
	legacySetup := setupResponse{Models: config.Models.Options(), Defaults: config.Defaults, Presets: append([]Preset(nil), config.Presets...)}
	mux.HandleFunc("GET /setup", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, legacySetup)
	})
	mux.HandleFunc("POST /signature-draft", signatureDraftHandler(config))
	mux.HandleFunc("POST /run", func(writer http.ResponseWriter, request *http.Request) {
		input, err := decodeRunRequest(writer, request)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
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
		runID, err := starts.start(input.RequestID, func() (string, error) {
			return config.Store.StartRun(taskForRunRequest(input), run.Roles{Coder: coder, Tester: tester, Reviewer: reviewer, CoderModel: input.CoderModel, TesterModel: input.TesterModel, ReviewerModel: config.ReviewerModel})
		})
		if err != nil {
			if errors.Is(err, run.ErrRunActive) {
				writeJSON(writer, http.StatusConflict, errorResponse{Error: err.Error()})
				return
			}
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(writer, http.StatusAccepted, startResponse{ID: runID})
	})
	mux.HandleFunc("POST /run/{id}/cancel", func(writer http.ResponseWriter, request *http.Request) {
		cancelRun(writer, request, config.Store)
	})
	mux.HandleFunc("GET /run/{id}", func(writer http.ResponseWriter, request *http.Request) {
		getRun(writer, request, config.Store)
	})
}

func signatureDraftHandler(config Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		input, err := decodeSignatureDraftRequest(writer, request)
		if err != nil {
			config.Logger.Warn("signature draft rejected", "reason", "invalid_request")
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		author, err := config.Models.Resolve(input.TesterModel)
		if err != nil {
			config.Logger.Warn("signature draft rejected", "model", input.TesterModel, "reason", "unknown_model")
			writeJSON(writer, http.StatusBadRequest, errorResponse{Error: "unknown test-writer model"})
			return
		}

		config.Logger.Info("signature draft started", "model", input.TesterModel, "spec_bytes", len(input.Spec))
		signature, err := config.Draft.Suggest(request.Context(), author, input.Spec)
		if err != nil {
			failure := classifySignatureDraftFailure(err)
			attrs := []any{
				"model", input.TesterModel,
				"spec_bytes", len(input.Spec),
				"failure_kind", failure.kind,
				"duration", time.Since(startedAt),
			}
			if failure.providerStatus != 0 {
				attrs = append(attrs, "provider_status", failure.providerStatus)
			}
			config.Logger.Warn("signature draft failed", attrs...)
			writeJSON(writer, http.StatusBadGateway, errorResponse{Error: failure.publicMessage})
			return
		}
		config.Logger.Info("signature draft succeeded",
			"model", input.TesterModel,
			"spec_bytes", len(input.Spec),
			"signature_bytes", len(signature),
			"duration", time.Since(startedAt),
		)
		writeJSON(writer, http.StatusOK, signatureDraftResponse{Signature: signature, Model: input.TesterModel})
	}
}

type signatureDraftFailure struct {
	kind           string
	providerStatus int
	publicMessage  string
}

func classifySignatureDraftFailure(err error) signatureDraftFailure {
	if errors.Is(err, context.Canceled) {
		return signatureDraftFailure{kind: "canceled", publicMessage: "signature drafting was canceled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return signatureDraftFailure{kind: "deadline_exceeded", publicMessage: "signature drafting timed out; check the server log"}
	}
	if errors.Is(err, draft.ErrResponseTooLarge) {
		return signatureDraftFailure{kind: "response_too_large", publicMessage: "signature drafting failed because the provider response was too large"}
	}
	if errors.Is(err, draft.ErrInvalidSignature) {
		return signatureDraftFailure{kind: "invalid_provider_output", publicMessage: "signature drafting failed because the provider did not return a valid Go signature"}
	}
	var providerErr *llm.HTTPStatusError
	if errors.As(err, &providerErr) {
		return signatureDraftFailure{
			kind:           "provider_http",
			providerStatus: providerErr.StatusCode,
			publicMessage:  fmt.Sprintf("signature drafting failed: provider returned HTTP %d; check the server log", providerErr.StatusCode),
		}
	}
	return signatureDraftFailure{kind: "provider_transport", publicMessage: "signature drafting failed while contacting the provider; check the server log"}
}

func cancelRun(writer http.ResponseWriter, request *http.Request, store *run.Store) {
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
}

func getRun(writer http.ResponseWriter, request *http.Request, store *run.Store) {
	id := strings.TrimSpace(request.PathValue("id"))
	snapshot, found := store.GetRun(id)
	if !found {
		writeJSON(writer, http.StatusNotFound, errorResponse{Error: "run not found"})
		return
	}
	writeJSON(writer, http.StatusOK, snapshot)
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

type templateCreateRequest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Spec      string `json:"spec"`
	Signature string `json:"signature"`
}

type templateUpdateRequest struct {
	Name      string `json:"name"`
	Spec      string `json:"spec"`
	Signature string `json:"signature"`
}

type templateRunRequest struct {
	RequestID   string `json:"requestId"`
	CoderModel  string `json:"coderModel"`
	TesterModel string `json:"testerModel"`
}

func taskForTemplate(selected template.Template) domain.Task {
	return domain.Task{
		Name:      selected.Name,
		Spec:      selected.Spec,
		Signature: selected.Signature,
		Oracle:    domain.OracleGenerated,
	}
}

func rolesForTemplateRun(config Config, input templateRunRequest) (run.Roles, error) {
	coder, err := config.Models.Resolve(input.CoderModel)
	if err != nil {
		return run.Roles{}, errors.New("unknown code-writer model")
	}
	tester, err := config.Models.Resolve(input.TesterModel)
	if err != nil {
		return run.Roles{}, errors.New("unknown test-writer model")
	}
	reviewer, err := config.Models.Resolve(config.ReviewerModel)
	if err != nil {
		return run.Roles{}, errors.New("configured oracle reviewer model is unavailable")
	}
	return run.Roles{
		Coder:         coder,
		Tester:        tester,
		Reviewer:      reviewer,
		CoderModel:    input.CoderModel,
		TesterModel:   input.TesterModel,
		ReviewerModel: config.ReviewerModel,
	}, nil
}

func decodeTemplateCreateRequest(writer http.ResponseWriter, request *http.Request) (templateCreateRequest, error) {
	var input templateCreateRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		return templateCreateRequest{}, err
	}
	return input, nil
}

func decodeTemplateUpdateRequest(writer http.ResponseWriter, request *http.Request) (templateUpdateRequest, error) {
	var input templateUpdateRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		return templateUpdateRequest{}, err
	}
	return input, nil
}

func decodeTemplateRunRequest(writer http.ResponseWriter, request *http.Request) (templateRunRequest, error) {
	var input templateRunRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		return templateRunRequest{}, err
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.CoderModel = strings.TrimSpace(input.CoderModel)
	input.TesterModel = strings.TrimSpace(input.TesterModel)
	if err := validateRequestID(input.RequestID); err != nil {
		return templateRunRequest{}, err
	}
	if input.CoderModel == "" {
		return templateRunRequest{}, errors.New("code-writer model is required")
	}
	if input.TesterModel == "" {
		return templateRunRequest{}, errors.New("test-writer model is required")
	}
	return input, nil
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, value any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRunRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return err
	}
	return nil
}

func writeTemplateError(writer http.ResponseWriter, err error) {
	if errors.Is(err, template.ErrNotFound) {
		writeJSON(writer, http.StatusNotFound, errorResponse{Error: "template not found"})
		return
	}
	if errors.Is(err, template.ErrUnsafeStorage) {
		writeJSON(writer, http.StatusInternalServerError, errorResponse{Error: "template library is unavailable"})
		return
	}
	writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
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

func withRequestLogging(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		observed := &observedResponseWriter{ResponseWriter: writer}
		next.ServeHTTP(observed, request)
		if observed.status == 0 {
			observed.status = http.StatusOK
		}
		level := slog.LevelDebug
		if request.Method != http.MethodGet && observed.status < http.StatusBadRequest {
			level = slog.LevelInfo
		}
		if observed.status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if observed.status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		logger.Log(request.Context(), level, "http request completed",
			"method", request.Method,
			"path", request.URL.Path,
			"status", observed.status,
			"duration", time.Since(startedAt),
		)
	})
}

type observedResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *observedResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *observedResponseWriter) Write(contents []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(contents)
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
