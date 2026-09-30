# Agent orientation

Fast map of this repository so a new session can act without re-exploring. For _why_ the code is shaped this way —
conventions, the engine/reporter architecture, and how the TUI is wired — read [DESIGN.md](DESIGN.md); for end-user
usage read [README.md](README.md). This file is the "where things are"; DESIGN.md is the "how it fits together".

## What this is

`evolve` is a single Linux-only Go CLI (module `github.com/codeactual/evolve`, entry `cmd/evolve`) that evaluates
coding-agent plugins across tiers: static checks (Tier 0), trigger-accuracy evals (Tier 1), and behavioral evals (Tier
2), then writes committed Markdown/JSON reports. It drives real agent CLIs (Claude Code and Codex) in throwaway
workspaces, inside a deny-by-default sandbox, and grades the results.

## Layout

```text
cmd/evolve/        CLI: package main, one file per verb (cobra). See "Commands" below.
internal/          All library code, one package per concern. See "Packages" below.
docs/              Authored Markdown documentation (GitHub-rendered): getting started, evaluations, config, reports.
schemas/           JSON Schemas for eval/results/report files (embedded via schemas.go).
e2e/               SEPARATE Go module: live smoke test + fixture repos/ and golden/ outputs.
security/          Committed code-scanning notes.
Makefile           Self-contained (includes nothing from any superproject); see "Build, test, run".
```

Build output not to edit or commit: `builds/` (the built binary, `builds/evolve`). The `e2e/golden/` files are
reference outputs `TestGenerateGolden` compares against; regenerate them with `go test ./internal/report -update`.

## Commands (`cmd/evolve/`)

`main.go` is the root command and shared `opts`; each verb is `<verb>.go` registering itself in `init()`. Verbs:

- `run` (parent) → `run checks`, `run triggers`, `run evals`, `run all` — the eval tiers; `run all` chains them.
- `report` — regenerate EVALUATION.md / EVALUATION.json from committed results.
- `models`, `doctor`, `version` — list the model matrix, environment diagnostics (including the sandbox section:
  bubblewrap provenance, outer smoke run, nested bubblewrap probe, `socat`), version.
- `runui.go` — interactive-TUI gating and the form→engine→dashboard wiring shared by the interactive `run` paths (the
  `run_*.go` files fall back to plain output when the TUI is off). **See DESIGN.md → TUI.**

## Packages (`internal/`)

- `cli` — shared command plumbing: global `Options`, layered `.evolve` config, harness/model/repo/threshold resolution.
- `plan` — the planner: the single owner of _what runs, for which models, in what order_. Holds the structural types
  (`UnitRef`, `Kind`, `Status`, `Mode`, `ItemMetrics`, `CaseRef`, `Filter`, `Tiers`, `SkillCatalog`), applicability +
  prior-metrics, the canonical model — `Plan` (ordered plugin→skill→model→unit→case tree), `Selection` (enable/disable
  intent), and `Build` (resolve a Selection into a Plan with per-model queued/prior) — and `Session` (`session.go`): the
  stateful owner of the TUI form's filter/harness/model/case selection, with receivers the form drives and a `Plan()`
  that resolves through the same `Build`. `run` and `tui` both import it and must not re-derive ordering or selection.
- `run` — the three eval engines (`checks.go`, `triggers.go`, `evals.go`, `sweep.go`), the unit enumeration / preselect
  matrix (`plan.go`: `Catalog`/`Plan`/`Needs`/`CaseReasons`), and the `Reporter` seam (`reporter.go`) the TUI and plain
  output both implement. Executes the per-model filters `plan.Build` resolves.
- `tui` — the interactive bubbletea selection form and live run dashboard; a presentation layer over `plan` (the form
  drives a `plan.Session`; the dashboard projects the `plan.Plan` the engine runs). **See DESIGN.md → TUI for the full
  wiring.**
- `model` — the model vendors (Anthropic, OpenAI) and the canonical model registry:
  provider-qualified ids, pricing, the harnesses each model can be driven by (the `Supported` map), and the vendor
  token-counting clients. The lowest-level domain package (imports no other internal package); owns the shared value
  types `CommandSpec`, `EvalInput`, `Usage`.
