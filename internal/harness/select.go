// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package harness

import (
	"fmt"
	"slices"
	"strings"

	"github.com/codeactual/evolve/internal/model"
)

// Selection is one (model, harness) pair a sweep runs. Exactly one harness per
// model — the resolved executor — so evals run once per model, never once per
// harness. Key is the model's results key; the harness does not appear in it.
type Selection struct {
	Model   model.Model
	Harness Harness
}

// Key is the results-file model key, provider-qualified
// ("anthropic/claude-sonnet-4-6").
func (s Selection) Key() string { return s.Model.Key() }

// RunnableHarness picks the harness id to execute m given the set of eligible
// harness ids (configured/available and not filtered out). The model's
// preferred harness wins when eligible; otherwise the first eligible supported
// harness in harnessOrder, so selection is deterministic.
func RunnableHarness(m model.Model, eligible map[string]bool) (string, bool) {
	if eligible[m.Preferred] && m.Supports(m.Preferred) {
		return m.Preferred, true
	}
	for _, id := range harnessOrder {
		if eligible[id] && m.Supports(id) {
			return id, true
		}
	}
	return "", false
}

// Select resolves a --models spec — provider ids, canonical or bare model ids,
// or "all", comma-separated — against the available model set, binding each
// matched model to its runnable harness from eligible. Models that match the
// spec but have no eligible harness are skipped (the caller warns); a token
// matching no model at all is an error. An empty spec means "all".
func Select(spec string, models []model.Model, eligible []Harness) ([]Selection, error) {
	elig := map[string]bool{}
	for _, h := range eligible {
		elig[h.ID()] = true
	}
	byID := map[string]Harness{}
	for _, h := range eligible {
		byID[h.ID()] = h
	}

	var tokens []string
	for t := range strings.SplitSeq(spec, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 || slices.Contains(tokens, "all") {
		tokens = []string{"all"}
	}

	var selected []Selection
	seen := map[string]bool{}
	add := func(m model.Model) {
		if seen[m.Key()] {
			return
		}
		id, ok := RunnableHarness(m, elig)
		if !ok {
			return // matched but no eligible harness — caller reports this
		}
		seen[m.Key()] = true
		selected = append(selected, Selection{Model: m, Harness: byID[id]})
	}

	for _, token := range tokens {
		matched := false
		for _, m := range models {
			if token == "all" || token == m.ProviderID || token == m.ID || token == m.BareID() {
				matched = true
				add(m)
			}
		}
		if !matched {
			return nil, fmt.Errorf("unknown provider or model in --model: %q", token)
		}
	}
	return selected, nil
}
