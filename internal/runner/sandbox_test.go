// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/codeactual/evolve/internal/model"
)

func TestSandboxDisabledIsPassthrough(t *testing.T) {
	argv := []string{"/usr/bin/claude", "-p", "hi"}
	spec := model.CommandSpec{Argv: argv, Dir: "/tmp/ws"}
	got, err := Sandbox{Enabled: false, RepoRoot: "/home/x/Repos/r"}.wrap(spec)
	if err != nil {
		t.Fatalf("wrap() error = %v, want nil", err)
	}
	if !slices.Equal(got, argv) {
		t.Fatalf("wrap() = %v, want unchanged %v", got, argv)
	}
}

func TestResolvePathDropsEmpties(t *testing.T) {
	if got := resolvePaths([]string{"", ""}); len(got) != 0 {
		t.Fatalf("resolvePaths(empties) = %v, want none", got)
	}
}

// hostFixture builds a stand-in for the host's root directory: the
// system directories the sandbox argv inspects, with /bin, /sbin and /lib as
// symlinks into /usr the way merged-/usr hosts lay them out.
func hostFixture(t *testing.T) hostRoot {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"usr/bin", "usr/sbin", "usr/lib", "etc", "sys"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"bin": "usr/bin", "sbin": "usr/sbin", "lib": "usr/lib"} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	return hostRoot{root: root}
}

// fixturePlan is a fully resolved plan over invented paths.
func fixturePlan() sandboxPlan {
	return sandboxPlan{
		bwrap:         "/usr/bin/bwrap",
		argv:          []string{"/usr/local/bin/claude", "-p", "hi"},
		repoRoot:      "/work/repo",
		gitConfigs:    []string{"/home/u/.gitconfig"},
		readPaths:     []string{"/opt/tools"},
		specReadPaths: []string{"/home/u/.claude/.credentials.json"},
		writePaths:    []string{"/home/u/go"},
		dir:           "/tmp/ws",
	}
}

func TestSandboxArgvOrder(t *testing.T) {
	want := []string{
		"/usr/bin/bwrap", "--unshare-all", "--share-net", "--die-with-parent",
		"--ro-bind", "/usr", "/usr",
		"--symlink", "usr/bin", "/bin",
		"--symlink", "usr/sbin", "/sbin",
		"--symlink", "usr/lib", "/lib",
		"--ro-bind", "/etc", "/etc",
		"--ro-bind", "/sys", "/sys",
		"--proc", "/proc", "--dev", "/dev",
		"--tmpfs", "/tmp", "--tmpfs", "/var/tmp",
		"--ro-bind", "/usr/local/bin", "/usr/local/bin",
		"--ro-bind", "/work/repo", "/work/repo",
		"--ro-bind", "/home/u/.gitconfig", "/home/u/.gitconfig",
		"--ro-bind", "/opt/tools", "/opt/tools",
		"--ro-bind", "/home/u/.claude/.credentials.json", "/home/u/.claude/.credentials.json",
		"--bind", "/home/u/go", "/home/u/go",
		"--bind", "/tmp/ws", "/tmp/ws",
		"--chdir", "/tmp/ws",
		"--",
		"/usr/local/bin/claude", "-p", "hi",
	}
	got := sandboxArgv(fixturePlan(), hostFixture(t))
	if !slices.Equal(got, want) {
		t.Fatalf("sandboxArgv =\n%v\nwant\n%v", got, want)
	}
	// The run directory is the last bind, so it stays writable even when it
	// sits inside an earlier read-only grant (later binds shadow earlier ones).
	lastBind := -1
	for i, a := range got {
		if a == "--bind" || a == "--ro-bind" {
			lastBind = i
		}
	}
	if got[lastBind] != "--bind" || got[lastBind+1] != "/tmp/ws" {
		t.Errorf("last bind = %v, want the run directory", got[lastBind:lastBind+3])
	}
}

