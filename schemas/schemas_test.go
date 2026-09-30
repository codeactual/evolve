// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package schemas_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/codeactual/evolve/internal/encfmt"
	"github.com/codeactual/evolve/internal/evalspec"
	"github.com/codeactual/evolve/internal/report"
	"github.com/codeactual/evolve/internal/results"
	"github.com/codeactual/evolve/schemas"
)

const idBase = "https://raw.githubusercontent.com/codeactual/evolve/main/schemas/"

// compile builds every embedded schema, registered under its published $id
// so cross-file refs resolve offline. Compiling them all is itself the
// self-check that every schema is valid 2020-12 and every $ref resolves.
func compile(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	entries, err := schemas.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := schemas.FS.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		if err := c.AddResource(idBase+e.Name(), doc); err != nil {
			t.Fatalf("add %s: %v", e.Name(), err)
		}
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, e := range entries {
		sch, err := c.Compile(idBase + e.Name())
		if err != nil {
			t.Fatalf("compile %s: %v", e.Name(), err)
		}
		compiled[e.Name()] = sch
	}
	return compiled
}

func validate(sch *jsonschema.Schema, data []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}

// TestSkillCreatorExamplesValidate is the superset guarantee as an
// executable test: the verbatim examples from skill-creator's
// references/schemas.md validate against evolve's schemas unchanged.
func TestSkillCreatorExamplesValidate(t *testing.T) {
	compiled := compile(t)
	for example, schema := range map[string]string{
		"evals.json":     "evals.schema.json",
		"grading.json":   "grading.schema.json",
		"metrics.json":   "metrics.schema.json",
		"timing.json":    "timing.schema.json",
		"benchmark.json": "benchmark.schema.json",
		"history.json":   "history.schema.json",
	} {
		data, err := os.ReadFile(filepath.Join("testdata", "skill-creator", example))
		if err != nil {
			t.Fatal(err)
		}
		if err := validate(compiled[schema], data); err != nil {
			t.Errorf("skill-creator %s does not validate against %s:\n%v", example, schema, err)
		}
	}
}

// TestFixtureDefinitionsValidate sweeps every authored eval definition in
// the e2e fixture repositories, in whatever format, through the schemas —
// and through the Go loader, so schema and loader agree on validity.
func TestFixtureDefinitionsValidate(t *testing.T) {
	compiled := compile(t)
	found := 0
	root := filepath.Join("..", "e2e", "repos")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		stem, _, _ := strings.Cut(d.Name(), ".")
		if stem != "evals" && stem != "triggers" {
			return nil
		}
		found++
		data, err := encfmt.NormalizeToJSON(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return nil
		}
		if err := validate(compiled[stem+".schema.json"], data); err != nil {
			t.Errorf("%s does not validate: %v", path, err)
		}
		if stem == "evals" {
			if _, err := evalspec.LoadEvals(path); err != nil {
				t.Errorf("%s does not load: %v", path, err)
			}
		} else if _, err := evalspec.LoadTriggers(path); err != nil {
			t.Errorf("%s does not load: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no eval definitions found under e2e/repos")
	}
}

