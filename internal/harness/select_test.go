// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package harness

import (
	"testing"

	"github.com/codeactual/evolve/internal/model"
)

// stubHarness is a minimal harness identified only by id; Select/RunnableHarness
// never invoke its command methods.
type stubHarness struct{ id string }

func (s stubHarness) ID() string                                   { return s.id }
func (stubHarness) Name() string                                   { return "Stub" }
func (stubHarness) CLI() []string                                  { return []string{"sh"} }
func (stubHarness) EnvKeys() []string                              { return nil }
func (stubHarness) SkillDirs() []string                            { return nil }
func (stubHarness) ScanLine([]byte, string, string) (bool, string) { return false, "" }
func (stubHarness) TriggerSpec(ws, _, _ string, _ bool) model.CommandSpec {
	return model.CommandSpec{Dir: ws}
}

func sonnet() model.Model {
	return model.Model{
		ID: "anthropic/claude-sonnet-4-6", ProviderID: "anthropic", Name: "Claude Sonnet 4.6",
		Supported: map[string]string{"claude": "claude-sonnet-4-6"},
		Preferred: "claude",
	}
}

// dual is a synthetic model both built-in harnesses can drive, preferring Claude.
func dual() model.Model {
	return model.Model{
		ID: "anthropic/dual", ProviderID: "anthropic", Name: "Dual",
		Supported: map[string]string{"claude": "dual", "codex": "dual"},
		Preferred: "claude",
	}
}

func TestRunnableHarness(t *testing.T) {
	m := dual()
	tests := []struct {
		name     string
		eligible map[string]bool
		want     string
		ok       bool
	}{
		{"preferred wins", map[string]bool{"claude": true, "codex": true}, "claude", true},
		{"fallback to other supported", map[string]bool{"codex": true}, "codex", true},
		{"none eligible", map[string]bool{"unknown": true}, "", false},
		{"empty", map[string]bool{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := RunnableHarness(m, tt.eligible)
			if got != tt.want || ok != tt.ok {
				t.Errorf("RunnableHarness = (%q,%v), want (%q,%v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	models := []model.Model{
		sonnet(),
		{ID: "openai/gpt-5.4", ProviderID: "openai", Supported: map[string]string{"codex": "gpt-5.4"}, Preferred: "codex"},
		{ID: "unknown/x", ProviderID: "unknown", Supported: map[string]string{"other": "x"}, Preferred: "other"},
	}
	claude := stubHarness{"claude"}
	codex := stubHarness{"codex"}

	t.Run("all binds runnable models to a harness", func(t *testing.T) {
		got, err := Select("all", models, []Harness{claude, codex})
		if err != nil {
			t.Fatal(err)
		}
		// unknown/x has no eligible harness, so it is skipped.
		if len(got) != 2 {
			t.Fatalf("Select(all) = %d, want 2 (unknown/x dropped, no harness)", len(got))
		}
		if got[0].Key() != "anthropic/claude-sonnet-4-6" || got[0].Harness.ID() != "claude" {
			t.Errorf("first = %s via %s", got[0].Key(), got[0].Harness.ID())
		}
	})

	t.Run("provider id token", func(t *testing.T) {
		got, err := Select("anthropic", models, []Harness{claude})
		if err != nil || len(got) != 1 || got[0].Key() != "anthropic/claude-sonnet-4-6" {
			t.Fatalf("Select(anthropic) = %v err=%v", got, err)
		}
	})

	t.Run("canonical and bare ids", func(t *testing.T) {
		got, err := Select("anthropic/claude-sonnet-4-6,gpt-5.4", models, []Harness{claude, codex})
		if err != nil || len(got) != 2 {
			t.Fatalf("Select(canonical,bare) = %v err=%v", got, err)
		}
	})

	t.Run("unknown token errors", func(t *testing.T) {
		if _, err := Select("nope", models, []Harness{claude}); err == nil {
			t.Error("Select(nope) = nil error, want unknown")
		}
	})

	t.Run("matched but no eligible harness is dropped not errored", func(t *testing.T) {
		got, err := Select("anthropic", models, []Harness{codex}) // sonnet needs claude
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("Select = %v, want empty (no eligible harness)", got)
		}
	})
}
