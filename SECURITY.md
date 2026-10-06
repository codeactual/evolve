# Security Policy

## Reporting a vulnerability

Please report vulnerabilities privately via
[GitHub Security Advisories](https://github.com/codeactual/evolve/security/advisories/new). Do not open public issues
for security reports.

## Threat model (summary)

evolve is a Linux CLI that evaluates coding-agent plugins by driving real agent CLIs (Claude Code and Codex) in
throwaway workspaces and grading the results. The agents run with permission prompts off, so the containment layers
below are the control, not the prompts.

### What is untrusted

The **repository under test** is untrusted: its skills, eval fixtures, eval prompts and its own `.evolve.<ext>` config
can all carry prompt injection or hostile settings. Content the agents and the LLM judge read is untrusted too: an
agent's output, the files it leaves in its workspace, and the eval author's expected-output text.

The operator is trusted: the operator's own flags, `EVOLVE_*` environment, user-level config file, `PATH`, and the agent
CLIs they installed.

### What evolve enforces

- **Operator-only configuration.** The `sandbox.*` keys, `cache_dir` and `telemetry.*` steer the sandbox and host-side
  paths evolve writes to, so they come only from flags, `EVOLVE_*` environment variables, or the user-level config at
  `$XDG_CONFIG_HOME/evolve/config.<ext>` (default `~/.config/evolve/config.<ext>`). A repository `.evolve.<ext>` that
  sets any of them is rejected with exit 2, and so is the removed protected-roots key anywhere. Offered-model
  probes run from a fresh empty directory, and the Claude probe ignores project settings and MCP servers, so a hostile
  repository cannot run code through them.
- **A deny-by-default outer sandbox.** Every agent run and every command assertion executes inside bubblewrap with a
  fresh root that shows only:
  - the system directories and `/etc`, `/sys`, `/proc`, `/dev`;
  - the agent executable;
  - the repository under test, read-only;
  - the operator's default git config files (`~/.gitconfig` and `$XDG_CONFIG_HOME/git/config`, falling back to
    `~/.config/git/config`), read-only — never `git/credentials`;
  - the bridged credential files (Claude `.credentials.json`, Codex `auth.json`), read-only;
  - grants the operator lists in `sandbox.read_paths` (read-only) and `sandbox.write_paths` (read-write);
  - the run directory, read-write.

  The operator's home directory, other checkouts, `~/.ssh`, cloud credentials and shell rc files are not visible. `HOME`
  stays set but is not mounted: it is an ephemeral directory on the sandbox's writable root. A grant may not be `/` or
  an ancestor of the home directory. `bubblewrap` itself is validated before every run — an absolute path resolving to
  a regular file, not setuid or setgid, owned by root or the operator, and neither it nor any ancestor directory
  group- or other-writable — and is never executed to be checked.
- **The agents' own sandboxes stay on, layered inside.** Claude Code runs with its sandbox enabled, `failIfUnavailable`,
  unsandboxed commands disallowed and a strict network allowlist; Codex runs `read-only` for triggers and
  `workspace-write` for evals. Agent shell commands therefore get no network unless the operator opts in with
  `sandbox.claude_allowed_domains` or `sandbox.codex_network_access`. `evolve run` refuses to start (exit 2) when the
  nested sandboxes cannot start, and `evolve doctor` explains why.
- **An allowlisted environment.** Agents receive `PATH`, `HOME`, `XDG_CONFIG_HOME`, locale, terminal, proxy and TLS
  basics, the credential variables their own CLI reads, and the names in `sandbox.env_passthrough` — not the operator's
  whole shell. `GITHUB_TOKEN`, `AWS_*` and evolve's `EVOLVE_*` token-counting keys never reach an agent. Codex also runs
  with its own `*KEY*`/`*SECRET*`/`*TOKEN*` shell-environment excludes on.
