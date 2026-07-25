package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/draft"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/oracle"
	"codex-hackathon-july2026/internal/repair"
	"codex-hackathon-july2026/internal/run"
	"codex-hackathon-july2026/internal/template"
)

func TestNewServesInteractiveGeneratedOracleAPI(t *testing.T) {
	handler := newHandler(t, providerFor(t, func(model string) string {
		switch model {
		case "test-model":
			return incrementTestCode
		case "code-model":
			return correctIncrementCode
		default:
			t.Fatalf("unexpected provider model %q", model)
			return ""
		}
	}))

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/templates" {
		t.Fatalf("GET / = status %d location %q, want redirect to /templates", page.Code, page.Header().Get("Location"))
	}
	templatesPage := httptest.NewRecorder()
	handler.ServeHTTP(templatesPage, httptest.NewRequest(http.MethodGet, "/templates", nil))
	if templatesPage.Code != http.StatusOK || !strings.Contains(templatesPage.Body.String(), "Task templates") {
		t.Fatalf("GET /templates did not serve the template library: status %d body %s", templatesPage.Code, templatesPage.Body.String())
	}
	setupPage := httptest.NewRecorder()
	handler.ServeHTTP(setupPage, httptest.NewRequest(http.MethodGet, "/setup", nil))
	if setupPage.Code != http.StatusOK {
		t.Fatalf("GET /setup status = %d, want %d", setupPage.Code, http.StatusOK)
	}
	if got := setupPage.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET /setup Cache-Control = %q, want no-store", got)
	}
	var setup setupResponse
	if err := json.NewDecoder(setupPage.Body).Decode(&setup); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	if len(setup.Models) != 2 || setup.Models[0].ID != "code-model" || setup.Models[1].ID != "test-model" {
		t.Fatalf("setup models = %#v, want configured allowlist", setup.Models)
	}
	if setup.Defaults != (ModelDefaults{CoderModel: "code-model", TesterModel: "test-model"}) {
		t.Fatalf("setup defaults = %#v", setup.Defaults)
	}
	if len(setup.Presets) != 1 || setup.Presets[0].Name != "increment" {
		t.Fatalf("setup presets = %#v", setup.Presets)
	}

	started := startGeneratedRun(t, handler, runRequest{
		RequestID:   "interactive_generated_001",
		Name:        "custom-increment",
		Spec:        "Return the input integer increased by one.",
		Signature:   "func Increment(value int) int",
		CoderModel:  "code-model",
		TesterModel: "test-model",
	})
	got := waitForRun(t, handler, started.ID)
	if got.Status != run.StatusPassed {
		t.Fatalf("GET /run status = %q, want passed; error = %q", got.Status, got.Error)
	}
	if got.Oracle != "generated" || got.TestCode != incrementTestCode {
		t.Fatalf("run frozen oracle = mode %q source %q", got.Oracle, got.TestCode)
	}
	if got.CoderModel != "code-model" || got.TesterModel != "test-model" {
		t.Fatalf("run models = coder %q tester %q", got.CoderModel, got.TesterModel)
	}
	if len(got.Attempts) != 1 || !got.Attempts[0].Passed {
		t.Fatalf("GET /run attempts = %#v, want one passing attempt", got.Attempts)
	}
}

