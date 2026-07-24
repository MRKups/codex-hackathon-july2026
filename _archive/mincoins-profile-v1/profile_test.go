package mincoins

import (
	"reflect"
	"strings"
	"testing"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/verification"
)

func TestProfileFreezesHundredsOfDeterministicCases(t *testing.T) {
	registry, err := verification.NewRegistry(
		verification.FreezeConfig{Seed: 99, CaseCount: minimumCases},
		Profile{},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	task := minCoinsTask()
	first, err := registry.Freeze(task)
	if err != nil {
		t.Fatalf("Freeze() first error = %v", err)
	}
	second, err := registry.Freeze(task)
	if err != nil {
		t.Fatalf("Freeze() second error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same profile/task/seed did not produce an identical bundle")
	}
	if first.Manifest.Profile != ID || first.Manifest.Plan.Seed != 99 || first.Manifest.Plan.CaseCount != minimumCases {
		t.Fatalf("manifest = %#v", first.Manifest)
	}
	if len(first.Manifest.Checks) != 3 {
		t.Fatalf("checks = %#v, want three declared check families", first.Manifest.Checks)
	}
	for _, check := range first.Manifest.Checks {
		if check.Cases != fixedCaseCount+minimumCases {
			t.Fatalf("check %q cases = %d, want %d", check.ID, check.Cases, fixedCaseCount+minimumCases)
		}
	}
	if !strings.Contains(first.TestCode, "const minCoinsProfileGeneratedCases = 200") {
		t.Fatalf("frozen source did not contain generated case budget:\n%s", first.TestCode)
	}
	if strings.Contains(first.TestCode, "[]int{10, 7, 4}") {
		t.Fatal("profile source froze the historical impossible output as an answer key")
	}
	if err := verification.ValidateBundle(task, first); err != nil {
		t.Fatalf("ValidateBundle() error = %v", err)
	}
}

func TestProfileRejectsWrongContractOrUnsafePlan(t *testing.T) {
	profile := Profile{}
	wrong := minCoinsTask()
	wrong.Signature = "func Other() int"
	plan := domain.VerificationPlan{Seed: 1, CaseCount: minimumCases, Families: append([]string(nil), planFamilies...)}
	if _, err := profile.Compile(wrong, plan); err == nil || !strings.Contains(err.Error(), "requires signature") {
		t.Fatalf("Compile(wrong signature) error = %v", err)
	}
	wrong = minCoinsTask()
	wrong.Spec = "Return any coin selection."
	if _, err := profile.Compile(wrong, plan); err == nil || !strings.Contains(err.Error(), "reviewed MinCoins specification") {
		t.Fatalf("Compile(wrong spec) error = %v", err)
	}
	plan.CaseCount = minimumCases - 1
	if _, err := profile.Compile(minCoinsTask(), plan); err == nil || !strings.Contains(err.Error(), "case count") {
		t.Fatalf("Compile(small budget) error = %v", err)
	}
	plan.CaseCount = minimumCases
	plan.Families = []string{"reachable"}
	if _, err := profile.Compile(minCoinsTask(), plan); err == nil || !strings.Contains(err.Error(), "required input families") {
		t.Fatalf("Compile(partial families) error = %v", err)
	}
}

func TestRegistryRejectsAnIncompatibleTaskBeforeProfileCompilation(t *testing.T) {
	registry, err := verification.NewRegistry(
		verification.FreezeConfig{Seed: 1, CaseCount: minimumCases},
		Profile{},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	task := minCoinsTask()
	task.Spec = "Return a coin selection with any ordering."
	if _, err := registry.Freeze(task); err == nil || !strings.Contains(err.Error(), "does not accept task") {
		t.Fatalf("Freeze(incompatible task) error = %v, want task-contract rejection", err)
	}
}

func minCoinsTask() domain.Task {
	return domain.Task{
		Name:                "minimum-coin-change",
		Spec:                Specification,
		Signature:           signature,
		Oracle:              domain.OracleAuthored,
		VerificationProfile: ID,
	}
}