- `harness` — the agent CLIs evolve drives (Claude Code and Codex): runner-CLI command construction (including the
  always-on inner sandbox settings and the judge specs), output parsing, the optional `EvalRunner` and `OfferedModels`
  capabilities (the latter probes
  which models the operator's installed CLI actually serves, so the TUI can deselect the rest by default), and
  `Selection`/`RunnableHarness` that bind a model to the one harness that runs it (evals run once per model, never once
  per harness).
- `runner` — executes `model.CommandSpec`s; the only package touching `os/exec` for agent execution, so engines test
  against a fake (the one setup-time exception: `internal/workspace` shells out to `git` to initialise each workspace).
  It owns the deny-by-default bubblewrap sandbox (`sandbox*.go`, bubblewrap provenance in `bwrap.go`, the probes in
  `probe_sandbox.go`) and the allowlisted child environment (`env.go`). The live tests in `sandbox_linux_test.go` run
  real bubblewrap and fail, rather than skip, when it is unavailable.
- `grade` — assertion evaluation: deterministic checks (files/regex/commands) first, then one batched LLM-judge session
  grading all of a case's `llm` assertions with per-assertion verdicts; owns the verdict schema, the nonce-fenced judge
  prompt, and the strict verdict decode.
- `workspace` — builds the throwaway project dirs each agent session runs in, each a fresh git repo with the fixture
  state as its initial commit.
- `results` — the committed per-skill `results.<ext>` files beside each skill's evals.
- `report` — renders results into EVALUATION.md / EVALUATION.json, and gates CI (`report --check`). Imports
  `internal/run` to reuse its `StaleTiers` staleness primitive (the same fingerprint comparison `--modified` uses) for
  the strict evidence gate — no cycle, since `run` never imports `report`.
- `evalspec` — parses authored triggers/evals definitions.
- `manifest` — parses plugin/marketplace manifests and SKILL.md frontmatter.
- `layout` — detects the repo shape (single/multi/marketplace) and enumerates plugins + eval sets.
- `tokencount` — caches vendor-reported input-token counts (from official counting APIs, never a local tokenizer).
- `encfmt` — reads/writes JSON, JSONC, YAML behind one data model.
- `telemetry` — OpenTelemetry setup: picks the exporter (JSON files when `--telemetry-dir`/`telemetry.dir` is set, else
  `autoexport` from `OTEL_*`, else disabled), builds the slog→OTEL fanout handler, decorates the `run.Reporter` to turn
  engine events into metrics/logs, and owns provider shutdown. The engines reach the global tracer/meter directly, so
  this package imports `internal/run` to decorate the reporter (no cycle); `internal/report` also imports it, for the
  `StaleTiers` gate primitive. Off by default.
- `version` — build/version info.

## Build, test, run

The `Makefile` is self-contained (Go via `go.mod`); it pins the static analyzers and runs them through `go run`, so nothing
is installed globally. Targets:

- `build` — `builds/evolve`, stamped with the version and verified through `evolve version`.
- `test` — race tests, including the live bubblewrap enforcement tests (they need a non-setuid `bwrap` and user
  namespaces).
- `ci` — `no-make-warnings`, `vet` (root and `e2e`), `imports-check`, `go-gofumpt-check`, `test`, `go-cyclo-gate`,
  `go-ineffassign`, `go-errcheck`, `go-staticcheck`, `go-revive`, `go-fix-check`, `build`. No credentials needed.
- `fmt` (goimports then gofumpt), `tidy` (root and `e2e`).
- `security_scan` (`osv_scan` + `trivy_scan`) — deliberately not a prerequisite of `ci`.
- `smoke` — the live end-to-end test in `e2e/`; needs the `claude` CLI and credentials, and on hosts with AppArmor's
  userns restriction `EVOLVE_SANDBOX_BWRAP_PATH` pointing at an unprofiled bubblewrap copy.
- `live` — the credentialed tests in `internal/...` that drive the real `claude` and `codex` inside the real sandbox
  (`-tags live`); needs `EVOLVE_LIVE_BWRAP` set to that unprofiled copy. Not part of `ci`.

After touching engine output formats, the `e2e/golden` files may need updating (`go test ./internal/report -update`).
Lint configuration lives in the Makefile's pinned analyzers; there is no separate lint config or exclusion file.

## Conventions

- Every `internal` package carries a `doc.go` package comment — read it first when entering a package (`tui` is the lone
  exception; its overview is the `app.go` package comment and DESIGN.md → TUI).
- Conventional Commits; commit signing is handed off via a `commit.sh` script (see the global agent instructions) rather
  than committed from a sandbox.
- Clean breaks over backward-compat shims: drop problematic formats rather than add deprecation aliases.
- No `*T`-pointer helper functions (e.g. `func ip(v int) *int { return &v }`). Go 1.26's `new(expr)` allocates and
  initializes in one call, so write `new(1400)` / `new(0.004)` directly. Mind the type: `new(expr)` uses the
  expression's own default type, not the assignment context — for a `*float64` field write `new(1.0)`, not `new(1)`
  (which is `*int`).