func TestRunAPIRejectsInvalidCustomInput(t *testing.T) {
	handler := newHandler(t, providerFor(t, func(string) string { return incrementTestCode }))

	tests := []struct {
		name      string
		body      string
		wantError string
	}{
		{
			name:      "missing request ID",
			body:      `{"spec":"Return one.","signature":"func One() int","coderModel":"code-model","testerModel":"test-model"}`,
			wantError: "request ID is required",
		},
		{
			name:      "blank request ID",
			body:      `{"requestId":" \t ","spec":"Return one.","signature":"func One() int","coderModel":"code-model","testerModel":"test-model"}`,
			wantError: "request ID is required",
		},
		{
			name: "unknown model",
			body: `{"requestId":"unknown_model","spec":"Return one.","signature":"func One() int","coderModel":"missing","testerModel":"test-model"}`,
		},
		{
			name: "test code is forbidden",
			body: `{"requestId":"test_code","spec":"Return one.","signature":"func One() int","coderModel":"code-model","testerModel":"test-model","testCode":"package solution"}`,
		},
		{
			name: "unrecognized verifier field is forbidden",
			body: `{"requestId":"verifier","spec":"Return one.","signature":"func One() int","coderModel":"code-model","testerModel":"test-model","verifier":"untrusted"}`,
		},
		{
			name: "verification bundle is forbidden",
			body: `{"requestId":"bundle","spec":"Return one.","signature":"func One() int","coderModel":"code-model","testerModel":"test-model","bundle":{"testCode":"package solution"}}`,
		},
		{
			name: "Rulebook override is forbidden",
			body: `{"requestId":"rulebook","spec":"Return one.","signature":"func One() int","coderModel":"code-model","testerModel":"test-model","rulebook":"weaken all checks"}`,
		},
		{
			name: "oracle policy override is forbidden",
			body: `{"requestId":"oracle_policy","spec":"Return one.","signature":"func One() int","coderModel":"code-model","testerModel":"test-model","oraclePolicy":{"review":false}}`,
		},
		{
			name: "bad signature",
			body: `{"requestId":"bad_signature","spec":"Return one.","signature":"not a function","coderModel":"code-model","testerModel":"test-model"}`,
		},
		{
			name: "type-invalid signature",
			body: `{"requestId":"type_invalid_signature","spec":"Return one.","signature":"func One(value MissingType) int","coderModel":"code-model","testerModel":"test-model"}`,
		},
		{
			name: "invalid request ID",
			body: `{"requestId":"not allowed!","spec":"Return one.","signature":"func One() int","coderModel":"code-model","testerModel":"test-model"}`,
		},
		{
			name: "multiple values",
			body: `{"requestId":"multiple_values","spec":"Return one.","signature":"func One() int","coderModel":"code-model","testerModel":"test-model"} {}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("POST /run status = %d, want 400; body = %s", response.Code, response.Body.String())
			}
			if test.wantError != "" && !strings.Contains(response.Body.String(), test.wantError) {
				t.Fatalf("POST /run error = %s, want it to contain %q", response.Body.String(), test.wantError)
			}
		})
	}
}

func TestSignatureDraftAPIValidatesAndHasNoRunSideEffect(t *testing.T) {
	handler := newHandler(t, providerFor(t, func(model string) string {
		if model != "test-model" {
			t.Fatalf("unexpected model %q", model)
		}
		return "func Increment(value int) int"
	}))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/signature-draft", strings.NewReader(`{"spec":"Return the input plus one.","testerModel":"test-model"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /signature-draft status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	var drafted signatureDraftResponse
	if err := json.NewDecoder(response.Body).Decode(&drafted); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if drafted.Signature != "func Increment(value int) int" || drafted.Model != "test-model" {
		t.Fatalf("signature draft = %#v", drafted)
	}

	for _, body := range []string{
		`{"testerModel":"test-model"}`,
		`{"spec":"Return one.","testerModel":"unknown"}`,
		`{"spec":"Return one.","testerModel":"test-model","signature":"forbidden"}`,
	} {
		bad := httptest.NewRecorder()
		handler.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/signature-draft", strings.NewReader(body)))
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("POST /signature-draft body %s status = %d, want 400", body, bad.Code)
		}
	}
}

func TestSignatureDraftFailureLogsSafeProviderStatus(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	provider := llm.ClientFactoryFunc(func(string) (llm.LLM, error) {
		return providerTestLLM{complete: func(context.Context, string) (string, error) {
			return "", &llm.HTTPStatusError{StatusCode: 500}
		}}, nil
	})
	handler := newHandlerWithLogger(t, provider, logger)

	const spec = "SERVER_SPEC_LOG_SENTINEL must never appear in a log"
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/signature-draft", strings.NewReader(`{"spec":"`+spec+`","testerModel":"test-model"}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("POST /api/signature-draft status = %d, want %d: %s", response.Code, http.StatusBadGateway, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "provider returned HTTP 500") {
		t.Fatalf("signature draft response = %s, want safe provider status", response.Body.String())
	}

	logs := output.String()
	for _, want := range []string{"signature draft failed", "model=test-model", "failure_kind=provider_http", "provider_status=500", "http request completed", "status=502"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs = %q, want %q", logs, want)
		}
	}
	if strings.Contains(logs, spec) {
		t.Fatalf("logs exposed submitted specification: %s", logs)
	}
}

