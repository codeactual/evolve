# Installation

`evolve` is a single Go binary for **Linux**. It drives the `claude` (Claude Code) and `codex` (OpenAI Codex) CLIs, so
the host needs those installed and logged in.

## Go install

Build from source with your Go toolchain:

```sh
go install github.com/codeactual/evolve/cmd/evolve@latest
```

The resulting binary reports its version as `dev`. To build this checkout instead:

```sh
make build
./builds/evolve version
```

## Prerequisites

evolve confines every agent run in a bubblewrap sandbox, and the agent CLIs nest their own sandboxes inside it:

- `bubblewrap` (`bwrap`), **non-setuid**, in a root- or operator-owned directory with no group- or other-writable
  ancestor. evolve validates it before every sandboxed run.
- `socat`, which Claude Code's sandbox needs for network filtering.
- Unprivileged user namespaces, **including nested ones**.

> [!WARNING]
> On Ubuntu 24.04 and later (`kernel.apparmor_restrict_unprivileged_userns=1`) AppArmor's `bwrap-userns-restrict`
> profile attaches to `/usr/bin/bwrap` and strips the capabilities of that bubblewrap's descendants, so the nested
> sandboxes cannot start and `evolve run` refuses to run (exit 2). Copy bubblewrap to a root- or operator-owned
> directory outside that profile (for example `~/.cache/evolve/bwrap`, mode `0755`, in a directory nothing but you can
> write to), and point `sandbox.bwrap_path` at the copy (`EVOLVE_SANDBOX_BWRAP_PATH` or `--bwrap-path` also work).
> The copy does not follow package upgrades of bubblewrap; refresh it by hand. evolve exposes the validated binary
> first on the sandbox's `PATH`, so the agent CLIs' nested sandboxes use the same copy.

The versions the sandbox behavior was verified against (on 2026-10-05) are `claude` 2.1.289 and `codex` 0.160.0. evolve
does not gate on a CLI version: an older CLI that lacks a flag evolve passes fails per case with its own unknown-option error.

## Verify the environment

Run `evolve doctor` from a plugin repository to check the agent CLIs, credentials, token-counting access, and the
sandbox:

```sh
evolve doctor
```

Its SANDBOX section reports the bubblewrap path and provenance verdict, an outer smoke run, a nested-bubblewrap probe
(with the AppArmor remedy above when it fails), and whether `socat` is installed.

## Verify a build

`make ci` runs the gates (vet, formatting, race tests, static analysis, build). `make security_scan` runs the supply-chain
scan (osv-scanner and trivy) and is deliberately not part of `ci`.
