// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

//go:build live && linux

package runner

import (
	"context"
	"os"
	"testing"
)

// TestLiveNestedBwrapProbe proves that a bubblewrap launched inside evolve's
// sandbox works, which the agent CLIs' own sandboxes need for every shell
// command. It needs EVOLVE_LIVE_BWRAP to name a bubblewrap copy outside any
// AppArmor profile (see the Makefile's live target): a bubblewrap launched from
// /usr/bin/bwrap cannot nest on hosts with
// kernel.apparmor_restrict_unprivileged_userns=1.
func TestLiveNestedBwrapProbe(t *testing.T) {
	bwrap := os.Getenv("EVOLVE_LIVE_BWRAP")
	if bwrap == "" {
		t.Fatal("EVOLVE_LIVE_BWRAP must name an unprofiled, operator-owned bubblewrap copy (see `make live`)")
	}
	t.Setenv("HOME", t.TempDir())
	sb, err := NewSandbox(SandboxConfig{RepoRoot: t.TempDir(), BwrapPath: bwrap})
	if err != nil {
		t.Fatal(err)
	}
	if err := ProbeNested(context.Background(), sb); err != nil {
		t.Fatalf("ProbeNested with BwrapPath=%s: %v", bwrap, err)
	}
}