func TestTaskForRunRequestCreatesGeneratedTask(t *testing.T) {
	input := runRequest{
		Name:      "custom-task",
		Spec:      "Return the input value.",
		Signature: "func Echo(value int) int",
	}

	got := taskForRunRequest(input)
	if got.Name != input.Name || got.Spec != input.Spec || got.Signature != input.Signature {
		t.Fatalf("taskForRunRequest() = %#v, want request task fields", got)
	}
	if got.Oracle != domain.OracleGenerated || got.TestCode != "" {
		t.Fatalf("taskForRunRequest() = %#v, want generated task without test source", got)
	}
}

func TestRunAPIReusesRequestIDWithoutStartingAnotherRun(t *testing.T) {
	var mu sync.Mutex
	providerCalls := 0
	handler := newHandler(t, providerFor(t, func(model string) string {
		mu.Lock()
		providerCalls++
		mu.Unlock()
		switch model {
		case "test-model":
			return incrementTestCode
		case "code-model":
			return correctIncrementCode
		default:
			t.Fatalf("unexpected provider model %q", model)
			return ""
		}
	}))

	input := validRunRequest()
	input.RequestID = "browser_start_001"
	first := startGeneratedRun(t, handler, input)
	firstRun := waitForRun(t, handler, first.ID)
	if firstRun.Status != run.StatusPassed {
		t.Fatalf("first run status = %q, want passed; error = %q", firstRun.Status, firstRun.Error)
	}

	second := startGeneratedRun(t, handler, input)
	if second.ID != first.ID {
		t.Fatalf("retried request ID returned %q, want original %q", second.ID, first.ID)
	}
	mu.Lock()
	gotProviderCalls := providerCalls
	mu.Unlock()
	if gotProviderCalls != 2 {
		t.Fatalf("provider calls = %d, want one tester + one coder for the original run only", gotProviderCalls)
	}
}

func TestRunAPIRejectsUnknownRun(t *testing.T) {
	handler := newHandler(t, providerFor(t, func(string) string { return incrementTestCode }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/run/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /run/missing status = %d, want 404", response.Code)
	}
}

func TestRunAPICancelsActiveTestWriterAndRejectsSecondStart(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	provider := llm.ClientFactoryFunc(func(modelID string) (llm.LLM, error) {
		if modelID == "test-model" {
			return providerTestLLM{complete: func(ctx context.Context, _ string) (string, error) {
				once.Do(func() { close(started) })
				<-ctx.Done()
				return "", ctx.Err()
			}}, nil
		}
		return providerTestLLM{complete: func(ctx context.Context, _ string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return correctIncrementCode, nil
		}}, nil
	})

	handler := newHandler(t, provider)
	input := validRunRequest()
	first := startGeneratedRun(t, handler, input)
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("test-writer provider request did not start")
	}
	replayed := startGeneratedRun(t, handler, input)
	if replayed.ID != first.ID {
		t.Fatalf("replayed live request ID returned %q, want original %q", replayed.ID, first.ID)
	}

	second := httptest.NewRecorder()
	secondInput := validRunRequest()
	secondInput.RequestID = "other_live_request"
	body, err := json.Marshal(secondInput)
	if err != nil {
		t.Fatalf("marshal second run request: %v", err)
	}
	secondRequest := httptest.NewRequest(http.MethodPost, "/run", bytes.NewReader(body))
	secondRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusConflict {
		t.Fatalf("second POST /run status = %d, want 409; body = %s", second.Code, second.Body.String())
	}

	cancel := httptest.NewRecorder()
	handler.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/run/"+first.ID+"/cancel", nil))
	if cancel.Code != http.StatusAccepted {
		t.Fatalf("POST /run/{id}/cancel status = %d, want 202; body = %s", cancel.Code, cancel.Body.String())
	}
	got := waitForRun(t, handler, first.ID)
	if got.Status != run.StatusCanceled {
		t.Fatalf("canceled run status = %q, want canceled; error = %q", got.Status, got.Error)
	}
	if got.Stage != run.PhaseComplete {
		t.Fatalf("canceled run stage = %q, want complete", got.Stage)
	}

	terminalCancel := httptest.NewRecorder()
	handler.ServeHTTP(terminalCancel, httptest.NewRequest(http.MethodPost, "/run/"+first.ID+"/cancel", nil))
	if terminalCancel.Code != http.StatusConflict {
		t.Fatalf("terminal cancel status = %d, want 409", terminalCancel.Code)
	}
}

