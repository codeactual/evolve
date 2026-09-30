// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/model"
)

// stubPath points PATH at a temp dir holding executable stubs for the named
// CLIs, so harness availability is fully test-controlled.
func stubPath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestJudgeSelection(t *testing.T) {
	tests := []struct {
		name         string
		path         []string
		cfgHarnesses []string
		cfgModels    []string
		token        string
		wantModel    string
		wantHarness  string
		wantErr      string
	}{
		{
			"default token",
			[]string{"claude"},
			nil, nil, "",
			"anthropic/claude-sonnet-5", "claude", "",
		},
		{
			"bare id",
			[]string{"claude"},
			nil, nil, "claude-sonnet-5",
			"anthropic/claude-sonnet-5", "claude", "",
		},
		{
			"canonical id",
			[]string{"claude"},
			nil, nil, "anthropic/claude-sonnet-5",
			"anthropic/claude-sonnet-5", "claude", "",
		},
		{
			"unknown token",
			[]string{"claude"},
			nil, nil, "bogus",
			"", "", "not a known model",
		},
		// The judge is a grading instrument: a `models` restriction on what is
		// under test does not constrain it.
		{
			"models restriction ignored",
			[]string{"claude"},
			nil,
			[]string{"openai"},
			"claude-sonnet-5",
			"anthropic/claude-sonnet-5", "claude", "",
		},
		// A model only one harness supports binds to that harness when it is the
		// only one installed.
		{
			"codex-only model",
			[]string{"codex"},
			nil, nil, "gpt-5.5",
			"openai/gpt-5.5", "codex", "",
		},
		{
			"no harness installed", nil, nil, nil, "claude-sonnet-5",
			"", "", "no installed harness can run judge sessions",
		},
		{
			"model's harness not installed",
			[]string{"codex"},
			nil, nil, "claude-sonnet-5",
			"", "", "no installed harness can run judge sessions",
		},
		{
			"harnesses restriction respected",
			[]string{"claude", "codex"},
			[]string{"codex"},
			nil, "claude-sonnet-5",
			"", "", "no installed harness can run judge sessions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubPath(t, tt.path...)
			o := &Options{Viper: viper.New()}
			if tt.cfgHarnesses != nil {
				o.Viper.Set("harnesses", tt.cfgHarnesses)
			}
			if tt.cfgModels != nil {
				o.Viper.Set("models", tt.cfgModels)
			}
			sel, err := o.JudgeSelection(tt.token)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if sel.Model.ID != tt.wantModel || sel.Harness.ID() != tt.wantHarness {
				t.Errorf("selection = (%s, %s), want (%s, %s)",
					sel.Model.ID, sel.Harness.ID(), tt.wantModel, tt.wantHarness)
			}
		})
	}
}

// nonEvalHarness is a harness that implements only the required Harness
// surface, not harness.EvalRunner. Both built-in harnesses run evals, so the
// no-EvalRunner skip in bindJudgeHarness can only be exercised with a fake; it
// is used through bindJudgeHarness directly, bypassing PATH and config.
type nonEvalHarness struct{ id string }

func (h nonEvalHarness) ID() string                                   { return h.id }
func (nonEvalHarness) Name() string                                   { return "NonEval" }
func (nonEvalHarness) CLI() []string                                  { return []string{"sh"} }
func (nonEvalHarness) EnvKeys() []string                              { return nil }
func (nonEvalHarness) SkillDirs() []string                            { return nil }
func (nonEvalHarness) ScanLine([]byte, string, string) (bool, string) { return false, "" }
func (nonEvalHarness) TriggerSpec(ws, _, _ string, _ bool) model.CommandSpec {
	return model.CommandSpec{Dir: ws}
}

// TestJudgeSelectionSkipsHarnessWithoutEvalRunner: a harness that supports the
// judge model but cannot run headless evals is never bound; an EvalRunner
// harness that also supports the model is used instead, and with none the
// selection fails.
func TestJudgeSelectionSkipsHarnessWithoutEvalRunner(t *testing.T) {
	m := model.Model{
		ID: "anthropic/dual", ProviderID: "anthropic", Name: "Dual",
		Supported: map[string]string{"plain": "dual", "claude": "dual"},
		Preferred: "plain", // preferred, but unable to judge
	}
	plain := nonEvalHarness{id: "plain"}

	sel, err := bindJudgeHarness(m, []harness.Harness{plain, harness.NewClaude()})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Harness.ID() != "claude" {
		t.Errorf("bound harness = %q, want claude (plain has no EvalRunner)", sel.Harness.ID())
	}

	_, err = bindJudgeHarness(m, []harness.Harness{plain})
	if err == nil || !strings.Contains(err.Error(), "no installed harness can run judge sessions") {
		t.Errorf("err = %v, want no-runnable-harness error", err)
	}
}