// maximalResults exercises every optional field the schema declares for
// wave 1, plus explicit nulls (pricing, skipped passed).
func maximalResults() *results.File {
	f := &results.File{Schema: results.Schema, Plugin: "p", Skill: "s"}
	f.SetTrigger("anthropic/claude-fable-5", &results.TriggerEntry{
		Header: results.Header{
			Provider: "anthropic", Model: "claude-fable-5", Display: "Claude Fable 5",
			ToolVersion: "test", RanAt: "2026-06-12T10:00:00Z", Executed: true,
			RunsPerQuery: 3, TimeoutSeconds: 120,
			Pricing: &results.Pricing{InputPerMTok: new(10.0), OutputPerMTok: new(50.0)},
		},
		Results: []results.TriggerResult{{
			Query: "q", ShouldTrigger: true, Hits: new(3), Runs: new(3),
			Passed: new(true), AvgRunSeconds: new(9.1),
			Estimate: &results.Estimate{InputTokens: 1385, InputCostUSD: new(0.01385)},
		}},
		Summary: results.TriggerSummary{
			Passed: new(1), Failed: new(0), Total: 1,
			PassRate: new(1.0), AvgRunSeconds: new(9.1),
			Estimate: &results.Estimate{InputTokens: 1385, InputCostUSD: new(0.01385)},
		},
		Previous: &results.TriggerSnapshot{
			RanAt: "2026-06-11T10:00:00Z",
			Summary: results.TriggerSummary{
				Passed: new(0), Failed: new(1), Total: 1,
				PassRate: new(0.0), AvgRunSeconds: new(8.0),
			},
			Results: []results.TriggerResult{
				{Query: "q", ShouldTrigger: true, Hits: new(1), Runs: new(3), Passed: new(false), AvgRunSeconds: new(8.0)},
			},
		},
	})
	graded := []results.GradedAssertion{
		{
			Assertion: evalspec.Assertion{Type: "command", Run: "go test ./...", Requires: "go", ExpectExit: new(0)},
			Text:      "command `go test ./...` exits 0", Passed: nil,
			Evidence: "skipped: go not installed", Source: "assertion",
		},
		{
			Assertion: evalspec.Assertion{Type: "llm", Text: "The output includes X"},
			Text:      "The output includes X", Passed: new(true),
			Evidence: "found X", Source: "expectation",
		},
	}
	f.SetEval("openai/gpt-5.3-codex-spark", &results.EvalEntry{
		Header: results.Header{
			Provider: "openai", Model: "gpt-5.3-codex-spark", Display: "GPT-5.3 Codex Spark",
			ToolVersion: "test", RanAt: "2026-06-12T11:00:00Z", Executed: true,
			TimeoutSeconds: 600, Pricing: nil, // explicit null in the file
		},
		Results: []results.EvalResult{{
			ID: "1", Name: "Ocean", Passed: new(true),
			Estimate:     &results.Estimate{InputTokens: 1827, InputCostUSD: new(0.01827)},
			Measured:     &results.Measured{InputTokens: new(100), CacheReadTokens: new(880), CacheCreationTokens: new(40), OutputTokens: new(10), CostUSD: new(0.0003)},
			Expectations: graded,
			Summary:      results.SummarizeExpectations(graded),
			ExecutionMetrics: &results.ExecutionMetrics{
				ToolCalls: map[string]int{"Read": 5}, TotalToolCalls: new(5),
				TotalSteps: new(2), FilesCreated: []string{"out.txt"},
				ErrorsEncountered: new(0), OutputChars: new(120), TranscriptChars: new(3200),
			},
			Timing: &results.Timing{
				TotalTokens: new(1030), DurationMS: new(23332), TotalDurationSeconds: new(23.3),
				ExecutorStart: "2026-06-12T11:00:00Z", ExecutorEnd: "2026-06-12T11:02:45Z",
				ExecutorDurationSeconds: new(165.0),
				GraderStart:             "2026-06-12T11:02:46Z", GraderEnd: "2026-06-12T11:03:12Z",
				GraderDurationSeconds: new(26.0),
			},
		}},
		Summary: results.EvalSummary{
			Passed: new(1), Failed: new(0), Total: 1, PassRate: new(1.0),
			AvgRunSeconds: new(165.0),
			Estimate:      &results.Estimate{InputTokens: 1827, InputCostUSD: new(0.01827)},
			Measured:      &results.Measured{InputTokens: new(100), OutputTokens: new(10), CostUSD: new(0.0003)},
		},
		Baseline: &results.EvalSnapshot{
			RanAt: "2026-06-12T11:00:00Z",
			Summary: results.EvalSummary{
				Passed: new(0), Failed: new(1), Total: 1, PassRate: new(0.0),
				AvgRunSeconds: new(40.0),
			},
			Results: []results.EvalResult{
				{
					ID: "1", Passed: new(false), Summary: &results.GradeSummary{PassRate: new(0.0)},
					Timing: &results.Timing{ExecutorDurationSeconds: new(40.0)}, Fingerprint: "fp-1",
				},
			},
		},
		Previous: &results.EvalSnapshot{
			RanAt: "2026-06-11T11:00:00Z",
			Summary: results.EvalSummary{
				Passed: new(0), Failed: new(1), Total: 1, PassRate: new(0.0),
				AvgRunSeconds: new(170.0),
				Measured:      &results.Measured{InputTokens: new(90), OutputTokens: new(8), CostUSD: new(0.0002)},
			},
			Results: []results.EvalResult{
				{
					ID: "1", Passed: new(false), Summary: &results.GradeSummary{PassRate: new(0.0)},
					Timing:   &results.Timing{ExecutorDurationSeconds: new(170.0)},
					Measured: &results.Measured{InputTokens: new(90), OutputTokens: new(8), CostUSD: new(0.0002)},
				},
			},
		},
	})
	return f
}

