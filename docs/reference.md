# Reference

## Commands

Top-level:

| Command          | Description                                                                |
| ---------------- | -------------------------------------------------------------------------- |
| `evolve doctor`  | Check provider CLIs, credentials, counting APIs and the sandbox.           |
| `evolve models`  | Show the effective provider/model matrix and pricing metadata.             |
| `evolve report`  | Regenerate evaluation rollups from stored results.                         |
| `evolve run`     | Run static checks, trigger checks, behavioral evals, or the full pipeline. |
| `evolve version` | Print build metadata.                                                      |

Run tiers:

```text
evolve run checks     Tier 0 — static validation (no agents run)
evolve run triggers   Tier 1 — does the expected skill activate?
evolve run evals      Tier 2 — behavioral cases in throwaway workspaces
evolve run all        check → triggers → evals → report
```

Run `evolve <command> --help` for every command and flag.

## Global flags

| Flag                                        | Description                                          |
| ------------------------------------------- | ---------------------------------------------------- |
| `--root PATH`                               | Repository root to operate on.                       |
| `--layout auto\|single\|multi\|marketplace` | Repository layout.                                   |
| `--results-format json\|jsonc\|yaml`        | Results and rollup format.                           |
| `--json`                                    | Emit machine-readable JSONL progress.                |
| `--telemetry-dir PATH`                      | Write OpenTelemetry JSON (operator-only, see below). |
| `-v, --verbose`                             | Debug logging.                                       |

## Run flags

| Flag                                           | Description                                                               |
| ---------------------------------------------- | ------------------------------------------------------------------------- |
| `--plugin a,b` (alias `--plugins`)             | Restrict the run to one or more plugins.                                  |
| `--skill x,y` (alias `--skills`)               | Restrict the run to one or more skills.                                   |
| `--model anthropic,openai` (alias `--models`)  | Pick providers / model ids, or `all`.                                     |
| `--harness claude,codex` (alias `--harnesses`) | Only drive models with these harnesses.                                   |
| `--eval case-id`                               | Restrict `run evals` to one behavioral case.                              |
| `--runs N`                                     | Repeat each trigger prompt N times.                                       |
| `--jobs N`                                     | Concurrency for behavioral evals.                                         |
| `--max-turns N`                                | Per-case turn cap.                                                        |
| `--timeout SECONDS`                            | Per-case timeout.                                                         |
| `--new`                                        | Run only work with missing or stale stored results.                       |
| `--failed`                                     | Rerun only cases that did not pass on a previous run.                     |
| `--baseline=false`                             | Skip the without-skill baseline run of each eval.                         |
| `--judge-model ID`                             | Model that grades `llm` assertions (default `anthropic/claude-sonnet-5`). |
| `--modified`                                   | Rerun only cases whose authored content changed since their results.      |
| `--keep-workspaces`                            | Leave temporary workspaces behind for debugging.                          |
| `--count-only`                                 | Compute token usage without running agents.                               |
| `--stale-results keep\|drop`                   | What to do with results outside the `models` set.                         |
| `--strict`                                     | Turn check / eval failures into a non-zero exit.                          |
| `--no-tui`                                     | Force plain line output (also `EVOLVE_NO_TUI=1`).                         |
| `--no-sandbox`                                 | Disable the outer sandbox (operator-only, see below).                     |
| `--bwrap-path PATH`                            | Use this bubblewrap instead of the one on `PATH` (also on `doctor`).      |

## Exit codes

| Code | Meaning                                                                   |
| ---- | ------------------------------------------------------------------------- |
| `0`  | The run completed. By default, failed checks / evals only warn.           |
| `1`  | With `--strict` (or `report --check`): check / eval / threshold failures. |
| `2`  | Usage, configuration, or runtime errors.                                  |

## Providers

| Provider  | Harness CLI | Triggers | Evals | Token counting |
| --------- | ----------- | -------- | ----- | -------------- |
| Anthropic | `claude`    | yes      | yes   | yes            |
| OpenAI    | `codex`     | yes      | yes   | yes            |

Each provider needs its harness CLI on `PATH`. For executing evaluations, any authentication method the harness CLI
supports works — browser-based / OAuth login included; evolve bridges the CLI's own login credential file into each
eval workspace, bound read-only inside the sandbox (see [Execution model](evaluations/execution.md)). Run
`evolve doctor` to check the local environment.

### Token counting

An API key or token is needed only for the vendor token-counting APIs. Each counter reads the `EVOLVE_`-prefixed
variable first — a counting-only credential the harness CLI never picks up — and falls back to the vendor's standard
variable, which the harness would also use for authentication if set:

| Provider  | Counting-only                                                 | Shared with the harness                                                  |
| --------- | ------------------------------------------------------------- | ------------------------------------------------------------------------ |
| Anthropic | `EVOLVE_ANTHROPIC_API_KEY` / `EVOLVE_CLAUDE_CODE_OAUTH_TOKEN` | `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_AUTH_TOKEN` |
| OpenAI    | `EVOLVE_OPENAI_API_KEY`                                       | `OPENAI_API_KEY`                                                         |

A model with no published pricing renders its cost figures as `n/a` — structurally absent, not zero.

## Sandbox and trust

The repository under test — its skills, fixtures, eval prompts and `.evolve.<ext>` — is untrusted, and agents run with
permission prompts off. Containment is the control (Linux only):

- **Outer sandbox.** Every agent and every `command` assertion runs inside a deny-by-default bubblewrap sandbox. It
  shows only the system directories, the agent CLI, the repository (read-only), the run directory, the operator's git
  config and the agent's bridged login file (both read-only), and paths you grant with `sandbox.read_paths` /
  `sandbox.write_paths`. `HOME` is set but not mounted: it starts empty and everything written there disappears at
  exit, so toolchains and caches under your home directory need a grant. The network stays shared.
- **Agents' own sandboxes.** Claude Code's sandbox is always on (`--settings`, fail-closed) and Codex runs `read-only`
  for triggers and `workspace-write` for evals. Agent shell commands get no network by default; opt in with
  `sandbox.claude_allowed_domains` and `sandbox.codex_network_access`. `evolve run` exits `2` before any agent starts if
  the nested sandboxes cannot start.
- **Environment.** Agents get an allowlisted environment (`PATH`, `HOME`, locale, terminal, proxy and TLS variables,
  the credential variables their own CLI reads, and names you list in `sandbox.env_passthrough`), not your whole shell.
- **Operator-only keys.** `sandbox.*`, `cache_dir` and `telemetry.*` come only from flags, `EVOLVE_*` environment
  variables, or the user-level config at `$XDG_CONFIG_HOME/evolve/config.<ext>` (default
  `~/.config/evolve/config.<ext>`). A repository `.evolve.<ext>` that sets any of them fails with exit `2`.
- **`--no-sandbox`** (or `sandbox.enabled=false`) removes the outer boundary. `evolve doctor` prints a `SANDBOX`
  section: bubblewrap provenance, an outer smoke run, a nested-bubblewrap probe and a `socat` check.

See the [configuration reference](config/index.md) for each key.

## Reports

`evolve report` rebuilds repository-level rollups from stored per-skill results, writing `EVALUATION.md` plus a
machine-readable rollup in the configured format (per-plugin detail pages in marketplace / multi repos). Gate on
thresholds (defaults: triggers `0.5`, evals `0.66`):

```sh
evolve report --check --min-triggers-pass-rate 0.95 --min-evals-pass-rate 0.90
```

[Reviewing reports](reports/index.md) walks through the report layout, every table and its columns, and the threshold
gate.
