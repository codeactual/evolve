// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/model"
	"github.com/codeactual/evolve/internal/runner"
)

// listingHarness is a fake harness with the OfferedModels capability.
type listingHarness struct {
	harness.Harness
	offered []string
	err     error
}

func (l listingHarness) ListOfferedModels(context.Context, harness.ProbeExec) ([]string, error) {
	return l.offered, l.err
}

// onPathHarness returns a harness whose first CLI candidate resolves on PATH:
// a stub executable is created in a temp dir prepended to PATH.
func onPathBase(t *testing.T) harness.Harness {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return harness.NewClaude()
}

func TestProbeOfferedModels(t *testing.T) {
	base := onPathBase(t)
	nonOffering := &fakeTriggerProvider{} // no OfferedModels capability
	hs := []harness.Harness{
		listingHarness{Harness: base, offered: []string{"Sonnet 5"}},
		nonOffering,
	}
	got := ProbeOfferedModels(t.Context(), &runner.Exec{}, hs, time.Second)
	if !slices.Equal(got[base.ID()], []string{"Sonnet 5"}) {
		t.Errorf("offered[%s] = %v, want [Sonnet 5]", base.ID(), got[base.ID()])
	}
	if _, ok := got[nonOffering.ID()]; ok {
		t.Error("harness without the capability should be absent (unknown)")
	}
}

func TestProbeOfferedModelsFailuresAbsent(t *testing.T) {
	base := onPathBase(t)
	hs := []harness.Harness{
		listingHarness{Harness: base, err: errors.New("boom")},
	}
	if got := ProbeOfferedModels(t.Context(), &runner.Exec{}, hs, time.Second); len(got) != 0 {
		t.Errorf("failed probe = %v, want empty (unknown fails open)", got)
	}
}

// fakeProbeRunner records the spec and serves canned line-oriented stdout.
type fakeProbeRunner struct {
	spec  model.CommandSpec
	lines []string
}

func (f *fakeProbeRunner) Run(_ context.Context, spec model.CommandSpec, _ time.Duration,
	scan *runner.Scan,
) (runner.Result, error) {
	f.spec = spec
	if scan == nil {
		return runner.Result{Stdout: []byte(joinLines(f.lines))}, nil
	}
	for _, line := range f.lines {
		if scan.OnLine([]byte(line + "\n")) {
			return runner.Result{Hit: true}, nil
		}
	}
	return runner.Result{}, nil
}

func joinLines(lines []string) string {
	var out strings.Builder
	for _, l := range lines {
		out.WriteString(l + "\n")
	}
	return out.String()
}

func TestProbeExecResolvesCLIAndStopsEarly(t *testing.T) {
	h := onPathBase(t)
	fake := &fakeProbeRunner{lines: []string{"skip", "stop", "never-read"}}
	exec := probeExec(fake, h, time.Second)

	out, err := exec(t.Context(), model.CommandSpec{Argv: []string{"claude", "app-server"}},
		func(line []byte) bool { return string(line) == "stop\n" })
	if err != nil {
		t.Fatalf("probeExec: %v", err)
	}
	if got := string(out); got != "skip\nstop\n" {
		t.Errorf("collected = %q, want lines up to and including the stop line", got)
	}
	if filepath.Base(fake.spec.Argv[0]) != "claude" || fake.spec.Argv[0] == "claude" {
		t.Errorf("Argv[0] = %q, want resolved absolute CLI path", fake.spec.Argv[0])
	}
}

func TestProbeExecUnfinishedProtocolErrors(t *testing.T) {
	h := onPathBase(t)
	fake := &fakeProbeRunner{lines: []string{"noise"}}
	exec := probeExec(fake, h, time.Second)
	if _, err := exec(t.Context(), model.CommandSpec{Argv: []string{"claude"}},
		func([]byte) bool { return false }); err == nil {
		t.Error("probe that never saw its response should error, not return partial output")
	}
}

// TestProbeExecRunsInFreshEmptyDir pins that every probe runs from its own
// empty temp directory — never the process cwd (normally the repository under
// test, whose project hooks and MCP servers a headless claude would load) — and
// that the directory is gone afterward.
func TestProbeExecRunsInFreshEmptyDir(t *testing.T) {
	h := onPathBase(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var seenDir string
	var entries []os.DirEntry
	rec := probeRunnerFunc(func(_ context.Context, spec model.CommandSpec, _ time.Duration,
		_ *runner.Scan,
	) (runner.Result, error) {
		seenDir = spec.Dir
		entries, _ = os.ReadDir(spec.Dir)
		return runner.Result{Stdout: []byte("ok")}, nil
	})
	exec := probeExec(rec, h, time.Second)
	if _, err := exec(t.Context(), model.CommandSpec{Argv: []string{"claude", "-p", "/model"}}, nil); err != nil {
		t.Fatalf("probeExec: %v", err)
	}
	if seenDir == "" {
		t.Fatal("probe ran with an empty Dir (inherits the process cwd)")
	}
	if seenDir == cwd || strings.HasPrefix(cwd, seenDir+string(os.PathSeparator)) {
		t.Errorf("probe Dir = %q, must not be the process cwd %q or an ancestor of it", seenDir, cwd)
	}
	if len(entries) != 0 {
		t.Errorf("probe Dir held %d entries while running, want a fresh empty dir", len(entries))
	}
	if _, err := os.Stat(seenDir); !os.IsNotExist(err) {
		t.Errorf("probe Dir %q still exists after the probe (err=%v)", seenDir, err)
	}
}

// probeRunnerFunc adapts a function to the probeRunner interface.
type probeRunnerFunc func(context.Context, model.CommandSpec, time.Duration, *runner.Scan) (runner.Result, error)

func (f probeRunnerFunc) Run(ctx context.Context, spec model.CommandSpec, timeout time.Duration,
	scan *runner.Scan,
) (runner.Result, error) {
	return f(ctx, spec, timeout, scan)
}
