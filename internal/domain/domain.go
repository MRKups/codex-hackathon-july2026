// Package domain defines data shared by the repair loop and its callers.
package domain

// OracleMode identifies where a run's frozen test oracle came from.
//
// The zero value remains compatible with the original authored-only task shape. Callers that
// create a generated-oracle task must set OracleGenerated explicitly.
type OracleMode string

const (
	OracleAuthored  OracleMode = "authored"
	OracleGenerated OracleMode = "generated"
)

// Task is a human-owned programming task. TestCode is a frozen verifier input: authored tasks
// provide it up front, while generated tasks receive it from the blind test-writer before the
// first coder call. It must never be included in a prompt sent to the coder.
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