// TestEmittedArtifactsValidate marshals evolve's own structures — maximal
// and minimal, in every emission format — and validates them, catching
// drift between the Go types and the published schemas in either direction.
func TestEmittedArtifactsValidate(t *testing.T) {
	compiled := compile(t)

	files := map[string]*results.File{
		"maximal": maximalResults(),
		"minimal": {Schema: results.Schema, Plugin: "p", Skill: "s"},
	}
	for name, f := range files {
		for _, format := range encfmt.Formats {
			data, err := encfmt.Marshal(f, format, "test header")
			if err != nil {
				t.Fatal(err)
			}
			normalized := data
			if format != "json" {
				dir := t.TempDir()
				path := filepath.Join(dir, "results."+format)
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
				if normalized, err = encfmt.NormalizeToJSON(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := validate(compiled["results.schema.json"], normalized); err != nil {
				t.Errorf("%s results as %s does not validate: %v", name, format, err)
			}
		}
	}

	rollup := func() *report.ModelRollup {
		return &report.ModelRollup{
			Provider: "anthropic", Display: "Claude Fable 5",
			Passed: new(1), Failed: new(0), Total: 1, PassRate: new(1.0),
			AvgRunSeconds: new(9.1),
			Estimate:      &results.Estimate{InputTokens: 1385, InputCostUSD: new(0.01385)},
			Measured:      &results.Measured{InputTokens: new(100), OutputTokens: new(10)},
			Baseline:      &report.RollupStats{Passed: new(0), Failed: new(1), Total: 1, PassRate: new(0.0)},
			PreviousDelta: &results.Delta{Rate: new(0.5), InputTokens: new(10)},
			BaselineDelta: &results.Delta{Rate: new(1.0)},
		}
	}
	summary := &report.Summary{
		Schema: 3, ToolVersion: "test", LatestRun: "2026-06-12T10:00:00Z",
		Plugins: map[string]*report.PluginSummary{
			"solo": {
				Evals:  map[string]*report.ModelRollup{"anthropic/claude-fable-5": rollup()},
				Skills: map[string]*report.SkillSummary{"s": {Evals: map[string]*report.ModelRollup{"anthropic/claude-fable-5": rollup()}}},
			},
		},
	}
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(compiled["evaluation.schema.json"], data); err != nil {
		t.Errorf("report summary does not validate: %v", err)
	}

	if golden, err := os.ReadFile(filepath.Join("..", "e2e", "golden", "summary.json")); err == nil {
		if err := validate(compiled["evaluation.schema.json"], golden); err != nil {
			t.Errorf("golden summary.json does not validate: %v", err)
		}
	}
}

// TestNegativeFixtures pins that the schemas actually constrain: documents
// that must fail, fail.
func TestNegativeFixtures(t *testing.T) {
	compiled := compile(t)
	bad := []struct {
		schema string
		doc    string
		why    string
	}{
		{
			"evals.schema.json", `{"evals": [{"id": true, "prompt": "p", "expectations": ["x"]}]}`,
			"boolean id",
		},
		{
			"evals.schema.json", `{"evals": [{"id": "x", "prompt": "p", "files": {"a.txt": "content"}, "expectations": ["y"]}]}`,
			"inline files map",
		},
		{
			"evals.schema.json", `{"evals": [{"id": "x", "prompt": "p", "assertions": [{"type": "regex"}]}]}`,
			"regex without pattern",
		},
		{
			"evals.schema.json", `{"evals": [{"id": "x", "prompt": "p"}]}`,
			"no expectations or assertions",
		},
		{
			"evals.schema.json", `{"evals": [{"id": "x", "expectations": ["y"]}]}`,
			"missing prompt",
		},
		{
			"triggers.schema.json", `{"triggers": [{"query": "q"}]}`,
			"missing should_trigger",
		},
		{
			"results.schema.json", `{"schema": 1, "plugin": "p", "skill": "s"}`,
			"old schema number",
		},
		{
			"grading.schema.json", `{"expectations": [{"text": "t", "passed": true}], "summary": {"passed": 1, "failed": 0, "total": 1}}`,
			"expectation missing evidence",
		},
		{
			"benchmark.schema.json", `{"metadata": {"skill_name": "s", "skill_path": "p", "executor_model": "m", "analyzer_model": "a", "timestamp": "2026-01-15T10:30:00Z", "evals_run": [1], "runs_per_configuration": 1}, "runs": [{"eval_id": 1, "configuration": "no_skill", "run_number": 1, "result": {"pass_rate": 1, "passed": 1, "total": 1}}], "run_summary": {"with_skill": {}, "without_skill": {}}}`,
			"unknown configuration",
		},
		{
			"history.schema.json", `{"started_at": "2026-01-15T10:30:00Z", "skill_name": "s", "current_best": "v1", "iterations": [{"version": "v0", "parent": null, "expectation_pass_rate": 0.5, "grading_result": "drew", "is_current_best": true}]}`,
			"unknown grading_result",
		},
	}
	for _, tc := range bad {
		if err := validate(compiled[tc.schema], []byte(tc.doc)); err == nil {
			t.Errorf("%s: %s unexpectedly validates", tc.schema, tc.why)
		}
	}
}
