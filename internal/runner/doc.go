// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

// Package runner executes model.CommandSpecs. It is the only package that
// touches os/exec, so every engine can be tested against a fake.
//
// Agent CLIs spawn children, so cancellation kills the whole process group —
// killing only the parent leaks grandchildren that hold the stdout pipe open.
//
// With a Sandbox enabled, every command runs inside bubblewrap with a
// deny-by-default filesystem: a fresh root that shows only the system
// directories, the agent executable, the repository under test (read-only), the
// operator's git config files and the spec's ReadPaths (read-only), the
// operator's grants, and the run directory. The operator's home directory is
// not mounted; HOME stays set and is an ephemeral directory on the sandbox's
// writable root. The bubblewrap binary itself is validated before every run
// (see ResolveBwrap).
package runner
