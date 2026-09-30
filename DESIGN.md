# Design

evolve is a single Go CLI that evaluates coding-agent plugins: static checks (Tier 0), trigger-accuracy evals (Tier 1),
behavioral evals (Tier 2), and committed Markdown/JSON reports. The README covers usage; this document records the
conventions the codebase follows.

## Ergonomics

Commands are flat verbs, except the eval tiers, which nest under `run`:

```text
evolve <verb>
evolve run checks|triggers|evals|all
```

`evolve run all` chains the tiers — check, triggers, evals, then report — and keeps going past tier failures so later
tiers still produce signal.

Global flags (`--root`, `--layout`, `--json`, `-v`) apply to every command. Configuration layers, lowest precedence
first: built-in defaults, the optional user-level config file (`os.UserConfigDir()/evolve/config.<ext>`), the optional
`.evolve.<ext>` config file at the repository root (YAML, JSON or JSONC — at most one of each), `EVOLVE_*` environment
variables, then explicit flags. The repository is untrusted, so its config is loaded separately and rejected when it sets
an operator-only key (`sandbox.*`, `cache_dir`, `telemetry.*`); see the sandbox paragraphs below.

Exit codes are part of the interface: 0 means the run completed, 2 means a usage or configuration error stopped it.
Failed checks or evals print a `WARN:` line but still exit 0 by default; passing `--strict` to a `run` subcommand
restores exit 1 on failures (`cli.ErrFailures`). `report --check` always exits 1 on threshold breaches.

## File layout

```text
./go.mod
./cmd/evolve/main.go   # main entry point and root command (package main)
./cmd/evolve/<verb>.go # one file per subcommand
./cmd/evolve/runui.go  # TUI gating + the form -> engine -> dashboard wiring for `run`

./internal/cli/...     # shared command plumbing: global Options, config layering,
                       # harness/model/repo/threshold resolution
./internal/run/...     # the three eval engines: checks, triggers, evals
./internal/tui/...     # interactive selection form + live run dashboard (bubbletea)
./internal/<area>/...  # one package per remaining concern (grade, report, results,
                       # runner, workspace, ...)

./docs/...             # authored Markdown documentation, rendered by GitHub
./e2e/...              # separate module: live smoke test plus fixture repositories and golden files
./Makefile             # self-contained: build, test, ci, the pinned analyzers, live, smoke, security_scan
```

If a concern spans areas, it gets its own package under `./internal` with a clear but concise name. Every internal
package carries its package documentation in a `doc.go`.

## Architecture

Each subcommand lives in its own `cmd/evolve/<verb>.go` as a package-level `<verb>Cmd` var plus a `<Verb>Flags` struct,
with the file's `init()` registering its flags and adding the command to its parent (`rootCmd`, or `runCmd` for the eval
tiers). Shared global state lives in the package-level `opts` (`cli.Options`).

`internal/cli` owns the resolved global state (`Options`) and the helpers that turn it into a detected repository, an
effective harness/model set, a token counter, and report thresholds. The engines (`run`, `report`) take what they need
as explicit options — the trigger and case engines embed the shared `run.Options` — and write through the interfaces
they declare, so they test against fakes; `runner` is the only package that touches `os/exec` for agent execution (the
one setup-time exception is `internal/workspace` shelling out to `git` to initialise each workspace as a repository).

### Trust model and the sandbox layers

The repository under test is untrusted (its skills, fixtures, eval prompts, and its own `.evolve.<ext>`), the agents run
with permission prompts off, and the operator is trusted. Containment is the control, in layers:

**Operator-only configuration.** `LoadConfig` reads the user-level config and the repository config into separate
vipers, rejects the repository config when it sets `sandbox.*`, `cache_dir`, or `telemetry.*` (exit 2, naming the key
and where the operator can set it), and only then merges it over the user-level one. So a hostile repository cannot turn
the sandbox off, widen it, or choose host paths evolve writes to. Offered-model probes run unsandboxed against the
operator's real CLI config, so they run from a fresh empty directory, and the Claude probe passes
`--setting-sources user --strict-mcp-config`.

