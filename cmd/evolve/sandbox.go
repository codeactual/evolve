// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/layout"
	"github.com/codeactual/evolve/internal/model"
	"github.com/codeactual/evolve/internal/run"
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

// sandboxEnvPassthrough is the operator's sandbox.env_passthrough: names of
// environment variables, beyond the baseline, that agent processes may inherit
// (GOPATH, GOFLAGS, a build-tool proxy setting, ...). Operator-only, like the
// rest of the sandbox block.
func sandboxEnvPassthrough() []string {
	if opts.Viper == nil {
		return nil
	}
	return opts.Viper.GetStringSlice("sandbox.env_passthrough")
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

// innerSandboxConfig reads the operator's settings for the agent CLIs' own
// sandboxes (sandbox.claude_allowed_domains, sandbox.codex_network_access) and
// rejects values they cannot honor. The inner sandboxes are always on, so this
// applies whether or not the outer sandbox is enabled.
func innerSandboxConfig() (model.InnerSandbox, error) {
	var in model.InnerSandbox
	if opts.Viper != nil {
		in.ClaudeAllowedDomains = opts.Viper.GetStringSlice("sandbox.claude_allowed_domains")
		in.CodexNetworkAccess = opts.Viper.GetBool("sandbox.codex_network_access")
	}
	return in, in.Validate()
}

// nestedProbe proves the agent CLIs' own sandboxes can nest inside evolve's; a
// variable so tests can fake the host.
var nestedProbe = runner.ProbeNested

var (
	preflightOnce sync.Once
	preflightErr  error
)

// preflightSandbox fails closed before any agent starts: with the outer sandbox
// enabled, the agent CLIs' own sandboxes must be able to start inside it. One
// probe per process, since the verdict is a fact about the host. Otherwise every
// agent run would fail on its own, noisier and after paying for the run. It
// does nothing when the outer sandbox is disabled: the inner layers then refuse
// to run without their own prerequisites (Claude through failIfUnavailable,
// Codex through its own bubblewrap check).
func preflightSandbox(ctx context.Context, sb runner.Sandbox) error {
	if !sb.Enabled {
		return nil
	}
	preflightOnce.Do(func() { preflightErr = nestedProbe(ctx, sb) })
	if preflightErr == nil {
		return nil
	}
	return fmt.Errorf("the agent CLIs' own sandboxes cannot start inside evolve's sandbox: %w\n"+
		"Claude Code and Codex nest a second bubblewrap per command, which needs nested user namespaces. "+
		"On Ubuntu 24.04+ (kernel.apparmor_restrict_unprivileged_userns=1) AppArmor's bwrap-userns-restrict profile "+
		"strips that capability from a bubblewrap launched from /usr/bin/bwrap: copy bubblewrap to a root- or "+
		"operator-owned directory outside the profile (not writable by group or others) and point "+
		"sandbox.bwrap_path (EVOLVE_SANDBOX_BWRAP_PATH, --bwrap-path) at the copy. "+
		"Run `evolve doctor` for the full diagnosis", preflightErr)
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
	if bwrap != "/usr/bin/bwrap" && cfg.BwrapPath != "" {
		lines = append(lines, "  note: a copy at a custom path does not follow package upgrades of bubblewrap; refresh it by hand")
	}
	sb, err := runner.NewSandbox(cfg)
	if err != nil {
		return append(lines, fmt.Sprintf("sandbox policy: INVALID — %v", err))
	}
	if err := runner.ProbeSandbox(ctx, sb); err != nil {
		return append(lines, fmt.Sprintf("outer sandbox: FAILED — %v", err))
	}
	lines = append(lines, "outer sandbox: ok (smoke run passed)")
	if err := runner.ProbeNested(ctx, sb); err != nil {
		lines = append(lines,
			fmt.Sprintf("nested bubblewrap: FAILED — %v", err),
			"  The agent CLIs nest a second bubblewrap per command, so `evolve run` refuses to start. On Ubuntu 24.04+",
			"  (kernel.apparmor_restrict_unprivileged_userns=1) copy bubblewrap to a root- or operator-owned directory",
			"  outside /usr/bin (not writable by group or others) and set sandbox.bwrap_path to the copy.")
	} else {
		lines = append(lines, "nested bubblewrap: ok (the agent CLIs' own sandboxes can start)")
	}
	if _, err := exec.LookPath("socat"); err != nil {
		lines = append(lines, "socat: MISSING — Claude Code's sandbox needs it for network filtering (install socat)")
	} else {
		lines = append(lines, "socat: ok")
	}
	return lines
}

// checkPosture verifies one harness's local-only surface; a variable so tests can
// fake the host.
var checkPosture = run.CheckPosture

var (
	postureMu       sync.Mutex
	postureVerdicts = map[string]error{}
)

// preflightPosture fails closed before any agent starts: every harness the run
// will drive must present a local-only session surface (no web tools, remote
// triggers, MCP servers, connectors or marketplace plugins). evolve evaluates
// first-party skills that are already on the filesystem, so nothing outward is
// wanted, and a CLI update that adds an outward tool or renames a flag fails here,
// at the start of the run, instead of mid-sweep. One probe per harness per
// process, since the verdict is a fact about the installed CLI.
func preflightPosture(ctx context.Context, r run.Runner, selected []harness.Selection, inner model.InnerSandbox) error {
	postureMu.Lock()
	defer postureMu.Unlock()
	done := map[string]bool{}
	for _, sel := range selected {
		id := sel.Harness.ID()
		if done[id] {
			continue
		}
		done[id] = true
		verdict, seen := postureVerdicts[id]
		if !seen {
			cliModelID, _ := sel.Model.CLIModelID(id)
			verdict = checkPosture(ctx, r, sel.Harness, cliModelID, inner, run.PostureTimeout)
			postureVerdicts[id] = verdict
		}
		if verdict != nil {
			return fmt.Errorf("the %s agent session is not local-only, so the run is refused: %w\n"+
				"evolve runs first-party skills from the filesystem and keeps agents from reaching outward "+
				"(web tools, remote triggers, MCP servers, connectors, marketplace plugins). "+
				"Run `evolve doctor` for the full posture report", id, verdict)
		}
	}
	return nil
}

// postureDoctorLines renders the doctor's posture section: for each installed
// harness, whether its configured session surface is local-only.
func postureDoctorLines(ctx context.Context, harnesses []harness.Harness) []string {
	root := opts.Root
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	r := &runner.Exec{EnvPassthrough: sandboxEnvPassthrough()}
	note := ""
	if sandboxEnabled(false) {
		sb, err := runner.NewSandbox(sandboxConfig(root))
		if err != nil {
			return []string{fmt.Sprintf("posture checks skipped: the sandbox policy is invalid (%v)", err)}
		}
		if _, err := runner.ResolveBwrap(sb.BwrapPath); err != nil {
			return []string{"posture checks skipped: bubblewrap is unavailable (see above)"}
		}
		r.Sandbox = sb
		note = " (inside the sandbox)"
	}
	inner, err := innerSandboxConfig()
	if err != nil {
		return []string{fmt.Sprintf("posture checks skipped: %v", err)}
	}
	var lines []string
	for _, h := range harnesses {
		if _, ok := h.(harness.PostureChecker); !ok {
			continue
		}
		if _, ok := harness.Available(h); !ok {
			lines = append(lines, fmt.Sprintf("%s: skipped (CLI not on PATH)", h.ID()))
			continue
		}
		cliModelID := ""
		for _, m := range model.AllModels(nil) {
			if id, ok := m.CLIModelID(h.ID()); ok {
				cliModelID = id
				break
			}
		}
		if err := checkPosture(ctx, r, h, cliModelID, inner, run.PostureTimeout); err != nil {
			lines = append(lines, fmt.Sprintf("%s: VIOLATION — %v", h.ID(), err))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: local-only%s", h.ID(), note))
	}
	return lines
}
