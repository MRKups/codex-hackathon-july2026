package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/llm/openai"
	"codex-hackathon-july2026/internal/oracle"
	"codex-hackathon-july2026/internal/repair"
)

func TestSplitCentsTaskAcceptsReferenceImplementation(t *testing.T) {
	coder := staticLLM{response: `package solution

import "errors"

func SplitCents(total, recipients int) ([]int, error) {
	if total < 0 || recipients <= 0 {
		return nil, errors.New("total and recipients must be valid")
	}

	shares := make([]int, recipients)
	base, remainder := total/recipients, total%recipients
	for index := range shares {
		shares[index] = base
		if index < remainder {
			shares[index]++
		}
	}
	return shares, nil
}
`}

	task := splitCentsTask()
	resolver, err := oracle.NewResolver(oracle.Config{
		MaxAttempts:      1,
		PreflightTimeout: 10 * time.Second,
		Rulebook:         oracle.DefaultRulebook(),
		Admitter:         oracle.NewStructuralAdmitter(),
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	resolution, err := resolver.Resolve(context.Background(), oracle.Request{Task: task}, oracle.ProgressReporter{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	final, err := repair.RepairWithConfig(
		context.Background(),
		coder,
		repair.CandidateRequest{
			Spec:      task.Spec,
			Signature: task.Signature,
			Bundle:    resolution.Bundle,
		},
		repair.Config{MaxAttempts: 1, TestTimeout: 10 * time.Second},
		repair.ProgressReporter{},
	)
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if !final.Passed {
		t.Fatalf("Repair() final attempt = %#v, want pass", final)
	}
}

func TestConfiguredModelsBuildsSafeRoleDefaults(t *testing.T) {
	t.Setenv(envModelCatalog, "fast-model,strong-model")
	t.Setenv(envModelCoder, "strong-model")
	t.Setenv(envModelTester, "fast-model")

	settings, err := configuredModels(testLLMConfig(), testFactory())
	if err != nil {
		t.Fatalf("configuredModels() error = %v", err)
	}
	if settings.coder != "strong-model" || settings.tester != "fast-model" {
		t.Fatalf("role defaults = coder %q tester %q", settings.coder, settings.tester)
	}
	want := []llm.ModelOption{{ID: "fast-model"}, {ID: "strong-model"}}
	if got := settings.catalog.Options(); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog options = %#v, want %#v", got, want)
	}
}

func TestConfiguredModelsFallsBackAndRejectsMissingRoleDefault(t *testing.T) {
	t.Setenv(envModelCatalog, "")
	t.Setenv(envModelCoder, "")
	t.Setenv(envModelTester, "")

	settings, err := configuredModels(testLLMConfig(), testFactory())
	if err != nil {
		t.Fatalf("configuredModels() fallback error = %v", err)
	}
	if settings.coder != "default-model" || settings.tester != "default-model" {
		t.Fatalf("fallback roles = coder %q tester %q", settings.coder, settings.tester)
	}
	if got := settings.catalog.Options(); !reflect.DeepEqual(got, []llm.ModelOption{{ID: "default-model"}}) {
		t.Fatalf("fallback catalog = %#v", got)
	}

	t.Setenv(envModelCatalog, "default-model")
	t.Setenv(envModelCoder, "other-model")
	if _, err := configuredModels(testLLMConfig(), testFactory()); err == nil {
		t.Fatal("configuredModels() error = nil, want missing role default error")
	}
}

func TestUniqueModels(t *testing.T) {
	if got, want := uniqueModels("one", "one", "two", "one"), []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueModels() = %#v, want %#v", got, want)
	}
}

func testLLMConfig() llm.Config {
	return llm.Config{
		Provider: openai.ProviderID,
		Model:    "default-model",
		Timeout:  time.Second,
	}
}

func testFactory() llm.ClientFactory {
	return llm.ClientFactoryFunc(func(modelID string) (llm.LLM, error) {
		return staticLLM{response: modelID}, nil
	})
}

func TestConfiguredClientFactoryRejectsUnknownProvider(t *testing.T) {
	_, err := configuredClientFactory(llm.Config{Provider: "unknown", Model: "model", Timeout: time.Second})
	if err == nil {
		t.Fatal("configuredClientFactory() error = nil, want unsupported provider error")
	}
}

type staticLLM struct {
	response string
}

func (model staticLLM) Complete(_ context.Context, _ string) (string, error) {
	return model.response, nil
}
