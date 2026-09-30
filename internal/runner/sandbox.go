// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/codeactual/evolve/internal/model"
)

// Sandbox confines an agent run to a deny-by-default filesystem. When Enabled,
// every command is wrapped in bubblewrap with a fresh root that shows only:
// the system directories, the agent executable, the repository under test
// (read-only), the operator's git config files (read-only), the spec's
// ReadPaths (read-only), the operator's grants, and the run directory. The
// operator's home directory, other checkouts, and everything else on the host
// are absent. The network stays shared: agents and dependency tooling need it.
//
// Build one with NewSandbox, which validates the operator's grants; a zero
// Sandbox is disabled and runs commands unconfined.
type Sandbox struct {
	Enabled    bool
	RepoRoot   string   // the repository under test, bound read-only
	ReadPaths  []string // operator grants bound read-only (sandbox.read_paths)
	WritePaths []string // operator grants bound read-write (sandbox.write_paths)
	BwrapPath  string   // operator-chosen bubblewrap ("" = the one on PATH)
}

// SandboxConfig is the operator's sandbox policy as configured, before
// validation. Grants may use ~ and environment variables.
type SandboxConfig struct {
	RepoRoot   string
	ReadPaths  []string
	WritePaths []string
	BwrapPath  string
}

// NewSandbox validates cfg and returns an enabled Sandbox. Every grant is
// expanded (~ and environment variables), must then be absolute and exist, and
// may not be / or an ancestor of the operator's home directory: that refusal
// keeps one typo from reopening every home directory. Granting the home
// directory itself, or anything inside it, is allowed.
func NewSandbox(cfg SandboxConfig) (Sandbox, error) {
	home, _ := os.UserHomeDir()
	s := Sandbox{Enabled: true, RepoRoot: resolvePath(cfg.RepoRoot), BwrapPath: cfg.BwrapPath}
	for _, g := range []struct {
		key     string
		entries []string
		out     *[]string
	}{
		{"sandbox.read_paths", cfg.ReadPaths, &s.ReadPaths},
		{"sandbox.write_paths", cfg.WritePaths, &s.WritePaths},
	} {
		for _, entry := range g.entries {
			path, err := validateGrant(g.key, entry, home)
			if err != nil {
				return Sandbox{}, err
			}
			*g.out = append(*g.out, path)
		}
	}
	return s, nil
}

// validateGrant expands and checks one operator grant, returning its resolved
// absolute path.
func validateGrant(key, entry, home string) (string, error) {
	expanded := os.ExpandEnv(entry)
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		if home == "" {
			return "", fmt.Errorf("%s entry %q: cannot expand ~ without a home directory", key, entry)
		}
		expanded = filepath.Join(home, strings.TrimPrefix(expanded, "~"))
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("%s entry %q: must be an absolute path after expanding ~ and environment variables (got %q)",
			key, entry, expanded)
	}
	expanded = filepath.Clean(expanded)
	if _, err := os.Stat(expanded); err != nil {
		return "", fmt.Errorf("%s entry %q: %w", key, entry, err)
	}
	path := resolvePath(expanded)
	if home != "" {
		realHome := resolvePath(home)
		if path == "/" || (path != realHome && strings.HasPrefix(realHome, path+string(os.PathSeparator))) {
			return "", fmt.Errorf("%s entry %q: refusing to expose %s, an ancestor of your home directory %s; "+
				"grant the specific directories you need", key, entry, path, realHome)
		}
	}
	return path, nil
}

// wrap returns the command line to execute for spec: its argv prefixed with
// the bubblewrap launcher and the deny-by-default policy. A disabled sandbox
// returns the argv unchanged. wrap fails closed: an enabled sandbox that cannot
// be constructed (no validated bubblewrap, no run directory, unresolvable
// executable) returns an error rather than silently running the agent
// unconfined.
func (s Sandbox) wrap(spec model.CommandSpec) ([]string, error) {
	if !s.Enabled {
		return spec.Argv, nil
	}
	bwrap, err := ResolveBwrap(s.BwrapPath)
	if err != nil {
		return nil, err
	}
	if spec.Dir == "" {
		return nil, fmt.Errorf("sandboxed run has no run directory: the agent needs one writable directory")
	}
	if len(spec.Argv) == 0 {
		return nil, fmt.Errorf("sandboxed run has an empty command")
	}
	plan := s.plan(spec, bwrap)
	return sandboxArgv(plan, hostRoot{root: "/"}), nil
}

// resolvePath makes p absolute and resolves symlinks, so the rules match the
// canonical path the kernel enforces against — a rule written against a
// symlinked path (a symlinked $TMPDIR, say) would never match. An unresolvable
// path (e.g. not yet created) falls back to its absolute form.
func resolvePath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func resolvePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if r := resolvePath(p); r != "" {
			out = append(out, r)
		}
	}
	return out
}
