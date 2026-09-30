// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

//go:build linux

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeactual/evolve/internal/model"
)

// The live tests below run real bubblewrap, so they prove the policy the argv
// tests only describe. They are part of `make ci`: when bubblewrap (or the
// unprivileged user namespaces it needs) is missing they fail and name the
// dependency instead of skipping.

// liveEnv is a hermetic stand-in for one operator's machine: a HOME holding
// canaries, a repository under test, and a run directory.
type liveEnv struct {
	home, repo, ws string
	sandbox        Sandbox
}

func newLiveEnv(t *testing.T, cfg SandboxConfig) liveEnv {
	t.Helper()
	if _, err := ResolveBwrap(""); err != nil {
		t.Fatalf("the live sandbox tests need a non-setuid bubblewrap (bwrap) on PATH and unprivileged user namespaces: %v", err)
	}
	e := liveEnv{home: t.TempDir(), repo: t.TempDir(), ws: t.TempDir()}
	t.Setenv("HOME", e.home)
	t.Setenv("XDG_CONFIG_HOME", "")
	cfg.RepoRoot = e.repo
	sb, err := NewSandbox(cfg)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	e.sandbox = sb
	return e
}

// run executes script (as `sh -c script arg0 args...`) inside the sandbox.
func (e liveEnv) run(t *testing.T, spec model.CommandSpec) Result {
	t.Helper()
	if spec.Dir == "" {
		spec.Dir = e.ws
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := (&Exec{Sandbox: e.sandbox}).Run(ctx, spec, 20*time.Second, nil)
	if err != nil {
		t.Fatalf("sandboxed run: %v", err)
	}
	return res
}

func script(body string, args ...string) model.CommandSpec {
	return model.CommandSpec{Argv: append([]string{"/bin/sh", "-c", body, "sh"}, args...)}
}

func mustFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLiveSandboxHidesOperatorHome(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	mustFile(t, filepath.Join(e.home, "canary"), "secret")
	mustFile(t, filepath.Join(e.home, ".ssh", "id_ed25519"), "key")
	res := e.run(t, script(`test ! -e "$HOME/canary" && test ! -e "$HOME/.ssh/id_ed25519"`))
	if res.ExitCode != 0 {
		t.Errorf("operator HOME content is visible inside the sandbox (exit %d, stderr %q)", res.ExitCode, res.StderrTail)
	}
}

func TestLiveSandboxHomeEphemeral(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	res := e.run(t, script(`mkdir -p "$HOME/.cache/x" && echo hi > "$HOME/.cache/x/f"`))
	if res.ExitCode != 0 {
		t.Fatalf("writing under the unmounted HOME failed (exit %d, stderr %q); it must succeed on the sandbox's ephemeral root", res.ExitCode, res.StderrTail)
	}
	if _, err := os.Stat(filepath.Join(e.home, ".cache")); !os.IsNotExist(err) {
		t.Errorf("a HOME write reached the host (stat err = %v)", err)
	}
}

func TestLiveSandboxGitIdentity(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	mustFile(t, filepath.Join(e.home, ".gitconfig"), "[user]\n\temail = ident@example.test\n")
	res := e.run(t, script(`git config user.email; echo x >> "$HOME/.gitconfig"`))
	if !strings.Contains(string(res.Stdout), "ident@example.test") {
		t.Errorf("git config user.email = %q, want the pre-seeded identity", res.Stdout)
	}
	if res.ExitCode == 0 || !strings.Contains(res.StderrTail, "Read-only file system") {
		t.Errorf("writing ~/.gitconfig: exit %d, stderr %q; want an EROFS failure", res.ExitCode, res.StderrTail)
	}
	if got, _ := os.ReadFile(filepath.Join(e.home, ".gitconfig")); strings.Contains(string(got), "x\n") {
		t.Error("the host .gitconfig was modified")
	}
}

func TestLiveSandboxGitIdentityXDG(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	mustFile(t, filepath.Join(xdg, "git", "config"), "[user]\n\temail = xdg@example.test\n")
	mustFile(t, filepath.Join(xdg, "git", "credentials"), "https://tok@example.test\n")
	res := e.run(t, script(`git config user.email; test ! -e "$XDG_CONFIG_HOME/git/credentials" && echo hidden`))
	if !strings.Contains(string(res.Stdout), "xdg@example.test") {
		t.Errorf("git config user.email = %q, want the XDG identity", res.Stdout)
	}
	if !strings.Contains(string(res.Stdout), "hidden") {
		t.Errorf("git/credentials is visible inside the sandbox (stdout %q, stderr %q)", res.Stdout, res.StderrTail)
	}
}

func TestLiveSandboxHidesHostTmp(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	canaryDir, err := os.MkdirTemp("/tmp", "evolve-canary-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(canaryDir) })
	mustFile(t, filepath.Join(canaryDir, "canary"), "secret")
	res := e.run(t, script(`test ! -e "$1" && echo "$TMPDIR"`, filepath.Join(canaryDir, "canary")))
	if res.ExitCode != 0 {
		t.Errorf("a host /tmp canary is visible inside the sandbox (exit %d)", res.ExitCode)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != "/tmp" {
		t.Errorf("TMPDIR inside the sandbox = %q, want /tmp", got)
	}
}

func TestLiveSandboxRunDirWritable(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	res := e.run(t, script(`echo made > out.txt`))
	if res.ExitCode != 0 {
		t.Fatalf("writing in the run directory failed (exit %d, stderr %q)", res.ExitCode, res.StderrTail)
	}
	if got, err := os.ReadFile(filepath.Join(e.ws, "out.txt")); err != nil || string(got) != "made\n" {
		t.Errorf("host out.txt = %q, %v; want the sandboxed write to reach the run directory", got, err)
	}
}

func TestLiveSandboxRepoReadOnly(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	mustFile(t, filepath.Join(e.repo, "SKILL.md"), "skill")
	res := e.run(t, script(`cat "$1/SKILL.md" && echo x > "$1/new.txt"`, e.repo))
	if !strings.Contains(string(res.Stdout), "skill") {
		t.Errorf("the repository is not readable inside the sandbox (stdout %q)", res.Stdout)
	}
	if res.ExitCode == 0 || !strings.Contains(res.StderrTail, "Read-only file system") {
		t.Errorf("writing under the repository: exit %d, stderr %q; want an EROFS failure", res.ExitCode, res.StderrTail)
	}
	if _, err := os.Stat(filepath.Join(e.repo, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("a write reached the host repository (stat err = %v)", err)
	}
}

func TestLiveSandboxReadPathsReadOnly(t *testing.T) {
	grant := t.TempDir()
	mustFile(t, filepath.Join(grant, "tool.txt"), "granted")
	e := newLiveEnv(t, SandboxConfig{ReadPaths: []string{grant}})
	res := e.run(t, script(`cat "$1/tool.txt" && echo x > "$1/new.txt"`, grant))
	if !strings.Contains(string(res.Stdout), "granted") {
		t.Errorf("the read grant is not readable (stdout %q)", res.Stdout)
	}
	if res.ExitCode == 0 || !strings.Contains(res.StderrTail, "Read-only file system") {
		t.Errorf("writing a read grant: exit %d, stderr %q; want an EROFS failure", res.ExitCode, res.StderrTail)
	}
}

func TestLiveSandboxWritePathsWritable(t *testing.T) {
	grant := t.TempDir()
	e := newLiveEnv(t, SandboxConfig{WritePaths: []string{grant}})
	res := e.run(t, script(`echo made > "$1/out.txt"`, grant))
	if res.ExitCode != 0 {
		t.Fatalf("writing a write grant failed (exit %d, stderr %q)", res.ExitCode, res.StderrTail)
	}
	if _, err := os.Stat(filepath.Join(grant, "out.txt")); err != nil {
		t.Errorf("the write grant did not receive the file: %v", err)
	}
}

func TestLiveSandboxCredentialBinding(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	cred := filepath.Join(e.home, ".claude", ".credentials.json")
	const body = `{"claudeAiOauth":{"accessToken":"x"}}`
	mustFile(t, cred, body)
	spec := script(`cat "$1" && echo changed > "$1"`, cred)
	spec.ReadPaths = []string{cred}
	res := e.run(t, spec)
	if !strings.Contains(string(res.Stdout), body) {
		t.Errorf("the bridged credential file is not readable inside (stdout %q)", res.Stdout)
	}
	if res.ExitCode == 0 || !strings.Contains(res.StderrTail, "Read-only file system") {
		t.Errorf("writing the credential file: exit %d, stderr %q; want an EROFS failure", res.ExitCode, res.StderrTail)
	}
	if got, _ := os.ReadFile(cred); string(got) != body {
		t.Errorf("the host credential file changed to %q", got)
	}
}

// TestLiveSandboxSymlinkedExecutable pins that an Argv[0] symlink in one hidden
// directory, pointing at a binary in another, still runs: the sandbox resolves
// the symlink and binds the resolved directory.
func TestLiveSandboxSymlinkedExecutable(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	binDir, linkDir := t.TempDir(), t.TempDir()
	tool := filepath.Join(binDir, "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho tool-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "tool")
	if err := os.Symlink(tool, link); err != nil {
		t.Fatal(err)
	}
	res := e.run(t, model.CommandSpec{Argv: []string{link}})
	if strings.TrimSpace(string(res.Stdout)) != "tool-ran" || res.ExitCode != 0 {
		t.Errorf("symlinked executable: exit %d, stdout %q, stderr %q", res.ExitCode, res.Stdout, res.StderrTail)
	}
}

func TestSandboxedRunNeedsRunDirectory(t *testing.T) {
	if _, err := ResolveBwrap(""); err != nil {
		t.Fatalf("bubblewrap is required: %v", err)
	}
	sb, err := NewSandbox(SandboxConfig{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sb.wrap(model.CommandSpec{Argv: []string{"/bin/true"}})
	if err == nil || !strings.Contains(err.Error(), "run directory") {
		t.Errorf("wrap without a Dir = %v, want a fail-closed run-directory error", err)
	}
}

// TestProbeSandboxOuter runs the outer smoke probe for real.
func TestProbeSandboxOuter(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	if err := ProbeSandbox(context.Background(), e.sandbox); err != nil {
		t.Errorf("ProbeSandbox: %v", err)
	}
	if err := ProbeSandbox(context.Background(), Sandbox{}); err != nil {
		t.Errorf("ProbeSandbox(disabled) = %v, want nil", err)
	}
}

// TestLiveSandboxExposesBwrapFirstOnPath pins that the validated bubblewrap is
// the first `bwrap` on the sandbox's PATH, so the agent CLIs' nested sandboxes
// use it rather than the (possibly AppArmor-profiled) system one.
func TestLiveSandboxExposesBwrapFirstOnPath(t *testing.T) {
	e := newLiveEnv(t, SandboxConfig{})
	res := e.run(t, script(`command -v bwrap; echo "$PATH"`))
	lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	if res.ExitCode != 0 || len(lines) < 2 || lines[0] != "/evolve/bin/bwrap" || !strings.HasPrefix(lines[1], "/evolve/bin:") {
		t.Errorf("bwrap inside the sandbox: exit %d, stdout %q, stderr %q; want /evolve/bin/bwrap first on PATH",
			res.ExitCode, res.Stdout, res.StderrTail)
	}
}