**The outer sandbox is deny-by-default.** Because `runner` is the single exec chokepoint, it also enforces filesystem
isolation: when `Exec.Sandbox` is enabled (the default) every command runs inside bubblewrap with a fresh root that shows
only the system directories (`/usr`, the merged-`/usr` symlinks or `/bin /sbin /lib…` binds, `/etc`, `/sys`, `/proc`,
`/dev`, tmpfs `/tmp` and `/var/tmp`), the resolved agent executable, the repository under test read-only, the operator's
git config files (`~/.gitconfig`, `$XDG_CONFIG_HOME/git/config` falling back to `~/.config/git/config` — never the `git/`
directory, so `git/credentials` stays hidden) read-only, the spec's `ReadPaths` (the bridged credential files, the
judge's view of a workspace) read-only, the operator's `sandbox.read_paths`/`sandbox.write_paths` grants, and the run
directory read-write, bound last so it stays writable even inside a read-only grant. The home directory is not mounted:
`HOME` stays set and is an ephemeral directory on the sandbox's writable root, and `TMPDIR` is `/tmp`. The network stays
shared, because agents and dependency tooling need it; `sandbox.read_paths`/`write_paths` are how an operator exposes a
build cache or toolchain. Grants must be absolute, exist, and may not be `/` or an ancestor of the home directory.

The bubblewrap binary runs before the security boundary, so `ResolveBwrap` validates it before every sandboxed run
(absolute path, regular file, not setuid/setgid, owned by root or the operator, and neither it nor any ancestor
directory group- or other-writable) and never executes it to check. The sandbox fails closed: an enabled sandbox that
cannot be constructed errors rather than running the agent unconfined; `--no-sandbox` / `sandbox.enabled=false` opts out.