func TestTemplateAPIsAndTemplateBackedRun(t *testing.T) {
	handler := newHandler(t, providerFor(t, func(model string) string {
		switch model {
		case "test-model":
			return incrementTestCode
		case "code-model":
			return correctIncrementCode
		default:
			t.Fatalf("unexpected model %q", model)
			return ""
		}
	}))

	created := templateRequest(t, handler, http.MethodPost, "/api/templates", `{"id":"increment","name":"Increment","spec":"Return the input integer increased by one.","signature":"func Increment(value int) int"}`, http.StatusCreated)
	if created.ID != "increment" || created.Digest == "" {
		t.Fatalf("created template = %#v", created)
	}
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/templates", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"id":"increment"`) {
		t.Fatalf("GET /api/templates = status %d body %s", listed.Code, listed.Body.String())
	}
	loaded := templateRequest(t, handler, http.MethodGet, "/api/templates/increment", "", http.StatusOK)
	if loaded != created {
		t.Fatalf("loaded template = %#v, want %#v", loaded, created)
	}
	updated := templateRequest(t, handler, http.MethodPut, "/api/templates/increment", `{"name":"Increment safely","spec":"Return the input integer increased by one without panicking.","signature":"func Increment(value int) int"}`, http.StatusOK)
	if updated.ID != created.ID || updated.Digest == created.Digest {
		t.Fatalf("updated template = %#v, want same ID and changed digest", updated)
	}

	start := httptest.NewRecorder()
	startBody := `{"requestId":"template_run_001","coderModel":"code-model","testerModel":"test-model"}`
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/api/templates/increment/runs", strings.NewReader(startBody)))
	if start.Code != http.StatusAccepted {
		t.Fatalf("POST template run = %d: %s", start.Code, start.Body.String())
	}
	var started startResponse
	if err := json.NewDecoder(start.Body).Decode(&started); err != nil {
		t.Fatalf("decode template run: %v", err)
	}
	got := waitForRun(t, handler, started.ID)
	if got.Status != run.StatusPassed || got.Template.ID != updated.ID || got.Template.Digest != updated.Digest {
		t.Fatalf("template-backed run = %#v, want passed snapshot with template provenance", got)
	}

	replayed := httptest.NewRecorder()
	handler.ServeHTTP(replayed, httptest.NewRequest(http.MethodPost, "/api/templates/increment/runs", strings.NewReader(startBody)))
	if replayed.Code != http.StatusAccepted || !strings.Contains(replayed.Body.String(), started.ID) {
		t.Fatalf("template run replay = %d: %s", replayed.Code, replayed.Body.String())
	}
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/api/templates/missing/runs", strings.NewReader(startBody)))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown template start = %d, want 404", unknown.Code)
	}
}

func TestTemplateAPIsRejectUnsafeInputAndPagesAreExplicit(t *testing.T) {
	handler := newHandler(t, providerFor(t, func(string) string { return incrementTestCode }))
	for _, body := range []string{
		`{"id":"../escape","name":"Name","spec":"Return one.","signature":"func One() int"}`,
		`{"id":"one","name":"Name","spec":"Return one.","signature":"func One() int","testCode":"forbidden"}`,
		`{"id":"one","name":"Name","spec":"Return one.","signature":"func One() int","oracle":"authored"}`,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("POST unsafe template = %d, want 400: %s", response.Code, response.Body.String())
		}
	}
	for _, route := range []string{"/templates", "/templates/new", "/templates/increment", "/runs", "/runs/run_000001"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "innerHTML") {
			t.Fatalf("GET %s = status %d with unsafe page content", route, response.Code)
		}
		if route == "/runs/run_000001" && (!strings.Contains(response.Body.String(), "Run no longer available") || !strings.Contains(response.Body.String(), "Writing blind test oracle") || !strings.Contains(response.Body.String(), "without a completion percentage")) {
			t.Fatalf("GET %s does not provide truthful live run progress", route)
		}
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/unknown", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("GET unknown route = %d, want 404", missing.Code)
	}
}

