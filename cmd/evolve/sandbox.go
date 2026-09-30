// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/codeactual/evolve/internal/layout"
	"github.com/codeactual/evolve/internal/runner"
)

// sandboxEnabled reports whether the OS sandbox is on: it is by default, and
// only the operator can turn it off — with --no-sandbox, EVOLVE_SANDBOX_ENABLED,
// or sandbox.enabled in the user-level config (a repository config that sets it
// is rejected at load).
func sandboxEnabled(noSandbox bool) bool {
	if noSandbox {
		return false
	}
	if opts.Viper != nil && opts.Viper.IsSet("sandbox.enabled") {
		return opts.Viper.GetBool("sandbox.enabled")
	}
	return true
}

// sandboxConfig reads the operator's sandbox policy: the operator-only
// sandbox.* keys (user-level config, EVOLVE_SANDBOX_*), with --bwrap-path
// overriding the bubblewrap location.
func sandboxConfig(repoRoot string) runner.SandboxConfig {
	cfg := runner.SandboxConfig{RepoRoot: repoRoot}
	if opts.Viper != nil {
		cfg.ReadPaths = opts.Viper.GetStringSlice("sandbox.read_paths")
		cfg.WritePaths = opts.Viper.GetStringSlice("sandbox.write_paths")
		cfg.BwrapPath = opts.Viper.GetString("sandbox.bwrap_path")
	}
	if runFlags.BwrapPath != "" {
		cfg.BwrapPath = runFlags.BwrapPath
	}
	return cfg
}

// resolveSandbox builds the filesystem-confinement policy for agent runs. It is
// on by default and deny-by-default: an agent sees only the system
// directories, the repository under test (read-only), its own run directory, its
// executable, the pre-seeded git config and bridged credential files
// (read-only), and whatever the operator grants with sandbox.read_paths and
// sandbox.write_paths. The operator's home directory is not mounted.
func resolveSandbox(repo *layout.Repo, noSandbox bool) (runner.Sandbox, error) {
	if !sandboxEnabled(noSandbox) {
		return runner.Sandbox{}, nil
	}
	return runner.NewSandbox(sandboxConfig(repo.Root))
}

// sandboxDoctorLines renders the doctor's sandbox section: the bubblewrap
// provenance verdict and an outer smoke run, or why the sandbox is off or
// misconfigured.
func sandboxDoctorLines(ctx context.Context) []string {
	if !sandboxEnabled(false) {
		return []string{"sandbox: disabled (sandbox.enabled=false) — agents run unconfined"}
	}
	root := opts.Root
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	cfg := sandboxConfig(root)
	bwrap, err := runner.ResolveBwrap(cfg.BwrapPath)
	if err != nil {
		return []string{fmt.Sprintf("bubblewrap: REFUSED — %v", err)}
	}
	lines := []string{fmt.Sprintf("bubblewrap: %s (provenance ok: non-setuid, owned by root or you, no writable ancestor)", bwrap)}
	sb, err := runner.NewSandbox(cfg)
	if err != nil {
		return append(lines, fmt.Sprintf("sandbox policy: INVALID — %v", err))
	}
	if err := runner.ProbeSandbox(ctx, sb); err != nil {
		return append(lines, fmt.Sprintf("outer sandbox: FAILED — %v", err))
	}
	return append(lines, "outer sandbox: ok (smoke run passed)")
}
