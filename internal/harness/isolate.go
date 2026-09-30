// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package harness

import (
	"os"
	"path/filepath"
)

// This file holds the shared session-isolation helpers. Every harness points
// its CLI at a throwaway, workspace-rooted state directory (".evolve/<name>-home")
// so trigger/eval runs never touch the operator's real session history or
// long-term memory; the directory dies with the workspace. Auth is bridged in
// from the operator's real config root: the auth-only files are symlinked, so a
// mid-run token refresh writes through. Both CLIs have a dedicated config-dir
// variable (CLAUDE_CONFIG_DIR, CODEX_HOME).
//
// All helpers are best-effort and never fail the run: a failure here still
// leaves the env override set, so the CLI creates its own tree (possibly
// unauthenticated), which surfaces as a runtime error on the case rather than
// an evolve crash.

// isolatedDir is the absolute workspace-rooted path for a slash-separated
// relative state dir like ".evolve/claude-home".
func isolatedDir(ws, rel string) string {
	if ws == "" {
		return filepath.FromSlash(rel)
	}
	return filepath.Join(ws, filepath.FromSlash(rel))
}

// operatorDir is the operator's real config root (the source of bridged auth),
// not the per-workspace isolated one. Honors envVar when the parent evolve
// process itself has it set (pass "" for CLIs without one); otherwise
// ~/<defaultRel>.
func operatorDir(envVar, defaultRel string) string {
	if envVar != "" {
		if d := os.Getenv(envVar); d != "" {
			return d
		}
	}
	userHome, err := os.UserHomeDir()
	if err != nil || userHome == "" {
		return ""
	}
	return filepath.Join(userHome, defaultRel)
}

// linkFile exposes src inside an isolated state dir. Prefers a symlink so
// mid-run writes (e.g. a token refresh) go through to the real file; falls
// back to a one-shot 0600 copy when symlink is unavailable. No-op when src is
// missing (CI with env-var auth only) or dst already exists.
func linkFile(src, dst string) {
	if _, err := os.Lstat(dst); err == nil {
		return
	}
	if _, err := os.Stat(src); err != nil {
		return
	}
	if err := os.Symlink(src, dst); err == nil {
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = os.WriteFile(dst, data, 0o600)
}

// sameFilePath reports whether a and b name the same path after cleaning.
func sameFilePath(a, b string) bool {
	a, errA := filepath.Abs(a)
	b, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return a == b
}
