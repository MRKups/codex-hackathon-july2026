package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestParseModelIDs(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    []string
		wantErr error
	}{
		{
			name:  "empty uses the caller default",
			value: "  ",
		},
		{
			name:  "trims configured IDs",
			value: " model-a,model-b , model-c ",
			want:  []string{"model-a", "model-b", "model-c"},
		},
		{
			name:    "rejects an empty entry",
			value:   "model-a,,model-b",
			wantErr: ErrEmptyModelID,
		},
		{
			name:    "rejects a duplicate entry",
			value:   "model-a, model-a",
			wantErr: ErrDuplicateModelID,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseModelIDs(test.value)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseModelIDs(%q) error = %v, want errors.Is(_, %v)", test.value, err, test.wantErr)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("ParseModelIDs(%q) = %#v, want %#v", test.value, got, test.want)
			}
		})
	}
}

func TestModelCatalogResolvesOnlyConfiguredReusableClients(t *testing.T) {
	var receivedModels []string
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var completion completionRequest
		if err := json.NewDecoder(request.Body).Decode(&completion); err != nil {
			t.Errorf("decode completion request: %v", err)
			return
		}
		receivedModels = append(receivedModels, completion.Model)

		response.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(response).Encode(completionResponse{
			Choices: []completionChoice{{
				Message: chatMessage{Role: "assistant", Content: "package solution"},
			}},
		}); err != nil {
			t.Errorf("encode completion response: %v", err)
		}
	}))
	defer provider.Close()

	catalog, err := NewModelCatalog(Config{
		BaseURL: provider.URL,
		APIKey:  "test-key",
		Model:   "default-model",
		Timeout: time.Second,
	}, []string{" model-one ", "model-two"})
	if err != nil {
		t.Fatalf("NewModelCatalog() error = %v", err)
	}

	wantOptions := []ModelOption{{ID: "model-one"}, {ID: "model-two"}}
	if got := catalog.Options(); !reflect.DeepEqual(got, wantOptions) {
		t.Errorf("Options() = %#v, want %#v", got, wantOptions)
	}

	options := catalog.Options()
	options[0].ID = "changed-by-caller"
	if got := catalog.Options(); !reflect.DeepEqual(got, wantOptions) {
		t.Errorf("Options() after caller mutation = %#v, want %#v", got, wantOptions)
	}

	first, err := catalog.Resolve(" model-one ")
	if err != nil {
		t.Fatalf("Resolve(model-one) error = %v", err)
	}
	firstAgain, err := catalog.Resolve("model-one")
	if err != nil {
		t.Fatalf("Resolve(model-one) second call error = %v", err)
	}
	if first != firstAgain {
		t.Error("Resolve(model-one) returned different clients for the same configured model")
	}

	second, err := catalog.Resolve("model-two")
	if err != nil {
		t.Fatalf("Resolve(model-two) error = %v", err)
	}
	if _, err := first.Complete(context.Background(), "first prompt"); err != nil {
		t.Fatalf("first.Complete() error = %v", err)
	}
	if _, err := second.Complete(context.Background(), "second prompt"); err != nil {
		t.Fatalf("second.Complete() error = %v", err)
	}

	if want := []string{"model-one", "model-two"}; !reflect.DeepEqual(receivedModels, want) {
		t.Errorf("provider request models = %#v, want %#v", receivedModels, want)
	}

	if _, err := catalog.Resolve(""); !errors.Is(err, ErrEmptyModelID) {
		t.Errorf("Resolve(empty) error = %v, want errors.Is(_, ErrEmptyModelID)", err)
	}
	if _, err := catalog.Resolve("not-configured"); !errors.Is(err, ErrUnknownModelID) {
		t.Errorf("Resolve(not-configured) error = %v, want errors.Is(_, ErrUnknownModelID)", err)
	}
}

func TestNewModelCatalogUsesBaseModelWhenAllowlistIsEmpty(t *testing.T) {
	catalog, err := NewModelCatalog(Config{
		BaseURL: "https://llm.example/v1",
		APIKey:  "test-key",
		Model:   "default-model",
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("NewModelCatalog() error = %v", err)
	}

	want := []ModelOption{{ID: "default-model"}}
	if got := catalog.Options(); !reflect.DeepEqual(got, want) {
		t.Errorf("Options() = %#v, want %#v", got, want)
	}
	if _, err := catalog.Resolve("default-model"); err != nil {
		t.Errorf("Resolve(default-model) error = %v", err)
	}
}

func TestNewModelCatalogRejectsInvalidAllowlist(t *testing.T) {
	_, err := NewModelCatalog(Config{
		BaseURL: "https://llm.example/v1",
		APIKey:  "test-key",
		Model:   "default-model",
		Timeout: time.Second,
	}, []string{"model-a", ""})
	if !errors.Is(err, ErrEmptyModelID) {
		t.Fatalf("NewModelCatalog() error = %v, want errors.Is(_, ErrEmptyModelID)", err)
	}
}
