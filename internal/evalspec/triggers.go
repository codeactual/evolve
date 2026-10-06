// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package evalspec

import (
	"fmt"

	"github.com/codeactual/evolve/internal/encfmt"
)

// Trigger is one trigger-accuracy query. Triggers carry no model restriction of
// their own: they run for the model set the sibling evals file's Models resolves
// to (intersected with the root models).
type Trigger struct {
	Query         string `json:"query"`
	ShouldTrigger bool   `json:"should_trigger"`
}

// TriggersFile is one authored triggers document: the same envelope shape as
// skill-creator's evals.json, {skill_name?, triggers: [...]}.
type TriggersFile struct {
	SkillName string    `json:"skill_name,omitempty"`
	Triggers  []Trigger `json:"triggers"`
}

// LoadTriggers parses an authored triggers file in any supported format.
func LoadTriggers(path string) (*TriggersFile, error) {
	var f TriggersFile
	if err := encfmt.DecodeFile(path, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// ValidateTriggers returns the problems in an authored trigger list.
func ValidateTriggers(triggers []Trigger) []string {
	var problems []string
	seen := map[string]bool{}
	for i, t := range triggers {
		switch {
		case t.Query == "":
			problems = append(problems, fmt.Sprintf("triggers[%d]: empty query", i))
		case seen[t.Query]:
			problems = append(problems, fmt.Sprintf("triggers[%d]: duplicate query %q", i, t.Query))
		}
		seen[t.Query] = true
	}
	return problems
}
