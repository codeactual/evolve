// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/codeactual/evolve/internal/runner"
)

// preflightHarness points the package globals the sweep setup reads at a scratch
// plugin repository and a fake nested-bubblewrap probe, restoring them after the
// test. It returns the probe's call counter.
func preflightHarness(t *testing.T, probe func(context.Context, runner.Sandbox) error) *int {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		".claude-plugin/plugin.json":     `{"name":"solo","version":"0.1.0"}`,
		"skills/solo-skill/SKILL.md":     "---\nname: solo-skill\n---\nbody\n",
		"evals/solo-skill/triggers.json": `{"triggers":[{"query":"q","should_trigger":true}]}`,
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	savedRoot, savedViper, savedLayout := opts.Root, opts.Viper, opts.Layout
	savedProbe, savedNo := nestedProbe, runFlags.NoSandbox
	t.Cleanup(func() {
		opts.Root, opts.Viper, opts.Layout = savedRoot, savedViper, savedLayout
		nestedProbe, runFlags.NoSandbox = savedProbe, savedNo
		preflightOnce, preflightErr = sync.Once{}, nil
	})
	opts.Root, opts.Viper, opts.Layout = root, viper.New(), "auto"
	runFlags.NoSandbox = false
	preflightOnce, preflightErr = sync.Once{}, nil
	calls := new(int)
	nestedProbe = func(ctx context.Context, sb runner.Sandbox) error {
		*calls++
		return probe(ctx, sb)
	}
	return calls
}

func sweepCmd(t *testing.T, args ...string) (*SweepFlags, *cobra.Command) {
	t.Helper()
	var f SweepFlags
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	f.register(cmd, 120)
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &f, cmd
}

// TestRunPreflightFailsClosed pins that with the sandbox enabled, a failing
// nested-bubblewrap probe stops the run (exit 2) before any agent can start:
// sweepOptions returns the error and no engine options, so no runner ever sees
// an agent spec.
func TestRunPreflightFailsClosed(t *testing.T) {
	calls := preflightHarness(t, func(context.Context, runner.Sandbox) error {
		return errors.New("bwrap: No permissions to create a new namespace")
	})
	f, cmd := sweepCmd(t)
	got, err := f.sweepOptionsW(cmd, io.Discard)
	if err == nil {
		t.Fatal("sweepOptions succeeded despite a failing nested probe")
	}
	for _, want := range []string{"No permissions to create a new namespace", "AppArmor", "sandbox.bwrap_path", "evolve doctor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if got.Runner != nil {
		t.Error("engine options carry a runner although the preflight failed")
	}
	if *calls != 1 {
		t.Errorf("nested probe ran %d times, want 1", *calls)
	}
	// The verdict is cached for the process: a second sweep does not probe again.
	if _, err := f.sweepOptionsW(cmd, io.Discard); err == nil || *calls != 1 {
		t.Errorf("second sweep: err=%v, probe calls=%d; want the cached failure and no re-probe", err, *calls)
	}
}

func TestRunPreflightSkippedWhenSandboxDisabled(t *testing.T) {
	for name, disable := range map[string]func(){
		"flag":   func() { runFlags.NoSandbox = true },
		"config": func() { opts.Viper.Set("sandbox.enabled", false) },
	} {
		t.Run(name, func(t *testing.T) {
			calls := preflightHarness(t, func(context.Context, runner.Sandbox) error { return errors.New("would fail") })
			disable()
			f, cmd := sweepCmd(t)
			// Later resolution may fail for unrelated reasons (no CLI installed);
			// the preflight error specifically must not appear.
			_, err := f.sweepOptionsW(cmd, io.Discard)
			if err != nil && strings.Contains(err.Error(), "cannot start inside evolve's sandbox") {
				t.Errorf("preflight ran with the sandbox disabled: %v", err)
			}
			if *calls != 0 {
				t.Errorf("nested probe ran %d times with the sandbox disabled, want 0", *calls)
			}
		})
	}
}

func TestRunPreflightSkippedForCountOnly(t *testing.T) {
	calls := preflightHarness(t, func(context.Context, runner.Sandbox) error { return errors.New("would fail") })
	f, cmd := sweepCmd(t, "--count-only")
	_, _ = f.sweepOptionsW(cmd, io.Discard)
	if *calls != 0 {
		t.Errorf("nested probe ran %d times for a count-only run that starts no agent, want 0", *calls)
	}
}

func TestInnerSandboxConfig(t *testing.T) {
	saved := opts.Viper
	t.Cleanup(func() { opts.Viper = saved })
	opts.Viper = viper.New()
	in, err := innerSandboxConfig()
	if err != nil || len(in.ClaudeAllowedDomains) != 0 || in.CodexNetworkAccess {
		t.Errorf("defaults = %+v, %v; want no domains and no Codex network", in, err)
	}
	opts.Viper.Set("sandbox.claude_allowed_domains", []string{"proxy.golang.org", "*.npmjs.org"})
	opts.Viper.Set("sandbox.codex_network_access", true)
	in, err = innerSandboxConfig()
	if err != nil || len(in.ClaudeAllowedDomains) != 2 || !in.CodexNetworkAccess {
		t.Errorf("configured = %+v, %v; want the operator's values", in, err)
	}
	opts.Viper.Set("sandbox.claude_allowed_domains", []string{"*"})
	if _, err := innerSandboxConfig(); err == nil || !strings.Contains(err.Error(), "srt") {
		t.Errorf("wildcard domain error = %v, want the srt rejection at config resolution", err)
	}
}
