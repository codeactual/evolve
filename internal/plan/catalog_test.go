// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package plan

import (
	"testing"

	"github.com/codeactual/evolve/internal/evalspec"
	"github.com/codeactual/evolve/internal/model"
)

func TestFilterInclusion(t *testing.T) {
	var nilF *Filter
	if !nilF.skillIncluded("x") || !nilF.triggerIncluded("x", "q") || !nilF.evalIncluded("x", "e") {
		t.Error("nil filter must include everything")
	}

	f := &Filter{
		Skills:   map[string]bool{"a": true},
		Triggers: map[string]map[string]bool{"a": {"q1": true}},
		Evals:    map[string]map[string]bool{"a": {}}, // present but empty = none
	}
	if !f.skillIncluded("a") || f.skillIncluded("b") {
		t.Error("skillIncluded")
	}
	if !f.triggerIncluded("a", "q1") || f.triggerIncluded("a", "q2") {
		t.Error("triggerIncluded for restricted skill")
	}
	if !f.triggerIncluded("z", "anything") {
		t.Error("triggerIncluded for a skill with no entry must be unrestricted")
	}
	if f.evalIncluded("a", "e1") {
		t.Error("an empty (non-nil) eval set must include nothing")
	}
}

func TestApplicableModelsRestriction(t *testing.T) {
	triggers := []evalspec.Trigger{{Query: "q1"}}
	evals := []evalspec.Eval{{ID: "e1"}}
	anthropic := model.Model{ID: "anthropic/claude-sonnet-4-6", ProviderID: "anthropic"}
	openai := model.Model{ID: "openai/gpt-5", ProviderID: "openai"}

	// Empty restriction allows every model.
	if got := ApplicableTriggers(triggers, openai, nil, "s", nil); len(got) != 1 {
		t.Errorf("unrestricted triggers = %d, want 1", len(got))
	}

	// A restriction admits a named model and excludes the rest, for both tiers.
	allow := []string{"anthropic"}
	if got := ApplicableEvals(evals, anthropic, allow, "s", nil); len(got) != 1 {
		t.Errorf("allowed evals = %d, want 1", len(got))
	}
	if got := ApplicableEvals(evals, openai, allow, "s", nil); got != nil {
		t.Errorf("excluded evals = %+v, want nil", got)
	}
	if got := ApplicableTriggers(triggers, openai, allow, "s", nil); got != nil {
		t.Errorf("excluded triggers = %+v, want nil", got)
	}

	// Allows mirrors the gate.
	sc := SkillCatalog{Models: allow}
	if !sc.Allows(anthropic) || sc.Allows(openai) {
		t.Error("Allows disagrees with the models restriction")
	}
	if !(SkillCatalog{}).Allows(openai) {
		t.Error("empty restriction must allow every model")
	}
}
