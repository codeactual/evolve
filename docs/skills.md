# Authoring skills

A _skill_ is a folder of instructions a coding agent loads on demand: a `SKILL.md` file whose frontmatter tells the
agent _when_ to reach for it and whose body tells it _how_. evolve evaluates skills, so this page covers the shape it
expects, what its Tier 0 checks require, and the advisory quality signals it reports.

> [!NOTE]
> **We are not the authority on skill design.** There is no settled science to writing a great skill, and we do not
> pretend to own it. The canonical guidance lives upstream at **[agentskills.io](https://agentskills.io/)** — start
> there. evolve only encodes the handful of structural rules its tooling needs to run, plus a couple of deterministic
> signals that nudge toward smaller, clearer skills. Treat the signals as hints, not verdicts.

## Layout

A skill never stands alone; it ships inside a _plugin_. The smallest case is a single-plugin repository whose root is
the plugin itself:

```text
my-plugin/
├── .claude-plugin/plugin.json   # Claude manifest: name, version
├── .codex-plugin/plugin.json    # Codex manifest: must agree with Claude's
└── skills/
    └── my-skill/                # one directory per skill
        └── SKILL.md             # frontmatter + instructions
```

Repositories can also hold many plugins under `plugins/`, optionally fronted by a marketplace manifest. evolve detects
which shape it is looking at; see [Supported repositories](getting-started.md) for the three layouts. Whatever the
shape, a skill is always a directory under a plugin's `skills/` containing a `SKILL.md`.

## Frontmatter

`SKILL.md` opens with a YAML frontmatter block, then the instructional body:

```markdown
---
name: my-skill
description: Use when wrapping Go errors so the chain is preserved with %w.
---

# My skill

Step-by-step guidance for the agent…
```

| Field         | Required | What evolve checks                                                                                                              |
| ------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `name`        | yes      | Matches the skill's directory name; kebab-case (`a–z`, `0–9`, hyphens); at most 64 characters.                                  |
| `description` | yes      | At most 1024 characters, and contains a trigger phrase matching `Use (when\|after\|before)` so the agent knows when to load it. |
| `license`     | no       | Forbidden by default. Permitted only when you set `checks.license`, and then it must match exactly.                             |

The trigger phrase is the load-bearing part: an agent decides whether to open a skill almost entirely from its
description, so "Use when…/after…/before…" is required rather than cosmetic.

## What `evolve run checks` requires

[Tier 0 checks](getting-started.md) are static and deterministic — no agents run. They are pass/fail gates; a finding
fails the run. The rules, grouped:

### Plugin

- A Claude manifest at `.claude-plugin/plugin.json`, and (by default) a Codex manifest at `.codex-plugin/plugin.json`.
- The manifests are JSON objects that agree on `name` and `version`, with `version` a strict `MAJOR.MINOR.PATCH` semver.
- No `hooks/` directory — Codex discovers `hooks.json` under an incompatible schema, so it is rejected.
- At least one skill under `skills/`.

### Skill

- Valid YAML frontmatter in `SKILL.md` satisfying the [field rules above](#frontmatter).
- A body no longer than the line cap (`checks.max_skill_lines`, default 500).

### Marketplace

Only in marketplace repositories:

- Both the Claude and Codex marketplace manifests are present, name an owner, and list the same `./`-prefixed plugin
  sources, each resolving to a real directory.

### Eval definitions

- Any authored `triggers`/`evals` files parse and validate, and every `evals/<skill>` directory has a matching skill.

Each rule has a knob under `checks.*` — the line cap, the trigger pattern, whether the Codex manifest is required, the
license, and so on. See the [configuration reference](config/index.md) for the full surface.

## Skill-quality signals

Alongside the pass/fail findings, `evolve run checks` prints a short **advisory** table of 0–100 scores (higher is
better). These signals never fail a run — pass `--no-signals` to hide them. They are pure functions of the `SKILL.md`
bytes (no model, no network), which is why they live in the deterministic checks tier and stay reproducible.

| Signal            | Measures                                 | Full marks (100)                             | Zero                                                                       |
| ----------------- | ---------------------------------------- | -------------------------------------------- | -------------------------------------------------------------------------- |
| **size**          | `SKILL.md` line count                    | at or below `checks.ideal_skill_lines` (200) | at `checks.max_skill_lines` (500) — beyond the cap is a hard check failure |
| **conciseness**   | composite of the three proxies below     | —                                            | —                                                                          |
| ↳ sentence length | average words per sentence in the prose  | ≤ 20 words                                   | ≥ 35 words                                                                 |
| ↳ hedging         | vague / filler words per 100 prose words | 0                                            | ≥ 6                                                                        |
| ↳ redundancy      | share of duplicated lines                | 0%                                           | ≥ 15%                                                                      |

The **overall** column is the mean of size and conciseness.

The size score is non-linear: it holds near full marks around the ideal, then falls off increasingly fast as the line
count approaches the cap — so the score doubles as an early warning that a skill is about to breach the hard limit. The
only knob exposed is the ideal (`checks.ideal_skill_lines`); it must sit below the cap with headroom, and
`evolve doctor` errors if it is set too high.

> [!NOTE]
> **Signals are weak proxies, on purpose.** Conciseness is measured from surface text — sentence length, filler words,
> repeated lines — so it cannot tell "verbose but necessary" from "verbose and bloated", and a long, workflow-heavy
> skill may score low for good reasons. We deliberately do **not** score subjective qualities like ambiguity, which
> would need an LLM judge rather than a reproducible static check. Read the signals as a nudge to look again, never as
> a grade.

## Further reading

- **[agentskills.io](https://agentskills.io/)** — the upstream guide to designing and writing skills.
- [Authoring evaluations](evaluations/index.md) — once a skill passes checks, write triggers and behavioral evals for
  it.
- [Configuration](config/index.md) — every `checks.*` knob and its default.
- `evolve run checks --help` — the command reference.
