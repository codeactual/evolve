// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strings"

	"github.com/codeactual/evolve/internal/grade"
	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/model"
)

// JudgeSelection resolves the judge-model token (--judge-model / judge_model /
// the default) to the (model, harness) pair that grades llm assertions. The
// token matches a canonical or bare model id from AvailableModels — the judge
// is a grading instrument, so the `models` restriction and the --model/--harness
// sweep filters do not apply — bound via harness.RunnableHarness to the first
// configured-and-installed harness that supports the model and can run
// headless judge sessions (implements harness.EvalRunner).
func (o *Options) JudgeSelection(token string) (harness.Selection, error) {
	if token == "" {
		token = grade.DefaultJudgeModel
	}
	models, err := o.AvailableModels()
	if err != nil {
		return harness.Selection{}, err
	}
	idx := -1
	for i, m := range models {
		if token == m.ID || token == m.BareID() {
			idx = i
			break
		}
	}
	if idx < 0 {
		return harness.Selection{}, fmt.Errorf("judge model %q: not a known model (see `evolve models`)", token)
	}
	available, err := o.AvailableHarnesses()
	if err != nil {
		return harness.Selection{}, err
	}
	return bindJudgeHarness(models[idx], available)
}

// bindJudgeHarness binds the judge model m to the first of the available
// harnesses that supports it and implements harness.EvalRunner. It is split out
// of JudgeSelection so the no-EvalRunner skip is testable without a real
// harness that lacks the capability.
func bindJudgeHarness(m model.Model, available []harness.Harness) (harness.Selection, error) {
	eligible := map[string]bool{}
	byID := map[string]harness.Harness{}
	for _, h := range available {
		if _, ok := h.(harness.EvalRunner); !ok {
			continue // no headless eval support
		}
		eligible[h.ID()] = true
		byID[h.ID()] = h
	}
	id, ok := harness.RunnableHarness(m, eligible)
	if !ok {
		supported := m.SupportedHarnessIDs()
		return harness.Selection{}, fmt.Errorf("judge model %s: no installed harness can run judge sessions (supported by: %s)",
			m.ID, strings.Join(supported, ", "))
	}
	return harness.Selection{Model: m, Harness: byID[id]}, nil
}
