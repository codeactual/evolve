// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package run

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/model"
	"github.com/codeactual/evolve/internal/runner"
)

// PostureTimeout bounds one posture probe: the probes start a CLI and read its
// session surface (or its feature table), which takes about a second.
const PostureTimeout = 30 * time.Second

// CheckPosture verifies, before any agent run, that h's configured session
// surface is local-only: it runs the harness's posture probe through r — the same
// runner, sandbox and environment real agent runs use — and returns the harness's
// verdict. A harness without the harness.PostureChecker capability has nothing to
// check and returns nil. Claude's probe starts a real session and is cancelled at
// its init event, before the session does anything; Codex's lists its feature
// table and starts no session.
func CheckPosture(ctx context.Context, r Runner, h harness.Harness, cliModelID string,
	inner model.InnerSandbox, timeout time.Duration,
) error {
	pc, ok := h.(harness.PostureChecker)
	if !ok {
		return nil
	}
	cli, ok := harness.Available(h)
	if !ok {
		return fmt.Errorf("%s posture check: CLI not found on PATH", h.ID())
	}
	ws, err := os.MkdirTemp("", "evolve-posture-")
	if err != nil {
		return fmt.Errorf("%s posture check: %w", h.ID(), err)
	}
	defer func() { _ = os.RemoveAll(ws) }() // best-effort scratch cleanup

	spec := pc.PostureSpec(ws, cliModelID, inner)
	spec.Argv[0] = cli
	var out bytes.Buffer
	scan := &runner.Scan{OnLine: func(line []byte) bool {
		out.Write(line)
		return pc.PostureDone(line)
	}}
	res, err := r.Run(ctx, spec, timeout, scan)
	if err != nil {
		return fmt.Errorf("%s posture check did not run: %w", h.ID(), err)
	}
	if res.TimedOut {
		return fmt.Errorf("%s posture check timed out after %s", h.ID(), timeout)
	}
	if err := pc.CheckPosture(out.Bytes()); err != nil {
		if tail := strings.TrimSpace(res.StderrTail); tail != "" {
			return fmt.Errorf("%w (stderr: %s)", err, tail)
		}
		return err
	}
	return nil
}
