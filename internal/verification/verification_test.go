package verification

import (
	"strings"
	"testing"

	"codex-hackathon-july2026/internal/domain"
)

func TestSourceBundlesSealDeterministically(t *testing.T) {
	task := domain.Task{
		Spec:      "Return the input.",
		Signature: "func Echo(value int) int",
	}
	testCode := "package solution\n\nimport \"testing\"\n\nfunc TestEcho(t *testing.T) {}\n"

	authored, err := AuthoredSource(task, testCode)
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}
	repeated, err := AuthoredSource(task, testCode)
	if err != nil {
		t.Fatalf("AuthoredSource() repeated error = %v", err)
	}
	if authored != repeated {
		t.Fatalf("same authored task/source yielded different bundles:\nfirst=%#v\nsecond=%#v", authored, repeated)
	}
	if authored.Manifest.Origin != domain.VerificationOriginAuthored {
		t.Fatalf("authored origin = %q, want authored", authored.Manifest.Origin)
	}
	if len(authored.Manifest.Digest) != 64 {
		t.Fatalf("bundle digest = %q, want SHA-256 hex", authored.Manifest.Digest)
	}
	if err := ValidateBundle(task, authored); err != nil {
		t.Fatalf("ValidateBundle(authored) error = %v", err)
	}

	generated, err := GeneratedSource(task, testCode)
	if err != nil {
		t.Fatalf("GeneratedSource() error = %v", err)
	}
	if generated.Manifest.Origin != domain.VerificationOriginGenerated {
		t.Fatalf("generated origin = %q, want generated", generated.Manifest.Origin)
	}
	if generated.Manifest.Digest == authored.Manifest.Digest {
		t.Fatal("different origins yielded the same bundle digest")
	}
	if err := ValidateBundle(task, generated); err != nil {
		t.Fatalf("ValidateBundle(generated) error = %v", err)
	}
}

func TestValidateBundleRejectsTaskAndSourceDrift(t *testing.T) {
	task := domain.Task{Spec: "Return one.", Signature: "func One() int"}
	bundle, err := GeneratedSource(task, "package solution\n")
	if err != nil {
		t.Fatalf("GeneratedSource() error = %v", err)
	}

	changedTask := task
	changedTask.Spec = "Return two."
	if err := ValidateBundle(changedTask, bundle); err == nil || !strings.Contains(err.Error(), "task digest") {
		t.Fatalf("ValidateBundle(changed task) error = %v, want task digest mismatch", err)
	}

	tamperedSource := bundle
	tamperedSource.TestCode += "// drift\n"
	if err := ValidateBundle(task, tamperedSource); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("ValidateBundle(tampered source) error = %v, want digest mismatch", err)
	}

	tamperedManifest := bundle
	tamperedManifest.Manifest.Origin = domain.VerificationOriginAuthored
	if err := ValidateBundle(task, tamperedManifest); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("ValidateBundle(tampered manifest) error = %v, want digest mismatch", err)
	}
}

func TestValidateBundleEnforcesTheTwoOriginContract(t *testing.T) {
	task := domain.Task{Spec: "Return one.", Signature: "func One() int"}
	bundle, err := AuthoredSource(task, "package solution\n")
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}
	bundle.Manifest.Origin = domain.VerificationOrigin("other")
	if err := ValidateBundle(task, bundle); err == nil || !strings.Contains(err.Error(), "unknown verification bundle origin") {
		t.Fatalf("ValidateBundle(third origin) error = %v, want origin rejection", err)
	}
}

func TestSourceSealingRejectsEmptyTestCode(t *testing.T) {
	task := domain.Task{Spec: "Return one.", Signature: "func One() int"}
	if _, err := AuthoredSource(task, " \n\t"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("AuthoredSource(empty) error = %v, want empty-source rejection", err)
	}
	if _, err := GeneratedSource(task, ""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("GeneratedSource(empty) error = %v, want empty-source rejection", err)
	}
}

func TestInspectTestSourceListsOnlyTopLevelTestFunctions(t *testing.T) {
	inventory := InspectTestSource(`package solution

import "testing"

func TestFirst(t *testing.T) {}
func TestSecond(t *testing.T) {}
func helper(t *testing.T) {}
func (thing) TestMethod(t *testing.T) {}
`)
	if got, want := strings.Join(inventory.TopLevelTests, ","), "TestFirst,TestSecond"; got != want {
		t.Fatalf("top-level tests = %q, want %q", got, want)
	}

	broken := InspectTestSource("package solution\nfunc TestBroken(")
	if len(broken.TopLevelTests) != 0 {
		t.Fatalf("broken source inventory = %#v, want empty", broken)
	}
}
