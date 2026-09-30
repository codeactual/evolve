// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"

	"github.com/codeactual/evolve/internal/results"
)

// MigrationOutcome reports that one skill's results file was upgraded to the
// current schema. Only upgraded files yield an outcome; files already current or
// absent are silent.
type MigrationOutcome struct {
	Plugin     string
	Skill      string
	FromSchema int // schema the file carried on disk before the upgrade
}

// MigrateResults upgrades every stored results file to the current schema,
// rewriting in the configured format only those written under an older
// structural schema; files already current are left untouched. It returns one
// outcome per upgraded file, in eval-set order. A file that cannot be migrated
// without discarding committed data — unreadable, older than the migratable
// range, or written by a newer evolve — stops the run with an error and is left
// in place rather than overwritten.
func (o *Options) MigrateResults() ([]MigrationOutcome, error) {
	repo, err := o.Repo()
	if err != nil {
		return nil, err
	}
	sets, err := repo.EvalSets()
	if err != nil {
		return nil, err
	}
	var out []MigrationOutcome
	for _, set := range sets {
		from, upgraded, err := results.MigrateFile(set.ResultsDir, set.Plugin.Name, set.Skill, o.ResultsFormat)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", set.Plugin.Name, set.Skill, err)
		}
		if upgraded {
			out = append(out, MigrationOutcome{Plugin: set.Plugin.Name, Skill: set.Skill, FromSchema: from})
		}
	}
	return out, nil
}
