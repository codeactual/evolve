// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"testing"

	"github.com/codeactual/evolve/internal/evalspec"
	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/model"
	"github.com/codeactual/evolve/internal/plan"
)

// These benchmarks bound the cost of one dashboard frame. The live run drives a
// spinner that ticks ~10×/second, and bubbletea recomputes View() on every tick
// regardless of whether any subprocess produced output — so the steady-state CPU
// the evolve process burns while agents wait on the model API is roughly
// (one view() cost) × 10. buildNodeRefs/rollupRows/groupMetric isolate where that
// per-frame cost goes: tree flattening vs the two O(total-cases) aggregations that
// re-fold every case's metrics on each frame.

// benchShape sizes a synthetic plan. totalCases ≈ plugins·skills·models·(triggers+evals).
type benchShape struct {
	name                                     string
	plugins, skills, models, triggers, evals int
}

// benchShapes span a small single-skill run up to a marketplace-scale `run all`
// matrix; the case counts in the names are the resulting terminal-case totals.
var benchShapes = []benchShape{
	{"small_36cases", 1, 3, 2, 3, 3},
	{"medium_320cases", 1, 8, 4, 5, 5},
	{"large_1728cases", 2, 12, 6, 6, 6},
}

func benchCatalog(plugins, skills, triggers, evals int) []plan.SkillCatalog {
	var cat []plan.SkillCatalog
	for pi := range plugins {
		for si := range skills {
			skill := fmt.Sprintf("p%d-skill%d", pi, si)
			trg := make([]evalspec.Trigger, triggers)
			for ti := range trg {
				trg[ti] = evalspec.Trigger{Query: fmt.Sprintf("query %d", ti), ShouldTrigger: ti%2 == 0}
			}
			ev := make([]evalspec.Eval, evals)
			for ei := range ev {
				ev[ei] = evalspec.Eval{ID: fmt.Sprintf("e%d", ei), Prompt: "do the thing"}
			}
			cat = append(cat, plan.SkillCatalog{
				Plugin: fmt.Sprintf("plugin%d", pi), Skill: skill,
				Title: skill, Description: "bench skill", Triggers: trg, Evals: ev,
			})
		}
	}
	return cat
}

func benchModels(n int) []harness.Selection {
	p := fakeProv{}
	sels := make([]harness.Selection, n)
	for i := range sels {
		sels[i] = harness.Selection{Harness: p, Model: model.Model{
			ID: fmt.Sprintf("fake/m%d", i), ProviderID: "fake", Name: fmt.Sprintf("Model %d", i),
			Supported: map[string]string{"fake": fmt.Sprintf("m%d", i)}, Preferred: "fake",
		}}
	}
	return sels
}

// seedMetrics settles every case with a fresh result. This is the worst-case
// steady-state frame: once cases finish, each group reads as started/done, so
// every plugin/skill/model header (and every rollup row) folds its units' metrics
// on render rather than short-circuiting on "pending".
func seedMetrics(d *dashboardModel) {
	for _, u := range d.units {
		u.status = stPass
		for _, c := range u.cases {
			c.status = stPass
			c.liveDone = true
			c.metrics.AvgRunSeconds = new(2.5)
			c.metrics.InputTokens = new(1400)
			c.metrics.OutputTokens = new(320)
			c.metrics.CacheReadTokens = new(12000)
			c.metrics.CacheCreationTokens = new(800)
			c.metrics.CostUSD = new(0.0042)
			switch c.kind {
			case plan.KindTriggers:
				c.metrics.Hits = new(3)
				c.metrics.Runs = new(3)
			case plan.KindEvals:
				c.metrics.AssertPassed = new(4)
				c.metrics.AssertTotal = new(5)
			}
		}
	}
}

func benchDashboard(tb testing.TB, s benchShape) dashboardModel {
	tb.Helper()
	cat := benchCatalog(s.plugins, s.skills, s.triggers, s.evals)
	models := benchModels(s.models)
	d := dashFromFilter(cat, models, nil, plan.PriorMetrics{})
	seedMetrics(&d)
	d.w, d.h = 140, 48 // a roomy terminal: more visible rows = more rendering per frame
	return d
}

var (
	viewSink string
	nodeSink []nodeRef
	rowSink  []skillRow
)

// BenchmarkDashboardView measures one full frame: the cost paid ~10×/second for
// the whole run, even while every agent subprocess is idle waiting on the API.
func BenchmarkDashboardView(b *testing.B) {
	for _, s := range benchShapes {
		d := benchDashboard(b, s)
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				viewSink = d.view()
			}
		})
	}
}

// BenchmarkBuildNodeRefs isolates the tree flattening view() runs each frame.
func BenchmarkBuildNodeRefs(b *testing.B) {
	for _, s := range benchShapes {
		d := benchDashboard(b, s)
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				nodeSink = d.buildNodeRefs()
			}
		})
	}
}

// BenchmarkRollupRows isolates the Rollup pane's per-(skill,model) aggregation,
// which re-folds every case's metrics (skillRows → aggregateGroup) each frame.
func BenchmarkRollupRows(b *testing.B) {
	for _, s := range benchShapes {
		d := benchDashboard(b, s)
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				rowSink = d.rollupRows()
			}
		})
	}
}

// BenchmarkGroupMetricPlugin isolates the Execution pane's per-header rollup: a
// single collapsed plugin row still folds every case beneath it on each frame.
func BenchmarkGroupMetricPlugin(b *testing.B) {
	for _, s := range benchShapes {
		d := benchDashboard(b, s)
		units := d.pluginUnits(0)
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				viewSink, _ = d.groupMetric(units)
			}
		})
	}
}

// BenchmarkRenderRuns guards the Runs-pane window: the live profile showed it was
// the single largest CPU consumer because it rendered every execLog entry per
// frame and kept ~7. Cost must stay flat across shapes (O(visible)), not grow with
// the execution count. A mid-list selection forces both ▲/▼ indicators.
func BenchmarkRenderRuns(b *testing.B) {
	for _, s := range benchShapes {
		d := benchDashboard(b, s)
		d.started = true
		d.runSel = len(d.execLog) / 2
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				viewSink = d.renderRuns(60, 7)
			}
		})
	}
}

// BenchmarkRenderLeft guards the Execution-pane window over a fully expanded tree
// (the worst case: every plugin/skill/model/case row visible). Like the Runs pane,
// cost must stay O(visible) rather than scaling with the node count.
func BenchmarkRenderLeft(b *testing.B) {
	for _, s := range benchShapes {
		d := benchDashboard(b, s)
		nodes := d.buildNodeRefsWith(func(nodeKey) bool { return true })
		hl := len(nodes) / 2
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				viewSink = d.renderLeftBody(nodes, hl, 60, 30)
			}
		})
	}
}
