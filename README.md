# evolve

`evolve` is a Linux Go CLI for evaluating coding-agent plugins and plugin repositories. It validates plugin structure,
checks whether skills trigger for the right prompts, runs behavioral eval suites in throwaway workspaces, and writes
committed Markdown/JSON rollups for review and CI. It drives the Claude Code and OpenAI Codex CLIs.

This is a fork of [`github.com/bitwise-media-group/evolve`](https://github.com/bitwise-media-group/evolve), slimmed to
Linux, Claude Code and Codex, with a hardened agent sandbox (see [SECURITY.md](SECURITY.md)).

The pipeline is split into three tiers:

- Tier 0 `checks`: static validation of manifests, schemas, skill metadata, and repository shape.
- Tier 1 `triggers`: prompt-level checks that verify the expected skill activates.
- Tier 2 `evals`: behavioral cases that run real agent CLIs and grade the result.

> [!TIP]
> **New to evolve?** Start with [docs/getting-started.md](docs/getting-started.md), then read how to author
> [evaluations](docs/evaluations/index.md), the [configuration reference](docs/config/index.md), and the
> [TUI guide](docs/tui.md).

## Supported repositories

`evolve` auto-detects these layouts, or you can force one with `--layout`:

| Layout        | Marker                                    | Skill paths                        | Eval paths                        |
| ------------- | ----------------------------------------- | ---------------------------------- | --------------------------------- |
| `single`      | `.claude-plugin/plugin.json`              | `skills/<skill>/`                  | `evals/<skill>/`                  |
| `multi`       | `plugins/*/.claude-plugin/plugin.json`    | `plugins/<plugin>/skills/<skill>/` | `plugins/<plugin>/evals/<skill>/` |
| `marketplace` | `.claude-plugin/marketplace.json` at root | `plugins/<plugin>/skills/<skill>/` | `plugins/<plugin>/evals/<skill>/` |

Each eval directory may contain:

- `triggers.<ext>` for trigger-accuracy prompts.
- `evals.<ext>` for behavioral eval cases.
- `results.<ext>` for stored model results.

Supported data formats are `json`, `jsonc`, `yaml`, and `yml`; for a given basename, only one matching file may exist.

## Harnesses and providers

`evolve` distinguishes the **harness** — the agent CLI it drives — from the **provider** that owns and prices the model
the harness runs. Models are provider-qualified, and each is bound to the harness that can run it; evals execute once
per model, through that harness.

Built-in harnesses, each needing its runner CLI on `PATH` and whatever credentials that CLI requires:

| Harness      | Runner CLI | Provider  |
| ------------ | ---------- | --------- |
| Claude Code  | `claude`   | Anthropic |
| OpenAI Codex | `codex`    | OpenAI    |

Run `evolve doctor` from a plugin repository to check the environment, credentials, runner CLIs and the sandbox, and
`evolve models` to see the effective provider / model / harness matrix. The sandbox behavior was verified with `claude`
2.1.285 and `codex` 0.159.2; evolve does not gate on a CLI version.

## Install

`evolve` runs on Linux only. Build from source with Go:

```sh
go install github.com/codeactual/evolve/cmd/evolve@latest
```

Or build this checkout:

```sh
make build
./builds/evolve version
```

The sandbox needs `bubblewrap` (non-setuid), `socat`, and nested unprivileged user namespaces; on Ubuntu 24.04+ that
also means pointing `sandbox.bwrap_path` at a bubblewrap copy outside AppArmor's `/usr/bin/bwrap` profile. See
[docs/installation.md](docs/installation.md).

## Sandbox and trust

Agents run with permission prompts off, so containment is the control. The repository under test is untrusted; the
operator is trusted. By default:

- Every agent run executes in a **deny-by-default bubblewrap sandbox**: only the system directories, the repository
  (read-only), the run directory, the agent CLI, the operator's git config and the bridged credential files
  (read-only), and grants listed in `sandbox.read_paths` / `sandbox.write_paths` are visible. The home directory is not
  mounted (`HOME` stays set, ephemeral). The network stays shared.
- The agent CLIs' **own sandboxes stay on**, layered inside: agent shell commands get no network until you opt in with
  `sandbox.claude_allowed_domains` or `sandbox.codex_network_access`. `evolve run` refuses to start when the nested
  sandboxes cannot start.
- Agents get an **allowlisted environment**, not your whole shell.
- `sandbox.*`, `cache_dir` and `telemetry.*` are **operator-only**: set them with flags, `EVOLVE_*` variables, or
  `~/.config/evolve/config.<ext>`. A repository `.evolve.<ext>` that sets them fails with exit 2.
- The LLM judge runs in its own directory with a read-only view of the workspace and returns schema-constrained
  verdicts.

`--no-sandbox` turns the outer sandbox off for one run. [SECURITY.md](SECURITY.md) lists the accepted residual risks,
including the shared network, the bridged credentials, and tokens kept in git config.

## Quick start

From the root of a plugin repository:

```sh
evolve doctor
evolve run checks
evolve run triggers
evolve run evals
evolve report
```

To run the full pipeline:

```sh
evolve run all
```

To make evaluation failures fail CI:

```sh
evolve run all --strict
evolve report --check
```

By default, `run` commands warn about failed checks or evals but exit `0` when the run itself completes. `--strict`
changes those failures to exit `1`; usage, configuration, and runtime errors exit `2`.

## Running evals

`evolve run checks` performs static validation only. It does not start agent CLIs.

```sh
evolve run checks
```

`evolve run triggers` runs each authored trigger prompt several times and records whether the expected skill activated.

```sh
evolve run triggers --model anthropic,openai --runs 5
```

`evolve run evals` runs behavioral cases in temporary workspaces, then grades the outputs with deterministic assertions
followed by a single LLM-judge session per case for the subjective checks.

```sh
evolve run evals --model anthropic,openai --jobs 4 --max-turns 12 --timeout 900
```

Useful run filters and debug flags:

- `--plugin a,b` (alias `--plugins`): restrict the run to one or more plugins. Repeatable, or comma-separated.
- `--skill x,y` (alias `--skills`): restrict the run to one or more skills. Repeatable, or comma-separated.
- `--model anthropic,openai` (alias `--models`): pick providers / model ids, or `all`. Repeatable, or comma-separated.
- `--eval case-id`: restrict `run evals` to one behavioral case.
- `--new`: run only work with missing or stale stored results.
- `--modified`: rerun only cases whose authored content changed since their stored results (trigger frontmatter or
  definition; eval skill files or definition), fingerprinted alongside the results.
- `--keep-workspaces`: leave temporary workspaces behind for debugging.
- `--count-only`: compute token usage without running agents.
- `--stale-results keep|drop`: decide what to do with stored results outside the `models` restriction.

## Interactive TUI

On an interactive terminal, `evolve run triggers`, `run evals`, and `run all` open a full-screen TUI: first a selection
form to scope the run, then a live dashboard that streams results as agents finish. Pass `--no-tui` (or set
`EVOLVE_NO_TUI=1`) for the plain line-based output used in CI and non-TTY pipes — both paths drive the same engine, so
the run is identical either way.

### Selection form

The form is a set of focusable panes you tab between to choose what runs:

- **Filters** — the same `new` / `modified` / `failed` scoping that the run flags expose.
- **Harnesses** — the agent CLIs to drive; any whose CLI is off `PATH` is shown disabled.
- **Models** — individual models grouped under a per-provider header row, so you can toggle one model or a whole
  provider at once. Models unsupported by the enabled harnesses are shown disabled.
- **Plugins / Skills / Cases** — a tree of every trigger and behavioral case. Each row shows whether it is forced on,
  forced off, or auto-queued for all / some / none of the enabled models; a legend under the tree names every glyph.

Move between panes with `tab` / `shift+tab` (or `1`–`4` to jump), `↑↓` / `jk` to move within a pane, `←→` / `hl` to fold
the tree, `space` to toggle, and `g` / `G` for the ends. Tab on to the **RUN** / **CANCEL** buttons, or just press `r`
to run and `esc` to cancel. The form previews exactly what will execute — it and the engine resolve through the same
plan, so they cannot drift.

### Live dashboard

![evolve live run dashboard](docs/assets/dashboard-1200.png)

Once a run starts, the dashboard streams progress:

- A title bar with running pass / fail / error tallies, elapsed time, rolled-up cost, and an overall progress bar.
- An **Execution** tree (plugin → skill → model → case) carrying per-node rollup columns.
- A tabbed **Rollup** (Summary / Providers / Plugins / Skills), a **Runs** log of every execution in plan order, and a
  **Details** pane showing in-flight cases and the selected case's authored spec.

Selecting a run in any pane moves the selection everywhere; `f` follows the live execution, `enter` jumps to its detail,
and `g` / `G` plus `^d` / `^u` scroll. See [DESIGN.md → TUI](DESIGN.md) for the full wiring.

## Reports

`evolve report` rebuilds repository-level rollups from stored per-skill results:

```sh
evolve report
evolve report --check
```

The report command writes `EVALUATION.md` plus a machine-readable rollup using the configured results format. In
marketplace and multi-plugin repositories, it also includes per-plugin detail pages.

Thresholds default to `0.5` for triggers and `0.66` for evals; they can be set in `.evolve.<ext>` or passed directly:

```sh
evolve report --check --min-triggers-pass-rate 0.95 --min-evals-pass-rate 0.90
```

## Commands

Top-level commands:

- `evolve doctor`: check harness runner CLIs, credentials, counting APIs, and the sandbox.
- `evolve models`: show the effective provider / model / harness matrix and pricing metadata.
- `evolve report`: regenerate evaluation rollups from stored results.
- `evolve run`: run static checks, trigger checks, behavioral evals, or the full pipeline.
- `evolve version`: print build metadata.

Run-tier commands:

- `evolve run checks`
- `evolve run triggers`
- `evolve run evals`
- `evolve run all`

Common global flags:

- `--root PATH`: repository root to operate on.
- `--layout auto|single|multi|marketplace`: repository layout.
- `--results-format json|jsonc|yaml`: results and rollup format.
- `--json`: emit machine-readable JSONL progress.
- `-v, --verbose`: enable debug logging.

Run `evolve <command> --help` for each command's flags.

## Configuration

`evolve` reads at most one repository config file from the repository root, and an optional user-level config at
`~/.config/evolve/config.<ext>`:

- `.evolve.yaml`
- `.evolve.yml`
- `.evolve.json`
- `.evolve.jsonc`

Settings are layered in this order:

1. Built-in defaults.
2. The user-level config file.
3. The repository config file.
4. `EVOLVE_*` environment variables.
5. Explicit CLI flags.

Common settings:

- `layout`
- `models`
- `harnesses`
- `results_format`
- `max_turns`
- `stale_results`
- `checks.*`
- `report.thresholds.*`
- `providers.<name>.models`

Operator-only settings (never valid in a repository config): `sandbox.*`, `cache_dir`, `telemetry.dir`.

Read [docs/config/index.md](docs/config/index.md) for the full configuration reference.

## Development

Common targets (run from this directory; the Makefile is self-contained):

```sh
make fmt
make ci
make smoke
make live
make security_scan
```

Notes:

- `make ci` runs vet, import and gofumpt checks, the race tests (including live bubblewrap enforcement tests, which
  need a non-setuid `bwrap` and user namespaces), the static analyzers (gocyclo, ineffassign, errcheck, staticcheck,
  revive, `go fix` check) and the build.
- `make security_scan` runs osv-scanner and trivy. It is deliberately not part of `ci`.
- `make smoke` runs the live end-to-end test in `e2e/` and requires the `claude` CLI and its credentials.
- `make live` runs the credentialed tests against the real `claude` and `codex` inside the real sandbox. It needs
  `EVOLVE_LIVE_BWRAP` set to an AppArmor-unprofiled bubblewrap copy.
- `e2e/` is a separate Go module for live smoke coverage and fixture repositories.

## Project layout

```text
cmd/evolve/   cobra CLI entrypoint and subcommands
internal/     core packages by concern
docs/         authored Markdown documentation
schemas/      JSON Schemas for eval and report data
e2e/          separate module for end-to-end smoke coverage
security/     code-scanning and security notes
```

## Further reading

- [docs/getting-started.md](docs/getting-started.md), [docs/evaluations/index.md](docs/evaluations/index.md) and
  [docs/config/index.md](docs/config/index.md) for usage, authoring and configuration.
- [DESIGN.md](DESIGN.md) for architecture, engine boundaries, and TUI wiring.
- [SECURITY.md](SECURITY.md) for the trust model and accepted residual risks.