func templateRequest(t *testing.T, handler http.Handler, method, path, body string, wantStatus int) template.Template {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
	if response.Code != wantStatus {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, response.Code, wantStatus, response.Body.String())
	}
	var value template.Template
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("decode template response: %v", err)
	}
	return value
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New(Config{}) error = nil, want validation error")
	}
}

func newHandler(t *testing.T, provider llm.ClientFactory) http.Handler {
	return newHandlerWithLogger(t, provider, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newHandlerWithLogger(t *testing.T, provider llm.ClientFactory, logger *slog.Logger) http.Handler {
	t.Helper()
	catalog, err := llm.NewModelCatalog(provider, "code-model", []string{"code-model", "test-model"})
	if err != nil {
		t.Fatalf("NewModelCatalog() error = %v", err)
	}
	resolver, err := oracle.NewResolver(oracle.Config{
		MaxAttempts:      1,
		PreflightTimeout: 10 * time.Second,
		Rulebook:         oracle.DefaultRulebook(),
		Admitter:         oracle.NewStructuralAdmitter(),
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	store, err := run.NewStore(run.Config{
		MaxAttempts: 1,
		TestTimeout: 10 * time.Second,
		RunTimeout:  time.Minute,
		Logger:      logger,
	}, resolver, repair.NewExecutor())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	templates, err := template.New(template.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("template.New() error = %v", err)
	}
	handler, err := New(Config{
		Store:         store,
		Models:        catalog,
		Draft:         draft.NewService(),
		Templates:     templates,
		ReviewerModel: "test-model",
		Defaults: ModelDefaults{
			CoderModel:  "code-model",
			TesterModel: "test-model",
		},
		Presets: []Preset{{
			Name:      "increment",
			Spec:      "Return the input integer increased by one.",
			Signature: "func Increment(value int) int",
		}},
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return handler
}

func providerFor(t *testing.T, responseForModel func(string) string) llm.ClientFactory {
	t.Helper()
	return llm.ClientFactoryFunc(func(modelID string) (llm.LLM, error) {
		return providerTestLLM{complete: func(ctx context.Context, prompt string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if strings.Contains(prompt, "Review this proposed blind Go test oracle") {
				return `{"verdict":"accept","findings":[]}`, nil
			}
			if strings.Contains(prompt, "Propose one bodyless, top-level Go function signature") {
				return "func Increment(value int) int", nil
			}
			return responseForModel(modelID), nil
		}}, nil
	})
}

type providerTestLLM struct {
	complete func(context.Context, string) (string, error)
}

func (model providerTestLLM) Complete(ctx context.Context, prompt string) (string, error) {
	return model.complete(ctx, prompt)
}

func startGeneratedRun(t *testing.T, handler http.Handler, input runRequest) startResponse {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal run request: %v", err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/run", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("POST /run status = %d, want 202; body = %s", response.Code, response.Body.String())
	}
	var started startResponse
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if started.ID == "" {
		t.Fatal("POST /run returned an empty ID")
	}
	return started
}

func waitForRun(t *testing.T, handler http.Handler, id string) run.Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/run/"+id, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET /run/%s status = %d, want 200", id, response.Code)
		}

		var got run.Run
		if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
			t.Fatalf("decode run response: %v", err)
		}
		if got.Status != run.StatusRunning {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %q did not finish within the test deadline", id)
	return run.Run{}
}

func validRunRequest() runRequest {
	return runRequest{
		RequestID:   "browser_start_001",
		Name:        "custom-increment",
		Spec:        "Return the input integer increased by one.",
		Signature:   "func Increment(value int) int",
		CoderModel:  "code-model",
		TesterModel: "test-model",
	}
}

const incrementTestCode = `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if got := Increment(2); got != 3 {
		t.Fatalf("Increment(2) = %d, want 3", got)
	}
}
`

const correctIncrementCode = `package solution

func Increment(value int) int {
	return value + 1
}
`