- **A first-party-only agent surface.** evolve evaluates skills already on the filesystem, so agents are kept from
  reaching outward through their own tools. Claude runs with `--strict-mcp-config` and its in-process web, remote-trigger,
  push-notification, scheduling and messaging tools denied — those tools are not covered by the sandbox's network
  allowlist, so denying them is the only control. Codex runs with its connector, plugin (including the remote catalog and
  sharing), MCP-dependency-install, browser, computer-use and image-generation features disabled and web search off.
  Before any agent starts, `evolve run` runs a posture probe for each harness it will drive (Claude's is cancelled at its
  session init event and costs no tokens; Codex's lists its feature table) and exits 2 if the surface is not local-only
  — including a tool the CLI added since the surface was reviewed. The Tier 0 `checks.local_only` check rejects `.mcp.json`
  files and project or Codex config that enable MCP servers, plugins or marketplaces, or redirect a base URL.
- **A hardened LLM judge.** The judge runs in its own directory with its own fresh CLI config home, a read-only view of
  the workspace it grades, no code-running tools, hooks, MCP servers or project settings, and returns schema-constrained
  structured output that is decoded strictly. The agent's output and the expected-output text are quoted inside
  per-call random nonce fences and labelled untrusted evidence, and a verdict block quoted from that content can never
  stand in for the judge's answer.

Also in scope, as before: parsing of untrusted authored input. evolve reads authored config and trigger/eval spec files
in JSON, JSONC or YAML; decoding rejects malformed input with an error instead of crashing, and fixture path references
in eval files are constrained to the evals directory so they cannot escape it via traversal.

### Accepted residual risks

These are known and accepted; evolve does not claim to close them.

- **The network is shared.** The outer sandbox shares the host network, so an agent's own process can reach any host. A
  fixture's `.claude/settings.json` `env` block can point the agent's own API calls, together with its token, at another
  host. Agent *shell commands* are the layer with no network by default.
- **Bridged credential files are readable by the agent.** They are bound read-only, so an agent cannot write through to
  your real files, but its own process can read them, and its workspace symlinks to them outlive the run under
  `--keep-workspaces`.
- **A mid-run OAuth refresh cannot persist.** Because the credential files are read-only in the sandbox, a refresh
  during a run is lost, and with refresh-token rotation it can invalidate the stored login.
- **Together, those mean an agent process can read its bridged credential file and send it to any host.**
- **Tokens kept in the default-bound git config are exposed.** The read-only git config binds expose anything you keep
  in `~/.gitconfig` or `git/config` (for example `http.*.extraHeader` or token-bearing `insteadOf` URLs) to the agent,
  and so to any host.
- **Claude's shell commands can read env-provided credentials.** evolve forwards the credential variables the Claude CLI
  reads (`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_AUTH_TOKEN`) when you export them. Claude Code's
  `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` would strip them from its tool subprocesses, but in claude 2.1.285 it forces the
  permission mode to default, which ends the prompts-off eval posture, so evolve does not set it. A file-based login
  never reaches the environment.
- **Some outward surface remains.** Claude's bundled skills (16 in claude 2.1.285) cannot be removed without disabling
  the skill under test, and claude.ai account connectors are opted out with `ENABLE_CLAUDEAI_MCP_SERVERS=false` plus
  `--strict-mcp-config`, but none was ever listed at session start on the verification host, so those two are asserted
  by the posture probe rather than shown to work. Project settings hooks in a fixture still run (they are local), and
  `--setting-sources user`, which would drop fixture settings, is unusable because it also stops project skills loading.
  The agent process itself keeps its network access.
- **An LLM judge can still be persuaded.** The judge's attack surface is removed, but persuasive agent output or
  workspace content can still influence an LLM grader's verdicts; no design makes one immune.
- **evolve trusts your `PATH`** for `claude`, `codex` and `git`, and a validated `sandbox.bwrap_path`. An earlier
  `--no-sandbox` run of a hostile repository could have planted a binary in an operator-owned `PATH` directory.
- **`--no-sandbox` (or `sandbox.enabled=false`) removes the outer boundary.** The agents' own sandboxes still apply.
- **Tool caches need grants.** Toolchains and caches under `HOME` are not visible unless you grant them, which widens
  the visible surface by exactly what you list.

### Out of scope

The coding agents, providers and plugin repositories evolve evaluates — it invokes the tools and runs against the
repositories you point it at, and the operator decides which third-party plugins to evaluate. A compromise of the
machine running evolve, or of the kernel or bubblewrap itself, is also out of scope.

## Code scanning triage

CodeQL findings are triaged in [`security/code-scanning/index.md`](security/code-scanning/index.md), with a report per
finding recording why it was dismissed or how it was remediated.
