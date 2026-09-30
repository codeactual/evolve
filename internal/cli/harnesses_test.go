// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/spf13/viper"
)

func TestAllowedHarnessIDs(t *testing.T) {
	o := &Options{Viper: viper.New()}
	if ids, configured := o.AllowedHarnessIDs(); configured || ids != nil {
		t.Errorf("unset: (%v,%v), want (nil,false)", ids, configured)
	}
	// AllowedHarnessIDs only reports the configured ids; validating them is
	// Harnesses' job, so an unknown id passes through here.
	o.Viper.Set("harnesses", []string{"claude", "codex", "bogus"})
	if ids, configured := o.AllowedHarnessIDs(); !configured || len(ids) != 3 {
		t.Errorf("set: (%v,%v), want 3 ids/true", ids, configured)
	}
}

func TestHarnessesRejectsUnknown(t *testing.T) {
	o := &Options{Viper: viper.New()}
	o.Viper.Set("harnesses", []string{"claude", "bogus"})
	if _, err := o.Harnesses(); err == nil {
		t.Error("Harnesses() = nil error for unknown harness, want error")
	}
}

func TestValidateFilterRestrictions(t *testing.T) {
	tests := []struct {
		name        string
		harnessCfg  []string
		modelCfg    []string
		harnessFlag []string
		modelFlag   []string
		wantErr     bool
	}{
		{"unknown harness flag", nil, nil, []string{"bogus"}, nil, true},
		{"known harness, no restriction", nil, nil, []string{"claude"}, nil, false},
		{"harness outside restriction", []string{"claude"}, nil, []string{"codex"}, nil, true},
		{"harness subset of restriction", []string{"claude", "codex"}, nil, []string{"claude"}, nil, false},
		{"model outside restriction", nil, []string{"anthropic"}, nil, []string{"openai"}, true},
		{"model subset of restriction", nil, []string{"anthropic"}, nil, []string{"anthropic/claude-haiku-4-5"}, false},
		{"model no restriction", nil, nil, nil, []string{"openai/gpt-5.4"}, false},
		{"model all token", nil, []string{"anthropic"}, nil, []string{"all"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Options{Viper: viper.New()}
			if tt.harnessCfg != nil {
				o.Viper.Set("harnesses", tt.harnessCfg)
			}
			if tt.modelCfg != nil {
				o.Viper.Set("models", tt.modelCfg)
			}
			err := o.ValidateFilterRestrictions(tt.harnessFlag, tt.modelFlag)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// TestUnsupportedModelWarnings: a configured model whose harness is not
// available is warned about; with no models restriction there are no warnings.
func TestUnsupportedModelWarnings(t *testing.T) {
	o := &Options{Viper: viper.New()}
	if w, err := o.UnsupportedModelWarnings(); err != nil || w != nil {
		t.Errorf("no restriction: (%v,%v), want (nil,nil)", w, err)
	}

	// Opus is supported only by the claude harness; restrict harnesses to codex
	// so no available harness can run it, regardless of PATH.
	o.Viper.Set("models", []string{"anthropic/claude-opus-4-8"})
	o.Viper.Set("harnesses", []string{"codex"})
	w, err := o.UnsupportedModelWarnings()
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 1 {
		t.Fatalf("warnings = %v, want 1", w)
	}
}
