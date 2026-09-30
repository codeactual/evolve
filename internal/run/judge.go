// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/codeactual/evolve/internal/grade"
	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/model"
)

// HarnessJudge implements grade.Judge over a resolved judge selection. Each call
// runs one judge session that grades all of a case's llm assertions, built by the
// harness's JudgeSpec: in a fresh directory next to the workspace (never inside
// it, so nothing the agent planted there is loaded), with the workspace exposed
// read-only, and with the verdicts returned as schema-constrained structured
// output. Stateless per call, so safe under the sweep's eval concurrency.
type HarnessJudge struct {
	sel    harness.Selection
	eval   harness.EvalRunner
	cli    string
	runner Runner
	keep   bool // keep each judge directory after the call (--keep-workspaces)
}

// NewHarnessJudge binds a resolved judge selection to an executor. keep leaves
// every judge directory behind for debugging, like --keep-workspaces does for
// workspaces. It errors when the harness lacks eval support or its CLI is not on
// PATH — resolution failures surface at command start, never per-assertion.
func NewHarnessJudge(sel harness.Selection, r Runner, keep bool) (*HarnessJudge, error) {
	eval, ok := sel.Harness.(harness.EvalRunner)
	if !ok {
		return nil, fmt.Errorf("judge harness %s cannot run headless judge sessions", sel.Harness.ID())
	}
	cli, ok := harness.Available(sel.Harness)
	if !ok {
		return nil, fmt.Errorf("judge harness %s: CLI not found on PATH", sel.Harness.ID())
	}
	return &HarnessJudge{sel: sel, eval: eval, cli: cli, runner: r, keep: keep}, nil
}

// Judge runs one judge session over the workspace ws — the session grades all of
// a case's llm assertions at once — and returns the structured verdicts payload
// (ANSI-stripped). The session's directory is created beside ws (same parent,
// so the same retention rules apply) and removed afterward unless keep is set;
// a removal failure is logged and never masks the verdict.
func (j *HarnessJudge) Judge(ctx context.Context, ws, prompt string, timeout time.Duration) (string, error) {
	dir, err := os.MkdirTemp(filepath.Dir(ws), "judge.")
	if err != nil {
		return "", fmt.Errorf("creating the judge directory: %w", err)
	}
	if !j.keep {
		defer func() {
			if err := os.RemoveAll(dir); err != nil {
				slog.DebugContext(ctx, "judge directory not removed", slog.String("dir", dir), slog.Any("error", err))
			}
		}()
	}
	cliModelID, _ := j.sel.Model.CLIModelID(j.sel.Harness.ID())
	spec := j.eval.JudgeSpec(dir, model.JudgeInput{
		Prompt:    prompt,
		MaxTurns:  model.DefaultJudgeMaxTurns,
		Workspace: ws,
		Schema:    grade.VerdictSchema,
	}, cliModelID)
	spec.Argv[0] = j.cli
	res, err := j.runner.Run(ctx, spec, timeout, nil)
	if err != nil {
		return "", err
	}
	if res.TimedOut {
		return "", errors.New("timed out")
	}
	if reason := j.eval.RuntimeError(res.Stdout, res.ExitCode, res.TimedOut); reason != "" {
		return "", errors.New(reason)
	}
	payload, err := j.eval.ParseJudgeOutput(res.Stdout)
	if err != nil {
		return "", err
	}
	return ansi.Strip(string(payload)), nil
}

// UnavailableJudge fails every verdict with the resolution failure that made
// the (defaulted, never explicitly configured) judge unrunnable — the sweep
// still runs, and only llm assertions error.
type UnavailableJudge struct{ Reason string }

// Judge always errors with the resolution failure reason.
func (u UnavailableJudge) Judge(context.Context, string, string, time.Duration) (string, error) {
	return "", errors.New("judge unavailable: " + u.Reason)
}
