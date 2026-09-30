// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

//go:build linux

package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/codeactual/evolve/internal/model"
)

// sandboxProbeTimeout bounds one sandbox probe; starting bubblewrap takes
// milliseconds, so a slow probe means something is wrong.
const sandboxProbeTimeout = 15 * time.Second

// ProbeSandbox proves that sb works on this host by running a trivial command
// inside it (the outer smoke run): bubblewrap passes provenance, unprivileged
// user namespaces are available, and the fresh root is usable. It returns
// nil for a disabled sandbox, which has nothing to probe. The error carries the
// probe's stderr so the operator sees why.
func ProbeSandbox(ctx context.Context, sb Sandbox) error {
	return probeInSandbox(ctx, sb, []string{"/bin/true"}, "sandbox smoke run")
}

// nestedProbeScript starts a bubblewrap inside the outer sandbox, resolved on
// the sandbox's own PATH exactly as the agent CLIs resolve theirs, and has a
// grandchild create one more user namespace, which is what Claude Code's
// seccomp helper does for every shell command it runs.
const nestedProbeScript = "bwrap --unshare-all --ro-bind / / --proc /proc --dev /dev -- unshare --user --map-root-user true"

var nestedProbeArgv = []string{"/bin/sh", "-c", nestedProbeScript}

// ProbeNested proves that the agent CLIs' own sandboxes can nest inside sb: it
// runs a bubblewrap inside the outer sandbox and a further user namespace
// inside that. It fails when nested user or mount namespaces are unavailable,
// notably on hosts where AppArmor (kernel.apparmor_restrict_unprivileged_userns=1)
// strips the capabilities of the descendants of a bubblewrap launched from a
// profiled path such as /usr/bin/bwrap: point sandbox.bwrap_path at an
// unprofiled copy. It returns nil for a disabled sandbox.
func ProbeNested(ctx context.Context, sb Sandbox) error {
	return probeInSandbox(ctx, sb, nestedProbeArgv, "nested bubblewrap probe")
}

// probeInSandbox runs argv inside sb in a scratch run directory and reports a
// failure with what names the probe.
func probeInSandbox(ctx context.Context, sb Sandbox, argv []string, what string) error {
	if !sb.Enabled {
		return nil
	}
	dir, err := os.MkdirTemp("", "evolve-sandbox-probe-")
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			slog.DebugContext(ctx, "sandbox probe dir not removed", slog.String("dir", dir), slog.Any("error", err))
		}
	}()
	res, err := (&Exec{Sandbox: sb}).Run(ctx, model.CommandSpec{Argv: append([]string(nil), argv...), Dir: dir},
		sandboxProbeTimeout, nil)
	switch {
	case err != nil:
		return fmt.Errorf("%s did not start: %w", what, err)
	case res.TimedOut:
		return fmt.Errorf("%s timed out after %s", what, sandboxProbeTimeout)
	case res.ExitCode != 0:
		return fmt.Errorf("%s failed (exit %d): %s", what, res.ExitCode, res.StderrTail)
	}
	return nil
}
