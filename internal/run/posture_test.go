// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package run

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codeactual/evolve/internal/model"
	"github.com/codeactual/evolve/internal/runner"
)

// postureHarness is a fake harness with the posture capability: the probe's
// stdout is "ready\n" then "extra\n"; PostureDone fires on "ready", and
// CheckPosture accepts only output containing "ready".
type postureHarness struct {
	fakeEvalProvider
	verdictErr error
	sawOutput  string
}

func (p *postureHarness) PostureSpec(ws, cliModelID string, _ model.InnerSandbox) model.CommandSpec {
	return model.CommandSpec{Argv: []string{"posture-cli", cliModelID}, Dir: ws}
}

func (p *postureHarness) PostureDone(line []byte) bool {
	return strings.TrimSpace(string(line)) == "ready"
}

func (p *postureHarness) CheckPosture(stdout []byte) error {
	p.sawOutput = string(stdout)
	if p.verdictErr != nil {
		return p.verdictErr
	}
	if !strings.Contains(string(stdout), "ready") {
		return errors.New("surface never appeared")
	}
	return nil
}

// scriptedRunner feeds scripted lines through the scan like the real runner:
// it stops feeding once OnLine reports a hit.
type scriptedRunner struct {
	lines   []string
	res     runner.Result
	err     error
	gotSpec model.CommandSpec
	fed     int
}

func (s *scriptedRunner) Run(_ context.Context, spec model.CommandSpec, _ time.Duration, scan *runner.Scan) (runner.Result, error) {
	s.gotSpec = spec
	for _, l := range s.lines {
		s.fed++
		if scan.OnLine([]byte(l + "\n")) {
			s.res.Hit = true
			break
		}
	}
	return s.res, s.err
}

func TestCheckPostureAccepts(t *testing.T) {
	h := &postureHarness{}
	r := &scriptedRunner{lines: []string{"noise", "ready", "extra"}}
	if err := CheckPosture(context.Background(), r, h, "model-1", model.InnerSandbox{}, time.Second); err != nil {
		t.Fatalf("CheckPosture = %v", err)
	}
	if r.fed != 2 || strings.Contains(h.sawOutput, "extra") {
		t.Errorf("probe fed %d lines and the verdict saw %q; want it ended at the ready line", r.fed, h.sawOutput)
	}
	if !strings.HasSuffix(r.gotSpec.Argv[0], "/sh") || r.gotSpec.Argv[1] != "model-1" {
		t.Errorf("probe argv = %v, want the CLI resolved and the model passed through", r.gotSpec.Argv)
	}
}

func TestCheckPostureViolationAndFailures(t *testing.T) {
	violation := errors.New("claude session is not local-only: outward tool WebFetch")
	if err := CheckPosture(context.Background(), &scriptedRunner{lines: []string{"ready"}}, &postureHarness{verdictErr: violation},
		"m", model.InnerSandbox{}, time.Second); !errors.Is(err, violation) {
		t.Errorf("violation = %v, want it returned", err)
	}
	r := &scriptedRunner{lines: []string{"ready"}, res: runner.Result{StderrTail: "auth blocked"}}
	err := CheckPosture(context.Background(), r, &postureHarness{verdictErr: errors.New("no init event")}, "m", model.InnerSandbox{}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "auth blocked") {
		t.Errorf("a verdict failure must carry the CLI's stderr tail, got %v", err)
	}
	if err := CheckPosture(context.Background(), &scriptedRunner{err: errors.New("boom")}, &postureHarness{}, "m", model.InnerSandbox{}, time.Second); err == nil ||
		!strings.Contains(err.Error(), "did not run") {
		t.Errorf("a runner error = %v, want a did-not-run error", err)
	}
	if err := CheckPosture(context.Background(), &scriptedRunner{res: runner.Result{TimedOut: true}}, &postureHarness{}, "m", model.InnerSandbox{}, time.Second); err == nil ||
		!strings.Contains(err.Error(), "timed out") {
		t.Errorf("a timeout = %v, want a timed-out error", err)
	}
}

func TestCheckPostureSkipsHarnessWithoutCapability(t *testing.T) {
	// fakeEvalProvider implements no PostureChecker.
	if err := CheckPosture(context.Background(), &scriptedRunner{}, &fakeEvalProvider{}, "m", model.InnerSandbox{}, time.Second); err != nil {
		t.Errorf("CheckPosture without the capability = %v, want nil", err)
	}
}
