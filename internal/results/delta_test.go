// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package results

import "testing"

func TestEvalSummaryDelta(t *testing.T) {
	cur := EvalSummary{
		PassRate: new(0.8), AvgRunSeconds: new(20.0),
		Measured: &Measured{InputTokens: new(120), OutputTokens: new(30), CostUSD: new(0.5)},
	}
	prior := EvalSummary{
		PassRate: new(0.5), AvgRunSeconds: new(25.0),
		Measured: &Measured{InputTokens: new(100), OutputTokens: new(40), CostUSD: new(0.6)},
	}

	d := EvalSummaryDelta(cur, prior)
	if d.Rate == nil || *d.Rate != 0.3 {
		t.Errorf("rate delta = %v, want +0.3", d.Rate)
	}
	if d.AvgRunSeconds == nil || *d.AvgRunSeconds != -5.0 {
		t.Errorf("avg delta = %v, want -5.0", d.AvgRunSeconds)
	}
	if d.InputTokens == nil || *d.InputTokens != 20 {
		t.Errorf("input delta = %v, want +20", d.InputTokens)
	}
	if d.OutputTokens == nil || *d.OutputTokens != -10 {
		t.Errorf("output delta = %v, want -10", d.OutputTokens)
	}
	if d.CostUSD == nil || *d.CostUSD != -0.1 {
		t.Errorf("cost delta = %v, want -0.1", d.CostUSD)
	}
}

func TestDeltaNilSafety(t *testing.T) {
	// A missing figure on either side yields a nil delta, never a spurious zero.
	d := EvalSummaryDelta(EvalSummary{PassRate: new(1.0)}, EvalSummary{})
	if d.Rate != nil {
		t.Errorf("rate delta = %v, want nil (no prior)", d.Rate)
	}
	if !d.Zero() {
		t.Error("a delta with no comparable figure must report Zero")
	}
	// Equal values give an explicit zero delta (a real 'no change'), not nil.
	eq := EvalSummaryDelta(EvalSummary{PassRate: new(0.5)}, EvalSummary{PassRate: new(0.5)})
	if eq.Rate == nil || *eq.Rate != 0 {
		t.Errorf("equal rate delta = %v, want 0", eq.Rate)
	}
	if eq.Zero() {
		t.Error("a delta carrying a zero rate is not Zero")
	}
}

func TestTriggerCaseDeltaRate(t *testing.T) {
	// A should-trigger query: correctness is hits/runs. 3/3 vs 1/2 → +0.5.
	cur := TriggerResult{Hits: new(3), Runs: new(3)}
	prior := TriggerResult{Hits: new(1), Runs: new(2)}
	if d := TriggerCaseDelta(cur, prior, true); d.Rate == nil || *d.Rate != 0.5 {
		t.Errorf("should-trigger rate delta = %v, want +0.5", d.Rate)
	}
	// A should-not-trigger query: correctness is (runs-hits)/runs. Firing more is a
	// regression — 3/3 hits (0% correct) vs 1/2 hits (50% correct) → -0.5.
	if d := TriggerCaseDelta(cur, prior, false); d.Rate == nil || *d.Rate != -0.5 {
		t.Errorf("should-not-trigger rate delta = %v, want -0.5", d.Rate)
	}
	// Zero runs is not comparable.
	if got := TriggerCaseDelta(TriggerResult{Hits: new(0), Runs: new(0)}, prior, true); got.Rate != nil {
		t.Errorf("rate delta with zero runs = %v, want nil", got.Rate)
	}
}

func TestSnapshotEval(t *testing.T) {
	e := &EvalEntry{
		Header: Header{Executed: true, RanAt: "2026-06-11T00:00:00Z"},
		Results: []EvalResult{
			{
				ID: "a", Passed: new(true), Summary: &GradeSummary{PassRate: new(1.0)},
				Timing:       &Timing{ExecutorDurationSeconds: new(12.0)},
				Expectations: []GradedAssertion{{Text: "x", Passed: new(true)}},
			},
			{ID: "b", RuntimeError: "boom"},
		},
		Summary: EvalSummary{Passed: new(1), Total: 2},
	}
	snap := SnapshotEval(e)
	if snap == nil || len(snap.Results) != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
	a, ok := findEvalResult(snap.Results, "a")
	if !ok || a.Passed == nil || !*a.Passed || a.Summary == nil || a.Summary.PassRate == nil || *a.Summary.PassRate != 1.0 {
		t.Errorf("case a = %+v, want pass-rate projection", a)
	}
	// Bulky detail is trimmed out of the snapshot.
	if a.Expectations != nil {
		t.Errorf("case a expectations not trimmed: %+v", a.Expectations)
	}
	if b, _ := findEvalResult(snap.Results, "b"); b.RuntimeError == "" {
		t.Errorf("case b = %+v, want errored", b)
	}
	// An unexecuted entry has nothing meaningful to compare against.
	if SnapshotEval(&EvalEntry{Header: Header{Executed: false}}) != nil {
		t.Error("an unexecuted entry must snapshot to nil")
	}
}

func findEvalResult(rs []EvalResult, id string) (EvalResult, bool) {
	for _, r := range rs {
		if r.ID == id {
			return r, true
		}
	}
	return EvalResult{}, false
}

func TestSummarizeEvalResults(t *testing.T) {
	rs := []EvalResult{
		{ID: "a", Passed: new(true), Timing: &Timing{ExecutorDurationSeconds: new(10.0)}},
		{ID: "b", Passed: new(false), Timing: &Timing{ExecutorDurationSeconds: new(20.0)}},
		{ID: "c", RuntimeError: "boom"},
	}
	s := SummarizeEvalResults(rs)
	if s.Passed == nil || *s.Passed != 1 || s.Failed == nil || *s.Failed != 1 {
		t.Errorf("passed/failed = %v/%v, want 1/1", s.Passed, s.Failed)
	}
	if s.Errored == nil || *s.Errored != 1 {
		t.Errorf("errored = %v, want 1", s.Errored)
	}
	// Pass rate excludes the errored case: 1/(1+1) = 0.5.
	if s.PassRate == nil || *s.PassRate != 0.5 {
		t.Errorf("pass rate = %v, want 0.5", s.PassRate)
	}
	if s.AvgRunSeconds == nil || *s.AvgRunSeconds != 15.0 {
		t.Errorf("avg = %v, want 15.0", s.AvgRunSeconds)
	}
}