func TestSandboxArgvLibSymlinks(t *testing.T) {
	host := hostFixture(t)
	if err := os.Mkdir(filepath.Join(host.root, "lib64"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(sandboxArgv(fixturePlan(), host), " ")
	for _, want := range []string{"--symlink usr/lib /lib", "--ro-bind /lib64 /lib64"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv lacks %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"/lib32", "/libx32"} {
		if strings.Contains(got, absent) {
			t.Errorf("argv mentions %s although the host lacks it:\n%s", absent, got)
		}
	}
}

func TestSandboxArgvResolvConfOutsideEtc(t *testing.T) {
	host := hostFixture(t)
	stub := filepath.Join(host.root, "run", "systemd", "resolve")
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stub, "stub-resolv.conf"), []byte("nameserver 127.0.0.53\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../run/systemd/resolve/stub-resolv.conf", filepath.Join(host.root, "etc", "resolv.conf")); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(sandboxArgv(fixturePlan(), host), " ")
	want := "--ro-bind /etc /etc --ro-bind /run/systemd/resolve/stub-resolv.conf /run/systemd/resolve/stub-resolv.conf --ro-bind /sys /sys"
	if !strings.Contains(got, want) {
		t.Errorf("argv lacks the resolv.conf target bind right after /etc:\n%s\nwant substring %q", got, want)
	}

	// A target that stays under /etc needs no extra bind.
	host = hostFixture(t)
	if err := os.MkdirAll(filepath.Join(host.root, "etc", "resolvconf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(host.root, "etc", "resolvconf", "resolv.conf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("resolvconf/resolv.conf", filepath.Join(host.root, "etc", "resolv.conf")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sandboxArgv(fixturePlan(), host), " "); strings.Contains(got, "resolvconf") {
		t.Errorf("a resolv.conf target inside /etc must not add a bind:\n%s", got)
	}

	// An unresolvable target is skipped rather than failing the run.
	host = hostFixture(t)
	if err := os.Symlink("/run/gone/resolv.conf", filepath.Join(host.root, "etc", "resolv.conf")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sandboxArgv(fixturePlan(), host), " "); strings.Contains(got, "/run/gone") {
		t.Errorf("a dangling resolv.conf target must be skipped:\n%s", got)
	}
}

func TestSandboxArgvReadPaths(t *testing.T) {
	plan := fixturePlan()
	plan.specReadPaths = []string{"/home/u/.claude/.credentials.json", "/work/ws-view"}
	got := sandboxArgv(plan, hostFixture(t))
	runDir := slices.Index(got, "/tmp/ws")
	for _, p := range plan.specReadPaths {
		i := slices.Index(got, p)
		if i < 1 || got[i-1] != "--ro-bind" || got[i+1] != p {
			t.Errorf("spec read path %s is not a --ro-bind pair: %v", p, got)
		}
		if i > runDir {
			t.Errorf("spec read path %s comes after the run directory bind", p)
		}
	}
}

// TestSandboxArgvNoHomeMount pins that HOME and its ancestors are never bound
// by default: the only HOME descendants visible are the default git config
// files and the bridged credential files.
func TestSandboxArgvNoHomeMount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, f := range []string{".gitconfig", ".ssh/id_ed25519", ".bashrc", ".aws/credentials"} {
		mustWriteFile(t, filepath.Join(home, f), "x")
	}
	cred := filepath.Join(home, ".claude", ".credentials.json")
	mustWriteFile(t, cred, "{}")

	ws := t.TempDir()
	s := Sandbox{Enabled: true, RepoRoot: t.TempDir()}
	plan := s.plan(model.CommandSpec{Argv: []string{"/bin/sh"}, Dir: ws, ReadPaths: []string{cred}}, "/usr/bin/bwrap")
	argv := sandboxArgv(plan, hostFixture(t))

	var binds []string
	for i, a := range argv {
		if a == "--bind" || a == "--ro-bind" {
			binds = append(binds, argv[i+2])
		}
	}
	for _, dest := range binds {
		if dest == home || strings.HasPrefix(home, dest+string(os.PathSeparator)) {
			if dest == "/usr" || dest == "/etc" || dest == "/sys" { // fixture HOME never sits under these
				continue
			}
			t.Errorf("argv binds %s, which is HOME or one of its ancestors", dest)
		}
		if strings.HasPrefix(dest, home+string(os.PathSeparator)) &&
			dest != filepath.Join(home, ".gitconfig") && dest != cred {
			t.Errorf("argv binds %s, a HOME descendant that is neither a git config nor a bridged credential file", dest)
		}
	}
	for _, want := range []string{filepath.Join(home, ".gitconfig"), cred} {
		if !slices.Contains(binds, want) {
			t.Errorf("argv lacks the bind of %s (binds: %v)", want, binds)
		}
	}
}

func TestSandboxArgvGitConfigBinds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	mustWriteFile(t, filepath.Join(home, ".gitconfig"), "[user]\n\temail = a@example.test\n")
	mustWriteFile(t, filepath.Join(home, ".config", "git", "config"), "[user]\n\tname = A\n")
	mustWriteFile(t, filepath.Join(home, ".config", "git", "credentials"), "https://tok@example.test\n")

	gitBinds := func() []string {
		s := Sandbox{Enabled: true, RepoRoot: t.TempDir()}
		plan := s.plan(model.CommandSpec{Argv: []string{"/bin/sh"}, Dir: t.TempDir()}, "/usr/bin/bwrap")
		return plan.gitConfigs
	}

	want := []string{filepath.Join(home, ".gitconfig"), filepath.Join(home, ".config", "git", "config")}
	if got := gitBinds(); !slices.Equal(got, want) {
		t.Errorf("default git config binds = %v, want %v (credentials must never be bound)", got, want)
	}

	// With XDG_CONFIG_HOME set, its git/config replaces the ~/.config fallback.
	xdg := t.TempDir()
	mustWriteFile(t, filepath.Join(xdg, "git", "config"), "[user]\n\temail = x@example.test\n")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	want = []string{filepath.Join(home, ".gitconfig"), filepath.Join(xdg, "git", "config")}
	if got := gitBinds(); !slices.Equal(got, want) {
		t.Errorf("git config binds with XDG_CONFIG_HOME = %v, want %v", got, want)
	}

	// Absent files emit no bind.
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := gitBinds(); len(got) != 0 {
		t.Errorf("git config binds with no config files = %v, want none", got)
	}
}

func mustWriteFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxRejectsHomeAncestorGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, grant := range []string{"/", filepath.Dir(home)} {
		_, err := NewSandbox(SandboxConfig{RepoRoot: t.TempDir(), ReadPaths: []string{grant}})
		if err == nil || !strings.Contains(err.Error(), grant) {
			t.Errorf("NewSandbox(read_paths=[%q]) error = %v, want a rejection naming the entry", grant, err)
		}
	}
	// The home directory itself, and anything inside it, is allowed.
	inside := filepath.Join(home, "go")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, grant := range []string{home, inside} {
		if _, err := NewSandbox(SandboxConfig{RepoRoot: t.TempDir(), ReadPaths: []string{grant}}); err != nil {
			t.Errorf("NewSandbox(read_paths=[%q]) = %v, want accepted", grant, err)
		}
	}
	// The same refusal covers write grants.
	if _, err := NewSandbox(SandboxConfig{RepoRoot: t.TempDir(), WritePaths: []string{"/"}}); err == nil {
		t.Error("NewSandbox(write_paths=[/]) accepted, want rejected")
	}
}

