// Command repair runs the authored-oracle terminal repair-loop demo.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/repair"
	"codex-hackathon-july2026/internal/run"
	"codex-hackathon-july2026/internal/server"
)

func main() {
	var address string
	var maxAttempts int
	var runTimeout time.Duration
	var serve bool
	var verifierTimeout time.Duration
	flag.StringVar(&address, "addr", "127.0.0.1:8080", "address for the browser demo server")
	flag.IntVar(&maxAttempts, "attempts", 3, "maximum number of coder attempts")
	flag.DurationVar(&runTimeout, "run-timeout", 90*time.Second, "maximum duration of one browser repair run")
	flag.BoolVar(&serve, "serve", false, "serve the browser demo instead of running once in the terminal")
	flag.DurationVar(&verifierTimeout, "verifier-timeout", 10*time.Second, "timeout for one candidate verification")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "usage: %s [-serve] [-addr ADDRESS] [-attempts N] [-run-timeout DURATION] [-verifier-timeout DURATION]\n", os.Args[0])
		os.Exit(2)
	}
	if maxAttempts <= 0 {
		exitFailure("configuration error", fmt.Errorf("attempts must be greater than zero"))
	}
	if verifierTimeout <= 0 {
		exitFailure("configuration error", fmt.Errorf("verifier timeout must be greater than zero"))
	}
	if runTimeout <= 0 {
		exitFailure("configuration error", fmt.Errorf("run timeout must be greater than zero"))
	}
	if strings.TrimSpace(address) == "" {
		exitFailure("configuration error", fmt.Errorf("server address must not be empty"))
	}

	config, err := llm.ConfigFromEnv()
	if err != nil {
		exitFailure("configuration error", err)
	}
	coder, err := llm.NewClient(config)
	if err != nil {
		exitFailure("configuration error", err)
	}
	if serve {
		serveBrowser(address, coder, splitCentsTask(), maxAttempts, verifierTimeout, runTimeout)
		return
	}

	final, err := repair.Repair(
		context.Background(),
		coder,
		splitCentsTask(),
		maxAttempts,
		verifierTimeout,
		printAttempt,
	)
	if err != nil {
		exitFailure("provider or verifier infrastructure failure", err)
	}
	if final.Passed {
		fmt.Printf("passed after %d attempt(s)\n", final.N)
		return
	}

	fmt.Printf("gave up after %d attempt(s)\n", final.N)
}

func serveBrowser(address string, coder llm.LLM, task domain.Task, maxAttempts int, verifierTimeout, runTimeout time.Duration) {
	store, err := run.NewStore(coder, maxAttempts, verifierTimeout, runTimeout)
	if err != nil {
		exitFailure("configuration error", err)
	}
	handler, err := server.New(store, task)
	if err != nil {
		exitFailure("server configuration error", err)
	}

	fmt.Printf("Repair Loop browser demo: http://%s\n", address)
	if err := http.ListenAndServe(address, handler); err != nil {
		exitFailure("browser server failure", err)
	}
}

func exitFailure(kind string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", kind, err)
	os.Exit(1)
}

func printAttempt(attempt domain.Attempt) error {
	status := "failed"
	if attempt.Passed {
		status = "passed"
	}
	fmt.Printf("attempt %d: %s\n", attempt.N, status)
	if attempt.Output == "" {
		fmt.Println("verifier output: (none)")
		return nil
	}

	fmt.Println("verifier output:")
	fmt.Print(attempt.Output)
	if !strings.HasSuffix(attempt.Output, "\n") {
		fmt.Println()
	}
	return nil
}

func splitCentsTask() domain.Task {
	return domain.Task{
		Name: "split-cents",
		Spec: `Implement SplitCents(total, recipients int) ([]int, error).

Split a non-negative number of cents among a positive number of recipients. Return a slice
whose length is exactly recipients. Every recipient gets total / recipients cents, and the
remaining total % recipients cents go one at a time to the earliest recipients (lower index).

For total < 0 or recipients <= 0, return a nil slice and a non-nil error. Do not panic.`,
		Signature: "func SplitCents(total, recipients int) ([]int, error)",
		TestCode: `package solution

import (
	"reflect"
	"testing"
)

func TestSplitCentsDistributesRemainderToEarliestRecipients(t *testing.T) {
	tests := []struct {
		name       string
		total      int
		recipients int
		want       []int
	}{
		{name: "even split", total: 12, recipients: 3, want: []int{4, 4, 4}},
		{name: "remainder goes to earliest recipients", total: 100, recipients: 3, want: []int{34, 33, 33}},
		{name: "more recipients than cents", total: 2, recipients: 5, want: []int{1, 1, 0, 0, 0}},
		{name: "zero total", total: 0, recipients: 3, want: []int{0, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitCents(tt.total, tt.recipients)
			if err != nil {
				t.Fatalf("SplitCents(%d, %d) error = %v", tt.total, tt.recipients, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SplitCents(%d, %d) = %v, want %v", tt.total, tt.recipients, got, tt.want)
			}
		})
	}
}

func TestSplitCentsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name       string
		total      int
		recipients int
	}{
		{name: "negative total", total: -1, recipients: 2},
		{name: "zero recipients", total: 1, recipients: 0},
		{name: "negative recipients", total: 1, recipients: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitCents(tt.total, tt.recipients)
			if err == nil {
				t.Fatalf("SplitCents(%d, %d) error = nil, want non-nil", tt.total, tt.recipients)
			}
			if got != nil {
				t.Fatalf("SplitCents(%d, %d) result = %v, want nil", tt.total, tt.recipients, got)
			}
		})
	}
}
`,
	}
}
