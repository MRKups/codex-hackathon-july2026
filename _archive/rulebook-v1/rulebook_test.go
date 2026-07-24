package rulebook

import (
	"strings"
	"testing"

	"codex-hackathon-july2026/internal/domain"
)

func TestCompileMinCoinsV1(t *testing.T) {
	source, err := Compile(domain.Task{
		Oracle:    domain.OracleRulebook,
		Rulebook:  domain.RulebookMinCoinsV1,
		Signature: minCoinsSignature,
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if source.ID != domain.RulebookMinCoinsV1 {
		t.Fatalf("source ID = %q, want %q", source.ID, domain.RulebookMinCoinsV1)
	}
	if source.EvidenceTier != EvidenceTierReferenceBacked {
		t.Fatalf("source evidence tier = %q, want %q", source.EvidenceTier, EvidenceTierReferenceBacked)
	}
	for _, want := range []string{
		"Rulebook: mincoins/v1",
		"Evidence tier: reference-backed",
		"package solution",
		"TestMinCoinsRulebookChecksOutputs",
		"checkMinCoinsRulebookValidity",
		"minCoinsRulebookReference",
	} {
		if !strings.Contains(source.TestCode, want) {
			t.Fatalf("rulebook test source does not contain %q", want)
		}
	}
	for _, forbidden := range []string{"[]int{10, 7, 4}", "want []int"} {
		if strings.Contains(source.TestCode, forbidden) {
			t.Fatalf("rulebook test source contains hand-calculated output literal %q", forbidden)
		}
	}
}

func TestCompileRejectsUnsupportedTasks(t *testing.T) {
	tests := []struct {
		name string
		task domain.Task
	}{
		{
			name: "wrong mode",
			task: domain.Task{
				Oracle:    domain.OracleGenerated,
				Rulebook:  domain.RulebookMinCoinsV1,
				Signature: minCoinsSignature,
			},
		},
		{
			name: "missing ID",
			task: domain.Task{
				Oracle:    domain.OracleRulebook,
				Signature: minCoinsSignature,
			},
		},
		{
			name: "unknown ID",
			task: domain.Task{
				Oracle:    domain.OracleRulebook,
				Rulebook:  domain.RulebookID("other/v1"),
				Signature: minCoinsSignature,
			},
		},
		{
			name: "wrong signature",
			task: domain.Task{
				Oracle:    domain.OracleRulebook,
				Rulebook:  domain.RulebookMinCoinsV1,
				Signature: "func MinCoins(amount int) ([]int, error)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Compile(tt.task); err == nil {
				t.Fatal("Compile() error = nil, want rejection")
			}
		})
	}
}
