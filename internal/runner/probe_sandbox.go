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
	if !sb.Enabled {
		return nil
	}
	dir, err := os.MkdirTemp("", "evolve-sandbox-probe-")
	if err != nil {
		return fmt.Errorf("sandbox probe: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			slog.DebugContext(ctx, "sandbox probe dir not removed", slog.String("dir", dir), slog.Any("error", err))
		}
	}()
	res, err := (&Exec{Sandbox: sb}).Run(ctx, model.CommandSpec{Argv: []string{"/bin/true"}, Dir: dir},
		sandboxProbeTimeout, nil)
	switch {
	case err != nil:
		return fmt.Errorf("sandbox smoke run did not start: %w", err)
	case res.TimedOut:
		return fmt.Errorf("sandbox smoke run timed out after %s", sandboxProbeTimeout)
	case res.ExitCode != 0:
		return fmt.Errorf("sandbox smoke run failed (exit %d): %s", res.ExitCode, res.StderrTail)
	}
	return nil
}
