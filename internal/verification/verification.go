// Package verification owns immutable, inspectable verification bundles.
package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex-hackathon-july2026/internal/domain"
)

// BundleVersionV1 is the first stable manifest schema. A bundle's digest covers this version so
// a later schema cannot silently change the meaning of a recorded run.
const BundleVersionV1 = "verification-bundle/v1"

// AuthoredSource seals trusted caller-owned test source. It records provenance, but does not
// infer semantic quality or a case count from arbitrary Go source.
func AuthoredSource(task domain.Task, testCode string) (domain.VerificationBundle, error) {
	return seal(task, domain.VerificationManifest{
		Version: BundleVersionV1,
		Origin:  domain.VerificationOriginAuthored,
	}, testCode)
}

// GeneratedSource seals an accepted blind test-writer result. Structural admission happens in
// oracle before this function is called; this bundle makes no semantic-correctness claim.
func GeneratedSource(task domain.Task, testCode string) (domain.VerificationBundle, error) {
	return seal(task, domain.VerificationManifest{
		Version: BundleVersionV1,
		Origin:  domain.VerificationOriginGenerated,
	}, testCode)
}

// ValidateBundle proves that a bundle is internally consistent, bound to task, and has not
// drifted from its digest. It does not claim that the executable semantics are universally true.
func ValidateBundle(task domain.Task, bundle domain.VerificationBundle) error {
	if strings.TrimSpace(bundle.TestCode) == "" {
		return errors.New("verification test source is empty")
	}
	manifest := bundle.Manifest
	if manifest.Version != BundleVersionV1 {
		return fmt.Errorf("unsupported verification bundle version %q", manifest.Version)
	}
	if manifest.TaskDigest != taskDigest(task) {
		return errors.New("verification bundle task digest does not match task")
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Digest) == "" {
		return errors.New("verification bundle digest is required")
	}
	if manifest.Digest != bundleDigest(manifest, bundle.TestCode) {
		return errors.New("verification bundle digest does not match source or manifest")
	}
	return nil
}

func seal(task domain.Task, manifest domain.VerificationManifest, testCode string) (domain.VerificationBundle, error) {
	if strings.TrimSpace(testCode) == "" {
		return domain.VerificationBundle{}, errors.New("verification test source is empty")
	}
	manifest.TaskDigest = taskDigest(task)
	manifest.Digest = ""
	if err := validateManifest(manifest); err != nil {
		return domain.VerificationBundle{}, err
	}
	manifest.Digest = bundleDigest(manifest, testCode)
	bundle := domain.VerificationBundle{Manifest: manifest, TestCode: testCode}
	if err := ValidateBundle(task, bundle); err != nil {
		return domain.VerificationBundle{}, err
	}
	return bundle, nil
}

func validateManifest(manifest domain.VerificationManifest) error {
	if strings.TrimSpace(manifest.Version) == "" {
		return errors.New("verification bundle version is required")
	}
	if strings.TrimSpace(manifest.TaskDigest) == "" {
		return errors.New("verification bundle task digest is required")
	}
	switch manifest.Origin {
	case domain.VerificationOriginAuthored, domain.VerificationOriginGenerated:
		return nil
	default:
		return fmt.Errorf("unknown verification bundle origin %q", manifest.Origin)
	}
}

func taskDigest(task domain.Task) string {
	payload := struct {
		Spec      string `json:"spec"`
		Signature string `json:"signature"`
	}{
		Spec:      strings.TrimSpace(task.Spec),
		Signature: strings.TrimSpace(task.Signature),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("marshal verification task digest: %v", err))
	}
	return hash(encoded)
}

func bundleDigest(manifest domain.VerificationManifest, testCode string) string {
	manifest.Digest = ""
	payload := struct {
		Manifest domain.VerificationManifest `json:"manifest"`
		TestCode string                      `json:"testCode"`
	}{
		Manifest: manifest,
		TestCode: testCode,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("marshal verification bundle digest: %v", err))
	}
	return hash(encoded)
}

func hash(input []byte) string {
	sum := sha256.Sum256(input)
	return hex.EncodeToString(sum[:])
}
