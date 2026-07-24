package llm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
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
	factory := &recordingFactory{}
	catalog, err := NewModelCatalog(factory, "default-model", []string{" model-one ", "model-two"})
	if err != nil {
		t.Fatalf("NewModelCatalog() error = %v", err)
	}

	wantOptions := []ModelOption{{ID: "model-one"}, {ID: "model-two"}}
	if got := catalog.Options(); !reflect.DeepEqual(got, wantOptions) {
		t.Errorf("Options() = %#v, want %#v", got, wantOptions)
	}
	if want := []string{"model-one", "model-two"}; !reflect.DeepEqual(factory.models, want) {
		t.Errorf("factory model IDs = %#v, want %#v", factory.models, want)
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

	if _, err := catalog.Resolve("model-two"); err != nil {
		t.Fatalf("Resolve(model-two) error = %v", err)
	}
	if _, err := first.Complete(context.Background(), "first prompt"); err != nil {
		t.Fatalf("first.Complete() error = %v", err)
	}

	if _, err := catalog.Resolve(""); !errors.Is(err, ErrEmptyModelID) {
		t.Errorf("Resolve(empty) error = %v, want errors.Is(_, ErrEmptyModelID)", err)
	}
	if _, err := catalog.Resolve("not-configured"); !errors.Is(err, ErrUnknownModelID) {
		t.Errorf("Resolve(not-configured) error = %v, want errors.Is(_, ErrUnknownModelID)", err)
	}
}

func TestNewModelCatalogUsesDefaultModelWhenAllowlistIsEmpty(t *testing.T) {
	factory := &recordingFactory{}
	catalog, err := NewModelCatalog(factory, "default-model", nil)
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

func TestNewModelCatalogRejectsInvalidInput(t *testing.T) {
	factory := &recordingFactory{}
	if _, err := NewModelCatalog(nil, "default-model", nil); err == nil {
		t.Fatal("NewModelCatalog(nil, ...) error = nil, want factory error")
	}
	if _, err := NewModelCatalog(factory, "", nil); !errors.Is(err, ErrEmptyModelID) {
		t.Fatalf("NewModelCatalog(empty default) error = %v, want errors.Is(_, %v)", err, ErrEmptyModelID)
	}
	if _, err := NewModelCatalog(factory, "default-model", []string{"model-a", ""}); !errors.Is(err, ErrEmptyModelID) {
		t.Fatalf("NewModelCatalog() error = %v, want errors.Is(_, %v)", err, ErrEmptyModelID)
	}
}

func TestNewModelCatalogPropagatesFactoryFailure(t *testing.T) {
	wantErr := errors.New("provider setup failed")
	_, err := NewModelCatalog(ClientFactoryFunc(func(string) (LLM, error) {
		return nil, wantErr
	}), "default-model", nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("NewModelCatalog() error = %v, want errors.Is(_, %v)", err, wantErr)
	}
}

type recordingFactory struct {
	clients map[string]*catalogTestLLM
	models  []string
}

func (factory *recordingFactory) New(modelID string) (LLM, error) {
	factory.models = append(factory.models, modelID)
	if factory.clients == nil {
		factory.clients = make(map[string]*catalogTestLLM)
	}
	client, found := factory.clients[modelID]
	if !found {
		client = &catalogTestLLM{modelID: modelID}
		factory.clients[modelID] = client
	}
	return client, nil
}

type catalogTestLLM struct {
	modelID string
}

func (model *catalogTestLLM) Complete(_ context.Context, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("prompt must not be empty")
	}
	return model.modelID, nil
}
