# Configuration

`evolve` reads configuration from two optional files and layers environment variables and flags above them.

**The repository config** is at most one file at the repository root:

- `.evolve.yaml`
- `.evolve.yml`
- `.evolve.json`
- `.evolve.jsonc`

**The user-level config** is at most one file at `$XDG_CONFIG_HOME/evolve/config.<ext>` (default
`~/.config/evolve/config.<ext>`), with the same four extensions. It is the home of the [operator-only keys](#operator-only-keys).

More than one file of either kind is ambiguous and rejected. Settings are layered, lowest to highest precedence:

1. Built-in defaults
2. The user-level config file
3. The repository config file
4. `EVOLVE_*` environment variables (a dotted key maps to underscores: `sandbox.enabled` → `EVOLVE_SANDBOX_ENABLED`)
5. Explicit CLI flags

## Operator-only keys

The repository under test is untrusted, so its `.evolve.<ext>` may not steer the sandbox or the host-side paths evolve
writes to. These keys are read only from flags, `EVOLVE_*` environment variables, or the user-level config file; a
repository config that sets any of them fails the run with exit 2, naming the key and where to set it.

| Key                            | Type            | Default                              | Description                                                                                                                                       |
| ------------------------------ | --------------- | ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `sandbox.enabled`              | bool            | `true`                               | Confine agents in the deny-by-default bubblewrap sandbox. `--no-sandbox` turns it off for one run. Off leaves only the agent CLIs' own sandboxes.   |
| `sandbox.read_paths`           | list of strings | none                                 | Host paths the agent may read but not write. `~` and environment variables expand; each must be absolute and exist. Never `/` or a home ancestor. |
| `sandbox.write_paths`          | list of strings | none                                 | Host paths the agent may read and write (a build cache, a toolchain directory). Same rules as `read_paths`.                                       |
| `sandbox.bwrap_path`           | string          | unset — `bwrap` on `PATH`            | The bubblewrap to run (`--bwrap-path`). Must pass provenance checks: non-setuid, owned by root or you, no group- or other-writable ancestor.       |
| `sandbox.claude_allowed_domains` | list of strings | none — no network for shell commands | Hosts Claude Code's shell commands may reach (`example.com`, `*.example.com`). `*` and `*.<tld>` are rejected.                                    |
| `sandbox.codex_network_access` | bool            | `false`                              | Let Codex's shell commands reach the network under its `workspace-write` sandbox.                                                                 |
| `sandbox.env_passthrough`      | list of strings | none                                 | Extra environment variable names agents may inherit, beyond the baseline (`GOPATH`, `GOFLAGS`, …).                                                |
| `cache_dir`                    | string          | unset — the OS user cache dir        | Directory holding the token-count cache. (`EVOLVE_CACHE_DIR`)                                                                                     |
| `telemetry.dir`                | string          | unset — telemetry disabled           | Directory for the OpenTelemetry JSON exporter. `--telemetry-dir` overrides it; both win over `OTEL_*` variables.                                  |

The former protected-roots key was removed: the sandbox is deny-by-default now. Setting it anywhere is an error that
points at `sandbox.read_paths` and `sandbox.write_paths`. From the environment, list-valued keys are whitespace-separated.

## Common settings

| Key                                        | Type            | Default                                              | Description                                                                                                                                                           |
| ------------------------------------------ | --------------- | ---------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `version`                                  | string          | unset — any evolve version may run                   | Terraform-style semver constraint (`"0.4.0"`, `"~> 0.4"`) the evolve binary must satisfy before it rewrites results. Non-release builds warn and skip the check.        |
| `layout`                                   | string          | `"auto"`                                             | Repository layout: `auto`, `marketplace`, `multi` or `single`.                                                                                                        |
| `models`                                   | list of strings | unset — every model runnable by an available harness | Restriction on which models exist: provider ids, canonical model ids (`anthropic/claude-sonnet-5`), or `all`. `--model` filters within it.                             |
| `harnesses`                                | list of strings | unset — every harness found on `PATH`                | Restriction on which agent CLIs (`claude`, `codex`) may drive models. `--harness` filters within it.                                                                  |
| `results_format`                           | string          | `"json"`                                             | `json`, `jsonc` or `yaml` for committed results files and the EVALUATION rollup.                                                                                      |
| `max_turns`                                | int             | `20`                                                 | Default maximum agent turns per behavioral eval; `--max-turns` and a per-eval `max_turns` override it.                                                                |
| `judge_model`                              | string          | `"anthropic/claude-sonnet-5"`                        | Model that grades LLM assertions, driven by any installed harness that supports it. Keep it consistent across runs so verdicts stay comparable.                       |
| `baseline`                                 | bool            | `true`                                               | Benchmark each eval without the skill (its lift), recomputed only when the eval or its fixtures change. `--baseline=false` disables it for one run.                   |
| `stale_results`                            | string          | unset — prompt on a terminal, otherwise keep         | `keep` or `drop` results for models outside the `models` restriction. `--stale-results` overrides it.                                                                 |
| `providers.<name>.models`                  | list of models  | the builtin matrix                                   | Replace a provider's builtin model matrix (`anthropic` or `openai`). Replace, not merge.                                                                              |
| `checks.license`                           | string          | unset — the `license` field is forbidden             | License every `SKILL.md` must declare.                                                                                                                                |
| `checks.description_pattern`               | string          | `"Use (when\|after\|before)"`                        | Regex every skill description must match.                                                                                                                             |
| `checks.max_skill_lines`                   | int             | `500`                                                | Maximum `SKILL.md` line count.                                                                                                                                        |
| `checks.ideal_skill_lines`                 | int             | `200`                                                | Ideal line count for the advisory size signal.                                                                                                                        |
| `checks.signals`                           | bool            | `true`                                               | Emit the advisory skill-quality signals after `run checks`.                                                                                                           |
| `checks.plugin_manifests`                  | list of strings | `["claude","codex"]`                                 | Plugin manifests every plugin must ship: `claude` (`.claude-plugin/plugin.json`) and/or `codex` (`.codex-plugin/plugin.json`).                                        |
| `checks.marketplace`                       | bool            | `true`                                               | Validate marketplace manifests (marketplace layout only).                                                                                                             |
| `checks.local_only`                        | bool            | `true`                                               | Fail `run checks` on config that reaches outside the repository: `.mcp.json` files, project settings enabling MCP servers, plugins or marketplaces (or redirecting a `*_BASE_URL`), plugin manifests declaring `mcpServers`, and Codex config for MCP servers, plugins or marketplaces. |
| `report.thresholds.triggers_min_pass_rate` | float           | `0.5`                                                | Minimum triggers pass rate (0–1) for `report --check`.                                                                                                                |
| `report.thresholds.evals_min_pass_rate`    | float           | `0.66`                                               | Minimum evals pass rate (0–1) for `report --check`.                                                                                                                   |
| `report.thresholds.models`                 | list of strings | unset — every model with stored results              | Model keys (`provider/model-id`) the thresholds apply to.                                                                                                             |
| `report.thresholds.maturity`               | list of strings | `["stable","unstable","prerelease"]`                 | Plugin maturity levels whose evidence issues fail `report --check`; other levels only warn. `--maturity` overrides it.                                                |
| `report.strict`                            | bool            | `false`                                              | Require the configured model matrix: `report --check` holds every defined model to the thresholds. `--strict` overrides it.                                           |

## Examples

A repository `.evolve.yaml`:

```yaml
models: [anthropic, openai]
harnesses: [claude, codex]
max_turns: 30
checks:
  max_skill_lines: 400
report:
  thresholds:
    evals_min_pass_rate: 0.75
```

A user-level `~/.config/evolve/config.yaml`, which may also set the operator-only keys:

```yaml
sandbox:
  read_paths: [~/.cache/go-build]
  write_paths: [~/go/pkg/mod]
  bwrap_path: ~/.cache/evolve/bwrap
  claude_allowed_domains: [proxy.golang.org, "*.npmjs.org"]
  codex_network_access: true
  env_passthrough: [GOPATH, GOFLAGS]
```

The same structure applies in every supported format, and JSONC additionally tolerates comments and trailing commas.

## Repository layouts

`evolve` auto-detects three shapes (override with `--layout`):

| Layout        | Marker                                    | Skills                        | Evals                        |
| ------------- | ----------------------------------------- | ----------------------------- | ---------------------------- |
| `single`      | `.claude-plugin/plugin.json`              | `skills/<skill>/`             | `evals/<skill>/`             |
| `multi`       | `plugins/*/.claude-plugin/plugin.json`    | `plugins/<p>/skills/<skill>/` | `plugins/<p>/evals/<skill>/` |
| `marketplace` | `.claude-plugin/marketplace.json` at root | `plugins/<p>/skills/<skill>/` | `plugins/<p>/evals/<skill>/` |

A `multi` repo is a marketplace repo without marketplace manifests — marketplace checks are skipped. In a `single` repo
the repository root is the plugin.
