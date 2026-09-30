// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/codeactual/evolve/internal/report"
	"github.com/codeactual/evolve/internal/version"
)

// Thresholds reads report.thresholds from config, falling back to the report
// package's default pass rates for the keys the config leaves unset. The gated
// maturity set reads from report.thresholds.maturity, defaulting to
// report.DefaultGatedMaturity (all three levels: stable, unstable, prerelease)
// when unset or when a configured token fails to parse (MaturityConfig reports
// that error to callers that need to surface it).
func (o *Options) Thresholds() report.Thresholds {
	th := report.Thresholds{
		TriggersMinPassRate: report.DefaultTriggersMinPassRate,
		EvalsMinPassRate:    report.DefaultEvalsMinPassRate,
		Models:              o.Viper.GetStringSlice("report.thresholds.models"),
		Maturity:            slices.Clone(report.DefaultGatedMaturity),
	}
	if o.Viper.IsSet("report.thresholds.triggers_min_pass_rate") {
		th.TriggersMinPassRate = o.Viper.GetFloat64("report.thresholds.triggers_min_pass_rate")
	}
	if o.Viper.IsSet("report.thresholds.evals_min_pass_rate") {
		th.EvalsMinPassRate = o.Viper.GetFloat64("report.thresholds.evals_min_pass_rate")
	}
	if levels, err := o.MaturityConfig(); err == nil && levels != nil {
		th.Maturity = levels
	}
	return th
}

// MaturityConfig resolves the gated maturity set from report.thresholds.maturity,
// validating each token via report.ParseMaturity. It returns nil, nil when the
// key is unset (callers default to report.DefaultGatedMaturity), and a clear
// error for an unrecognized token — a malformed config value must not
// silently no-op.
func (o *Options) MaturityConfig() ([]report.Maturity, error) {
	if !o.Viper.IsSet("report.thresholds.maturity") {
		return nil, nil
	}
	return parseMaturityLevels(o.Viper.GetStringSlice("report.thresholds.maturity"))
}

// ParseMaturityFlag parses the comma-separated --maturity flag value into the
// gated maturity set, validating each token via report.ParseMaturity.
func ParseMaturityFlag(v string) ([]report.Maturity, error) {
	return parseMaturityLevels(splitFlag(v))
}

// ResolveMaturity is the single owner of the gated-maturity precedence for
// `report --check`: the --maturity flag wins when set, else report.thresholds.
// maturity from config, else the default gate-everything set. Unlike
// Thresholds() (which swallows a malformed-config error as its documented
// fail-safe for the dashboard), this surfaces the error so `report --check`
// fails loudly on a bad token. flagChanged and flagValue come from cobra
// (cmd.Flags().Changed("maturity") and the flag's value).
func (o *Options) ResolveMaturity(flagChanged bool, flagValue string) ([]report.Maturity, error) {
	if flagChanged {
		return ParseMaturityFlag(flagValue)
	}
	levels, err := o.MaturityConfig()
	if err != nil {
		return nil, err
	}
	if levels != nil {
		return levels, nil
	}
	return slices.Clone(report.DefaultGatedMaturity), nil
}

// parseMaturityLevels parses each token via report.ParseMaturity, rejecting
// the first unrecognized one with a clear error (a bad value must not
// silently no-op). An empty or whitespace-only set (splitFlag drops blank
// tokens, so `--maturity ""` or `","` reaches here empty) is rejected too:
// an empty gated set demotes every plugin to a warning — a silent "gate
// nothing" escape hatch, exactly the failure mode this gate warns against. To
// gate nothing, pass --check without --strict (a pass-rate gate) rather than
// disarming maturity gating invisibly.
func parseMaturityLevels(tokens []string) ([]report.Maturity, error) {
	levels := make([]report.Maturity, 0, len(tokens))
	for _, t := range tokens {
		// Trim per token so the config path (viper's raw GetStringSlice) accepts
		// padded values the same as the flag path (splitFlag already trims);
		// a token that trims to "" is dropped, so [" "] collapses to the empty
		// set and is rejected below rather than erroring as an unknown level.
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		m, ok := report.ParseMaturity(t)
		if !ok {
			return nil, fmt.Errorf("unknown maturity level %q (want stable, unstable, or prerelease)", t)
		}
		levels = append(levels, m)
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("empty maturity set (want at least one of stable, unstable, prerelease); " +
			"an empty set would gate nothing")
	}
	return levels, nil
}

// StrictConfig is the configured report.strict default (the --strict flag overrides).
func (o *Options) StrictConfig() bool { return o.Viper.GetBool("report.strict") }

// DefinedModelKeys is the configured model matrix as sorted keys — the strict
// --check denominator.
func (o *Options) DefinedModelKeys() ([]string, error) {
	configured, err := o.ConfiguredModels()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(configured))
	for _, m := range configured {
		keys = append(keys, m.Key())
	}
	sort.Strings(keys)
	return keys, nil
}

// RegenerateReports refreshes the Markdown/JSON reports after a sweep, the
// way the Python harness did from run_triggers/run_evals.
func (o *Options) RegenerateReports() error {
	repo, err := o.Repo()
	if err != nil {
		return err
	}
	models, err := o.AvailableModels()
	if err != nil {
		return err
	}
	active, _, err := o.ActiveModelKeys()
	if err != nil {
		return err
	}
	_, err = report.Generate(report.Options{
		Repo:         repo,
		ToolVersion:  version.Version,
		Models:       models,
		Format:       o.ResultsFormat,
		ActiveModels: active,
	})
	return err
}