**The agents' own sandboxes are layered inside.** Both CLIs use bubblewrap on Linux, which nests when user namespaces
are available, so the harnesses keep their own sandboxes on instead of disabling them (`model.InnerSandbox` threads the
operator's settings into `TriggerSpec`/`EvalSpec`). Every Claude invocation passes `--settings` with the sandbox enabled,
`failIfUnavailable`, unsandboxed commands disallowed, and a strict network allowlist (`sandbox.claude_allowed_domains`,
empty by default); Codex runs `read-only` for triggers and `workspace-write` for evals with
`sandbox_workspace_write.network_access` from `sandbox.codex_network_access` (off by default), and keeps `.git`
read-only, so a Codex agent cannot commit. Agent shell commands therefore get no network until the operator opts in.
Before any agent starts, `evolve run` probes that a bubblewrap can nest inside the outer sandbox (and a further user
namespace inside that, which Claude's seccomp helper needs) and exits 2 with the probe's stderr if it cannot.

On hosts with `kernel.apparmor_restrict_unprivileged_userns=1` AppArmor's profile for `/usr/bin/bwrap` strips the
capabilities of that bubblewrap's descendants. So the operator points `sandbox.bwrap_path` at an unprofiled copy, and
evolve exposes the validated binary at `/evolve/bin/bwrap` first on the sandbox's `PATH` (the same file at another path
is unprofiled), so the CLIs' nested sandboxes use it too. `evolve doctor` reports the provenance verdict, an outer
smoke run, the nested probe with this remedy, and whether `socat` is present.

**An allowlisted environment.** `runner.Run` builds the child environment from a baseline (`PATH`, `HOME`,
`XDG_CONFIG_HOME`, locale, terminal, proxy and TLS names), the operator's `sandbox.env_passthrough`, and the spec's own
entries; harnesses forward only the credential variables their CLI reads. `GITHUB_TOKEN`, `AWS_*` and the
`EVOLVE_*`-prefixed token-counting keys (which scope credentials to the counting APIs and never override what the CLIs
authenticate with) never reach an agent. Codex runs with `shell_environment_policy.ignore_default_excludes=false` so its
shell commands drop `*KEY*`/`*SECRET*`/`*TOKEN*`. Claude's analogous `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` is not set: in
claude 2.1.285 it forces the permission mode to default, which would end the prompts-off eval posture. The operator-context
probes (`Exec.InheritEnv`) keep the full environment because they read the operator's own account configuration.

**Session isolation** covers what a run may remember. Every harness points its CLI at a throwaway state directory inside
the workspace (`.evolve/<name>-home`) so trigger/eval sessions never land in the operator's real session history, and no
long-term memory carries across runs: `CLAUDE_CONFIG_DIR` and `CODEX_HOME`. Auth is bridged in from the operator's real
config root: Claude's `.credentials.json` and Codex's `auth.json` are symlinked into the isolated home and bound
**read-only** at their real path inside the sandbox through `CommandSpec.ReadPaths`, so an agent cannot write through to
the operator's real files (a mid-run token refresh cannot persist). The pre-seeded git identity arrives through the
read-only git config binds, not a private `HOME`. The shared helpers live in `internal/harness/isolate.go` and are
best-effort: a failure still leaves the env override set, so the CLI builds its own tree and an auth gap surfaces as a
runtime error on the case rather than an evolve crash. The isolated state dies with the workspace; `--keep-workspaces`
preserves it for debugging.

**The judge** is the grader, and what it reads is untrusted, so it runs apart from the agent: `HarnessJudge.Judge` creates
a fresh directory beside the workspace, runs the session there with its own fresh CLI config home and the workspace as a
read-only path, and removes the directory afterward. Claude runs `--restricted --safe-mode --strict-mcp-config --tools
Read,Grep,Glob --permission-mode dontAsk --add-dir <ws> --json-schema <schema>`; Codex runs `--sandbox read-only
--ignore-rules --ephemeral --output-schema`. `grade` owns the verdict schema, fences the agent's output and the
expected-output text in per-call nonce markers (stripping any copy of the nonce first) and labels all of it untrusted
evidence, and strictly decodes the structured payload; nothing is ever extracted from prose, so a verdict block the
judge quotes from the content under test cannot stand in for its answer.

## TUI

`evolve run triggers`, `run evals`, and `run all` show an interactive full-screen UI — a selection form, then a live run
dashboard — when stdout is a real terminal and the user has not opted out (`--no-tui` / `EVOLVE_NO_TUI`). The check is
`interactiveTUI` in `cmd/evolve/runui.go`; when it returns false the historical line-based path runs unchanged. The UI
is built on
[bubbletea](https://github.com/charmbracelet/bubbletea)/[lipgloss](https://github.com/charmbracelet/lipgloss) and lives
entirely in `internal/tui`, which is a presentation layer over `internal/run` — it computes nothing about a run itself,
it only displays what the engine reports.

### The reporter seam

`run.Reporter` (`internal/run/reporter.go`) is the single contract between the engine and any front end. The engine
never writes progress to stdout directly; it calls `UnitStarted` / `UnitSkipped` / `ItemStarted` / `ItemDone` /
`UnitFinished` / `Warn`. Two implementations exist:

- `run.PlainReporter` — the default when `Options.Reporter` is nil, reproducing the legacy line output exactly, so
  non-TTY runs and the engine tests are untouched by the indirection.
- `tui.tuiReporter` — forwards each call into the bubbletea program as a message via `program.Send`, which is
  goroutine-safe. That matters because `ItemDone` and `Warn` fire from the parallel agent-run goroutines (`--jobs`).

Because the seam is the only coupling, the same `run.Sweep` drives either output with no engine changes.

### Process model

`runWithUI` (`cmd/evolve/runui.go`) runs two goroutines joined by channels:

- The main goroutine runs `p.Run()` — bubbletea's event loop and renderer.
- An engine goroutine blocks on the `runReq` channel. When the user chooses RUN the form sends a `tui.RunRequest`; the
  goroutine invokes the `engine` callback (which calls `run.Sweep` with the reporter attached), then sends
  `tui.RunDone(...)` back into the program.

Quitting is cooperative: closing `progExited` releases the engine goroutine if the user cancels before running;
`cancel()` on the command context stops a sweep already in flight (a resulting `context.Canceled` is swallowed — a user
quit is not an error); `<-engineDone` joins before returning. Token-counter diagnostics are routed through a
`switchWriter` that starts at `io.Discard` and is repointed at `forward(rep)` once the run begins, turning each counter
line into a `Warn` event so it surfaces in the dashboard rather than corrupting the alt-screen.

### Root model and screens

`tui.Model` (`app.go`) is the bubbletea root. It holds two sub-models and a `screen` that advances
`screenForm -> screenDashboard`. `Update` routes by message type: `WindowSizeMsg` fans the size out to both sub-models;
`spinner.TickMsg` drives the dashboard spinner while a run is live; `KeyMsg` is delegated to whichever screen is active;
and the progress messages (`unitStartedMsg`, `itemDoneMsg`, …, `runDoneMsg` — all defined in `messages.go`, one per
`Reporter` method) are applied to the dashboard. `startRun` builds the execution plan with `run.PlanFor` per selected
model, constructs the dashboard, and `tea.Batch`es dispatching the request with starting the spinner.

### Selection form

`formModel` (`form.go`) is a five-region screen: a left column stacking a **filters** pane (the new/modified/failed
toggles), a **harnesses** pane, and a **models** pane, beside a large plugin → skill → case **tree**, plus a
tab-reachable **button row** (CANCEL / RUN, navigated with `←`/`→` and fired with `enter`). The models pane prefixes
each vendor's models with a provider **header row** that toggles the whole group (tri-state box) on top of the
individual model toggles. The panes carry the dashboard's cyberdream accent borders, mirrored horizontally
(Filters/Harnesses/Models green/teal/orange down the left, the tree pink on the right) with the same always-bright
titles and border-only dimming when unfocused. All selection state lives in `plan.Session` (`internal/plan/session.go`):
the form navigates and routes each key press to a Session receiver
(`SetNewFilter`/`SetModifiedFilter`/`SetFailedFilter`, `EnableHarness`, `EnableModel`, `SetCases`), then renders every
pane by querying the Session. `tree.go` is now pure structure and navigation — it holds no selection state — and the
flat regions use a minimal `list` (`list.go`). The Session holds the per-(model, case) categories (`run.CaseReasons`)
the filters act on, derives the queued baseline from the active filters, and resolves the whole state into a `plan.Plan`
through the same `plan.Build` the engine runs. A case renders as force-on (`☑`), force-off (`☐`), or one of the auto
states — queued for all (`◉`), some (`◷`), or none (`○`) of its applicable enabled models; a harness off PATH and a
model unsupported by the enabled harnesses render disabled. Before the form is built, each installed CLI that can report
its account's model list is probed (`run.ProbeOfferedModels` over the optional `harness.OfferedModels` capability —
Claude's client-side `/model` alias list and Codex's app-server `model/list`); a model the resolved
harness does not offer starts deselected and renders muted with an `(unavail.)` tag, but stays toggleable. The probe
fails open (no capability, error, or timeout deselects nothing) and is skipped entirely when `--model` names models
explicitly. `request()` returns a `tui.RunRequest` carrying the Session's enabled selections and resolved
`plan.Selection`; the engine and dashboard re-`Build` from those, so the form preview, the dashboard, and the engine
cannot drift.

### Live dashboard

The dashboard is split across two files: `dashboard.go` holds the state, message handling, and key handling;
`dashboard_view.go` holds all rendering. It is constructed from the plan at run start (`newDashboard`):

- `unitState` is one (skill, model, tier) execution unit; its `caseState` rows are pre-populated from the catalog —
  mirroring the engine's per-provider skips and the selection filter — so pending cases render with their real labels
  before they run, and live updates are matched back by label.
- `apply(msg)` is the reducer: each progress message mutates unit/case status, tallies, metrics, in-flight tracking, and
  the warning ring buffer.
- Units are execution-ordered and grouped plugin → skill → model for the left "Execution" pane. `buildNodeRefs`
  collapses settled and not-yet-started groups to a single row and expands the active one (plus, when the user has
  paused on an older selection, the group holding it).
- The execution log (`execLog`) is pre-populated with every planned execution in plan order, so the Runs pane lists the
  pending runs up front rather than growing as each starts; `itemStarted` matches back to those rows by label. Because
  the list is no longer start-ordered, `liveIdx` tracks the most recently started execution as the follow anchor.
- The Execution, Runs, and Details panes share one selection — `runSel`, an index into `execLog`. The Runs pane lists
  it, Details renders it, and the Execution pane highlights its case; moving the selection in any pane moves it in the
  others (the Execution tree expands the selected group so its highlight stays visible). `runFollow` pins the selection
  to the live execution (`liveIdx`); `[f]` (re-)engages it from any pane, navigating off the live row releases it, and
  leaving the Execution pane does **not** silently re-follow. `[g]`/`[G]` jump to the top/bottom of the list; `[enter]`
  in either the Execution or Runs pane jumps to Details on the selected run. Panel titles stay bright at their pane's
  accent colour at all times; only the border dims when the pane is unfocused.
- The view is a title bar (run stats on the left; a run-wide progress bar over the Rollup column on the right, with
  percent-complete at its end), a blank separator row, then the body and a footer of key hints. The body is the left
  execution pane (a plugin → skill → model → case tree, every level carrying the same right-aligned rollup columns) with
  a glyph legend anchored beneath it (the form's legend pattern: one or two rows by width, dropped on short terminals;
  `leftDims` owns the split and `execPageStep` shares it so paging stays true), beside a right column of a tabbed
  "Rollup" (Improvements / Regressions / Skills, switched with `[←→]`/`[h]` and — when the rows overflow the pane —
  scrolled with `[↑↓]`/`[jk]`, `[g]`/`[G]`, and `[^d]`/`[^u]` over a pinned header), the Runs log, and an "Executing"
  detail pane (in-flight cases plus the highlighted case's authored spec). The progress bar rides the title bar rather
  than taking its own row, so the two panes stay top-aligned. The Execution pane always scrolls the whole tree to keep
  the highlight on-screen (centred, clamped at the ends) — the same focused or not — so leaving the pane never makes
  other nodes disappear. Only the expansion differs by mode: live-status collapse while unfocused, the user's overrides
  while focused.
- Group and unit rollup rows classify against the report thresholds (`report.thresholds.*`, plumbed in as
  `tui.Thresholds` so the dashboard and `report --check` agree): green ✓ when every case passed, orange ✓ when a tier
  has failures but its pass rate meets the tier's minimum, red ✗ below it — the worst tier verdict wins a mixed group,
  and any errored case still rolls the group to ⚠ (results are incomplete, so no rate is trustworthy). Individual case
  rows stay binary; `caseAggStatus`/`tierStatus` in `dashboard.go` own the classification.
- `now func() time.Time` is injected so elapsed-time rendering is deterministic under test.

### Mouse

Both screens take mouse input alongside the keyboard. `Model.View` declares `MouseMode: cell motion` on the returned
`tea.View` (the v2 replacement for the `WithMouseCellMotion` program option), and `Update` routes `tea.MouseMsg` to the
active screen's `handleMouse` exactly like key presses — so every click and wheel event flows through the same mutators
the keyboard uses (`setFocus`/`moveRun`/`scrollDetailBy`… on the dashboard, the Session receivers via `toggle()` on the
form) and none of the shared-selection or no-drift invariants above gain a second code path. A left click focuses the
pane under the cursor and applies the row it hit: form checkbox rows toggle, tree parents (form and Execution alike)
fold or unfold, a case/run row takes the shared selection, the rollup tab strip switches tabs, CANCEL/RUN activate, and
the footer's `[o] open dir` / `[l] open log` hints fire `openPath` like their keys. A repeat click on the form-tree leaf
already under the cursor cycles its tri-state — cycling on first contact would flip cases while the user is merely
selecting. The wheel scrolls the pane under the cursor **without** moving focus: offset-scrolled panes (Rollup, Details)
step by `wheelScrollStep`, selection-centred ones (Runs, Execution, the form's cursor-anchored windows) step their
selection or cursor by one row. The quit dialog ignores the mouse entirely.

Hit-testing is manual geometry with one hard rule: **it must read the same `layout()` the view renders from**. Each
screen exposes a `layout()` (`dashLayout` in `dashboard_view.go`, `formLayout` in `form.go`) that reports every pane's
outer rect, and `view()` is written in terms of it — so the rendered frame and the click targets cannot drift, and the
tests pin the alignment by asserting each pane's title sits on the row its rect starts at. `mouse.go` holds the shared
primitives: `rect`, `contentRect` (the inverse of `panel`'s border-and-margin inset), and the window inverses
`windowIndexAt`/`topWindowStart` that map a clicked row back to a list index (tested against `scrollWindowFunc` /
`renderRows` output directly). One tradeoff to know: with mouse reporting on, the terminal's native text selection needs
the usual shift-drag (or option/alt-drag) bypass.

### Rendering primitives and tests

`panel.go` draws every framed box — a rounded border with the title, count, and tab strip embedded in the border edges,
lazygit-style — and `panelContentWidth` is the single source of truth for body sizing. `styles.go` holds the ANSI-256
palette (chosen to degrade on limited terminals) plus the cyberdream accent colours for the dashboard's panel borders,
and `util.go` the width-aware `truncate`/`clip` helpers. `tui_test.go` exercises the models directly — feeding `KeyMsg`s
and `Reporter` messages into `Update`/`apply` and asserting on `view()` output — so the whole UI is tested without a
terminal.
