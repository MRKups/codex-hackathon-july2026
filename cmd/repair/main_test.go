package main

import (
	"context"
	"testing"
	"time"

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

	final, err := repair.Repair(context.Background(), coder, splitCentsTask(), 1, 10*time.Second, nil)
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if !final.Passed {
		t.Fatalf("Repair() final attempt = %#v, want pass", final)
	}
}

type staticLLM struct {
	response string
}

func (model staticLLM) Complete(_ context.Context, _ string) (string, error) {
	return model.response, nil
}
