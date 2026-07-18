// Package domain defines data shared by the repair loop and its callers.
package domain

// Task is a human-owned programming task. TestCode is the fixed, human-authored verifier
// input and must never be included in a prompt sent to an LLM.
type Task struct {
	Name      string
	Spec      string
	Signature string
	TestCode  string
}

// Attempt records one generated candidate and the result of verifying it.
// Output is empty on success; otherwise it is failed-stage output or a stable verifier-timeout note.
type Attempt struct {
	N      int
	Code   string
	Passed bool
	Output string
}
