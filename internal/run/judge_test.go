// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/model"
	"github.com/codeactual/evolve/internal/runner"
)

// specRunner records the spec it ran and returns a canned result.
type specRunner struct {
	gotSpec    model.CommandSpec
	gotTimeout time.Duration
	result     runner.Result
	err        error
}

func (s *specRunner) Run(_ context.Context, spec model.CommandSpec, timeout time.Duration, _ *runner.Scan) (runner.Result, error) {
	s.gotSpec, s.gotTimeout = spec, timeout
	return s.result, s.err
}

// fakeJudgeSelection binds the fake harness (CLI "sh", so it resolves on PATH)
// to its canonical model.
func fakeJudgeSelection() harness.Selection {
	p := &fakeEvalProvider{}
	return harness.Selection{Model: p.canonicalModel(), Harness: p}
}

func TestHarnessJudge(t *testing.T) {
	r := &specRunner{result: runner.Result{Stdout: []byte(`{"verdicts": [{"id": 1, "passed": true, "evidence": "e"}]}`)}}
	j, err := NewHarnessJudge(fakeJudgeSelection(), r, false)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	payload, err := j.Judge(context.Background(), ws, "the prompt", 7*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, `"passed": true`) {
		t.Errorf("payload = %q", payload)
	}
	// The spec is the harness's JudgeSpec with Argv[0] resolved to the installed
	// CLI — the fake harness's CLI is "sh", which LookPath resolves absolutely.
	if !strings.HasSuffix(r.gotSpec.Argv[0], "/sh") {
		t.Errorf("Argv[0] = %q, want resolved sh path", r.gotSpec.Argv[0])
	}
	if r.gotSpec.Argv[1] != "JUDGE" || r.gotSpec.Argv[2] != "the prompt" {
		t.Errorf("argv = %v, want the fake JudgeSpec shape", r.gotSpec.Argv)
	}
	if r.gotSpec.Argv[3] != strconv.Itoa(model.DefaultJudgeMaxTurns) {
		t.Errorf("MaxTurns = %s, want the judge turn ceiling %d", r.gotSpec.Argv[3], model.DefaultJudgeMaxTurns)
	}
	if !strings.Contains(r.gotSpec.Argv[4], `"verdicts"`) {
		t.Errorf("schema arg = %q, want the grade verdict schema", r.gotSpec.Argv[4])
	}
	if !slices.Contains(r.gotSpec.ReadPaths, ws) {
		t.Errorf("ReadPaths = %v, want the eval workspace as a read-only path", r.gotSpec.ReadPaths)
	}
	if r.gotTimeout != 7*time.Second {
		t.Errorf("timeout = %s, want 7s", r.gotTimeout)
	}
}

// TestHarnessJudgeSeparateDir pins that the judge never runs inside the
// agent-modified workspace: it gets its own directory next to it.
func TestHarnessJudgeSeparateDir(t *testing.T) {
	r := &specRunner{result: runner.Result{Stdout: []byte(`{}`)}}
	j, err := NewHarnessJudge(fakeJudgeSelection(), r, true)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if _, err := j.Judge(context.Background(), ws, "p", time.Second); err != nil {
		t.Fatal(err)
	}
	dir := r.gotSpec.Dir
	if dir == "" || dir == ws || strings.HasPrefix(dir, ws+string(filepath.Separator)) {
		t.Errorf("judge Dir = %q, must be neither the workspace %q nor inside it", dir, ws)
	}
	if filepath.Dir(dir) != filepath.Dir(ws) {
		t.Errorf("judge Dir %q does not share the workspace's parent %q", dir, filepath.Dir(ws))
	}
}

func TestHarnessJudgeRemovesDir(t *testing.T) {
	for _, keep := range []bool{false, true} {
		r := &specRunner{result: runner.Result{Stdout: []byte(`{}`)}}
		j, err := NewHarnessJudge(fakeJudgeSelection(), r, keep)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := j.Judge(context.Background(), t.TempDir(), "p", time.Second); err != nil {
			t.Fatal(err)
		}
		_, statErr := os.Stat(r.gotSpec.Dir)
		if keep && statErr != nil {
			t.Errorf("keep=true: judge dir %s was removed (%v), want it kept", r.gotSpec.Dir, statErr)
		}
		if !keep && !os.IsNotExist(statErr) {
			t.Errorf("keep=false: judge dir %s still exists (%v), want it removed", r.gotSpec.Dir, statErr)
		}
		if keep {
			if err := os.RemoveAll(r.gotSpec.Dir); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestHarnessJudgeErrors(t *testing.T) {
	tests := []struct {
		name   string
		runner *specRunner
		want   string
	}{
		{"run error", &specRunner{err: errors.New("exec blew up")}, "exec blew up"},
		{"timeout", &specRunner{result: runner.Result{TimedOut: true}}, "timed out"},
		// Empty stdout trips the fake harness's RuntimeError, mapping an unusable
		// judge session to an error instead of grading garbage.
		{"runtime error", &specRunner{result: runner.Result{ExitCode: 1}}, "empty CLI output"},
	}
	for _, tt := range tests {
		j, err := NewHarnessJudge(fakeJudgeSelection(), tt.runner, false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = j.Judge(context.Background(), t.TempDir(), "p", time.Second)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

func TestNewHarnessJudgeRejectsNonEvalHarness(t *testing.T) {
	// fakeTriggerProvider implements harness.Harness but not EvalRunner.
	sel := harness.Selection{Model: model.Model{ID: "fake/model-1"}, Harness: &fakeTriggerProvider{}}
	if _, err := NewHarnessJudge(sel, &specRunner{}, false); err == nil ||
		!strings.Contains(err.Error(), "cannot run headless judge sessions") {
		t.Errorf("err = %v, want headless-judge rejection", err)
	}
}

func TestUnavailableJudge(t *testing.T) {
	_, err := UnavailableJudge{Reason: "no harness installed"}.Judge(context.Background(), "", "", 0)
	if err == nil || !strings.Contains(err.Error(), "judge unavailable: no harness installed") {
		t.Errorf("err = %v, want the resolution reason", err)
	}
}
