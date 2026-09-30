// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/model"
	"github.com/codeactual/evolve/internal/run"
	"github.com/codeactual/evolve/internal/runner"
	"github.com/codeactual/evolve/internal/version"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check each harness (CLI on PATH, credential) and each vendor's counting API",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := opts.ChecksConfig().ValidateSignals(); err != nil {
			return fmt.Errorf("config: %w", err)
		}
		harnesses, err := opts.Harnesses()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		outln(w, "HARNESS\tCLI\tCREDENTIAL")
		for _, h := range harnesses {
			cliPath := "missing (" + h.CLI()[0] + ")"
			if path, ok := harness.Available(h); ok {
				cliPath = path
			}
			outf(w, "%s\t%s\t%s\n", h.ID(), cliPath, credentialStatus(h.EnvKeys()))
		}
		if err := w.Flush(); err != nil {
			return err
		}

		// Offered-models probes: what each installed CLI reports the operator's
		// account actually serves — the list the interactive form deselects
		// against. Absent capability or a failed probe reads "unknown", the
		// fail-open verdict.
		offered := run.ProbeOfferedModels(cmd.Context(), &runner.Exec{}, harnesses,
			offeredModelsProbeTimeout)
		wm := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		outln(wm, "\nHARNESS\tOFFERED MODELS")
		for _, h := range harnesses {
			outf(wm, "%s\t%s\n", h.ID(), offeredStatus(h, offered))
		}
		if err := wm.Flush(); err != nil {
			return err
		}

		w2 := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		outln(w2, "\nPROVIDER\tTOKEN COUNTING")
		for _, p := range model.Providers() {
			outf(w2, "%s\t%s\n", p.ID, probeCounting(cmd.Context(), p.ID))
		}
		if err := w2.Flush(); err != nil {
			return err
		}

		warnings, err := opts.UnsupportedModelWarnings()
		if err != nil {
			return err
		}
		for _, msg := range warnings {
			outf(cmd.OutOrStdout(), "WARN: %s\n", msg)
		}

		outln(cmd.OutOrStdout(), "\nSANDBOX")
		for _, line := range sandboxDoctorLines(cmd.Context()) {
			outf(cmd.OutOrStdout(), "  %s\n", line)
		}

		outf(cmd.OutOrStdout(), "\nLLM judge: %s\n", judgeStatus())

		outf(cmd.OutOrStdout(), "\nVersion pin: %s\n", versionPinStatus())
		return nil
	},
}

func init() {
	doctorCmd.Flags().StringVar(&runFlags.BwrapPath, "bwrap-path", "",
		"bubblewrap executable to check, instead of the one on PATH (operator config: sandbox.bwrap_path)")
	rootCmd.AddCommand(doctorCmd)
}

// versionPinStatus renders the repository's version pin verdict for the doctor
// output: doctor diagnoses a pin mismatch rather than failing on it, so it
// stays usable on exactly the binary the pin rejects.
func versionPinStatus() string {
	pin := opts.VersionPin()
	if pin == "" {
		return "none (any evolve version may run)"
	}
	var warn bytes.Buffer
	err := opts.CheckVersionPin(version.Version, &warn)
	switch {
	case err != nil:
		return fmt.Sprintf("VIOLATED — %v", err)
	case warn.Len() > 0:
		return fmt.Sprintf("%q skipped (evolve %s is not a release build)", pin, version.Version)
	default:
		return fmt.Sprintf("%q satisfied by evolve %s", pin, version.Version)
	}
}

// judgeStatus renders the resolved LLM judge — the model that grades llm
// assertions and the installed harness that will drive it — or why no judge
// can run (llm assertions would then fail loudly).
func judgeStatus() string {
	token := ""
	if opts.Viper != nil {
		token = opts.Viper.GetString("judge_model")
	}
	sel, err := opts.JudgeSelection(token)
	if err != nil {
		return fmt.Sprintf("UNAVAILABLE — %v", err)
	}
	cli, _ := harness.Available(sel.Harness)
	return fmt.Sprintf("%s via %s (%s)", sel.Model.ID, sel.Harness.ID(), cli)
}

// offeredStatus renders one harness's offered-models probe verdict.
func offeredStatus(h harness.Harness, offered map[string][]string) string {
	if list, ok := offered[h.ID()]; ok {
		return strings.Join(list, ", ")
	}
	if _, ok := h.(harness.OfferedModels); !ok {
		return "n/a (no listing surface)"
	}
	if _, onPath := harness.Available(h); !onPath {
		return "skipped (CLI not on PATH)"
	}
	return "unknown (probe failed; all models treated as offered)"
}

// credentialStatus reports the first set credential env var, or that none of
// them is set (naming the highest-precedence one).
func credentialStatus(envKeys []string) string {
	if len(envKeys) == 0 {
		return "n/a"
	}
	for _, env := range envKeys {
		if os.Getenv(env) != "" {
			return env
		}
	}
	return "not set (" + envKeys[0] + ")"
}

// hasCredential reports whether any of the env vars is set.
func hasCredential(envKeys []string) bool {
	for _, env := range envKeys {
		if os.Getenv(env) != "" {
			return true
		}
	}
	return false
}

// probeCounting makes one tiny counting call for a vendor that supports it.
func probeCounting(ctx context.Context, providerID string) string {
	tc, ok := model.CounterFor(providerID)
	if !ok {
		return "n/a (no counting API)"
	}
	if !hasCredential(model.CounterEnvKeys(providerID)) {
		return "skipped (no credential)"
	}
	bareID := ""
	for _, m := range model.AllModels(nil) {
		if m.ProviderID == providerID {
			bareID = m.BareID()
			break
		}
	}
	if bareID == "" {
		return "skipped (no models)"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tokens, err := tc.CountTokens(ctx, bareID, "ping")
	if err != nil {
		return fmt.Sprintf("failed: %v", err)
	}
	return fmt.Sprintf("ok (%d tokens for %q on %s)", tokens, "ping", bareID)
}
