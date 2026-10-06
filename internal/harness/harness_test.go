// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package harness

import (
	"testing"

	"github.com/codeactual/evolve/internal/model"
)

func TestAllAndByID(t *testing.T) {
	all := All()
	if len(all) != 2 {
		t.Fatalf("All() = %d harnesses, want 2", len(all))
	}
	for i, id := range []string{model.HarnessClaude, model.HarnessCodex} {
		if all[i].ID() != id {
			t.Errorf("All()[%d] = %q, want %q", i, all[i].ID(), id)
		}
		h, ok := ByID(id)
		if !ok {
			t.Errorf("ByID(%q) = not found", id)
			continue
		}
		if h.ID() != id {
			t.Errorf("ByID(%q).ID() = %q", id, h.ID())
		}
	}
	if _, ok := ByID("nope"); ok {
		t.Error("ByID(nope) = found, want none")
	}
}

// TestEvalRunnerCapability pins which harnesses implement EvalRunner: both
// built-in harnesses have a gradable headless run.
func TestEvalRunnerCapability(t *testing.T) {
	want := map[string]bool{model.HarnessClaude: true, model.HarnessCodex: true}
	for _, h := range All() {
		_, isRunner := h.(EvalRunner)
		if isRunner != want[h.ID()] {
			t.Errorf("%s EvalRunner = %v, want %v", h.ID(), isRunner, want[h.ID()])
		}
	}
}

// TestToolCallReporterCapability pins which harnesses can report tool calls
// from their eval output. Claude and Codex both emit structured output that
// carries tool invocations.
func TestToolCallReporterCapability(t *testing.T) {
	want := map[string]bool{model.HarnessClaude: true, model.HarnessCodex: true}
	for _, h := range All() {
		_, isReporter := h.(ToolCallReporter)
		if isReporter != want[h.ID()] {
			t.Errorf("%s ToolCallReporter = %v, want %v", h.ID(), isReporter, want[h.ID()])
		}
	}
}
