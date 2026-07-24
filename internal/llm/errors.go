package llm

import "fmt"

// HTTPStatusError is a provider error normalized to its safe HTTP status. It deliberately
// contains no provider response body, request headers, or credentials because callers may
// surface its Error string in a run record.
type HTTPStatusError struct {
	StatusCode int
}

func (err *HTTPStatusError) Error() string {
	return fmt.Sprintf("completion request returned HTTP %d", err.StatusCode)
}
