// Package domain defines data shared by the repair loop and its callers.
package domain

// OracleMode identifies where a run's frozen test oracle came from.
//
// The zero value remains compatible with the original authored-only task shape. Callers that
// create generated tasks must set their mode explicitly.
type OracleMode string

const (
	OracleAuthored  OracleMode = "authored"
	OracleGenerated OracleMode = "generated"
)

// VerificationOrigin identifies the legal source of a frozen verification bundle.
type VerificationOrigin string

const (
	VerificationOriginAuthored  VerificationOrigin = "authored"
	VerificationOriginGenerated VerificationOrigin = "generated"
)

// VerificationManifest is the inspectable, immutable description of a frozen bundle. TaskDigest
// binds the bundle to the submitted spec/signature; Digest binds the complete manifest and exact
// executable source.
type VerificationManifest struct {
	Version    string             `json:"version"`
	Origin     VerificationOrigin `json:"origin"`
	TaskDigest string             `json:"taskDigest"`
	Digest     string             `json:"digest"`
}

// VerificationBundle is the one artifact that judges every candidate attempt in a run. TestCode
// is never passed to a coder prompt. It is retained separately in Run JSON for readable display.
type VerificationBundle struct {
	Manifest VerificationManifest
	TestCode string
}

// Clone returns a copy suitable for a run snapshot or callback boundary.
func (bundle VerificationBundle) Clone() VerificationBundle {
	return bundle
}

// Task is a human-owned programming task. TestCode is trusted authored verifier input. Generated
// tasks start without it; their accepted blind source exists only in a VerificationBundle before
// the first coder call. Neither form may be included in a prompt sent to the coder.
type Task struct {
	Name      string
	Spec      string
	Signature string
	Oracle    OracleMode
	TestCode  string
}

// Attempt records one generated candidate and the result of verifying it.
// Output is empty on success; otherwise it is failed-stage output or a stable verifier-timeout note.
type Attempt struct {
	N      int    `json:"n"`
	Code   string `json:"code"`
	Passed bool   `json:"passed"`
	Output string `json:"output"`
}