func TestSandboxRejectsRelativeGrant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := NewSandbox(SandboxConfig{RepoRoot: t.TempDir(), ReadPaths: []string{"relative/dir"}})
	if err == nil || !strings.Contains(err.Error(), "relative/dir") || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error = %v, want a relative-path rejection naming the entry", err)
	}
}

func TestSandboxRejectsMissingGrant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := NewSandbox(SandboxConfig{RepoRoot: t.TempDir(), WritePaths: []string{missing}})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %v, want a missing-path rejection naming the entry", err)
	}
}

func TestSandboxExpandsTildeAndEnvInGrants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("EVOLVE_TEST_GRANT", filepath.Join(home, "tools"))
	for _, d := range []string{"go", "tools"} {
		if err := os.Mkdir(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewSandbox(SandboxConfig{
		RepoRoot:   t.TempDir(),
		ReadPaths:  []string{"$EVOLVE_TEST_GRANT"},
		WritePaths: []string{"~/go"},
	})
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	if !slices.Equal(s.ReadPaths, []string{filepath.Join(home, "tools")}) ||
		!slices.Equal(s.WritePaths, []string{filepath.Join(home, "go")}) {
		t.Errorf("grants = %v / %v, want the expanded absolute paths", s.ReadPaths, s.WritePaths)
	}
}
