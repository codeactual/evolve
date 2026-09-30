// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

//go:build linux

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codeactual/evolve/internal/model"
)

// systemDirs are the top-level directories a merged-/usr host links to /usr, or
// carries as real directories on older layouts. Each one that exists on the host
// is reproduced inside the sandbox (as a symlink or a read-only bind) so dynamic
// loaders and #! interpreters resolve.
var systemDirs = []string{"/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32"}

// hostRoot inspects the host's root filesystem layout. root is "/" on a real
// host; tests point it at a fixture tree so the layout-dependent argv (symlinked
// /lib, a resolv.conf outside /etc) is exercised without a particular distro.
type hostRoot struct{ root string }

func (h hostRoot) path(p string) string { return filepath.Join(h.root, p) }

// resolveLink follows p through symlinks within the host root and returns the
// final, existing, non-symlink path (as a host-absolute path), or "" when the
// chain dangles or loops.
func (h hostRoot) resolveLink(p string) string {
	for range 40 {
		info, err := os.Lstat(h.path(p))
		if err != nil {
			return ""
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return p
		}
		target, err := os.Readlink(h.path(p))
		if err != nil {
			return ""
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		p = target
	}
	return ""
}

// sandboxPlan is one run's fully resolved sandbox inputs, separated from the
// argv builder so the argv is a pure function of it.
type sandboxPlan struct {
	bwrap         string   // the validated bubblewrap
	argv          []string // the command, Argv[0] resolved through symlinks
	repoRoot      string   // read-only
	gitConfigs    []string // existing default git config files, read-only
	readPaths     []string // operator grants, read-only
	specReadPaths []string // the spec's ReadPaths, read-only
	writePaths    []string // operator grants, read-write
	dir           string   // the run directory, read-write
}

// plan resolves the sandbox inputs for spec. Missing spec.ReadPaths entries are
// dropped (removing a read bind only narrows access); operator grants were
// validated by NewSandbox.
func (s Sandbox) plan(spec model.CommandSpec, bwrap string) sandboxPlan {
	argv := append([]string(nil), spec.Argv...)
	argv[0] = resolveExecutable(argv[0])
	p := sandboxPlan{
		bwrap:      bwrap,
		argv:       argv,
		repoRoot:   resolvePath(s.RepoRoot),
		gitConfigs: defaultGitConfigs(),
		readPaths:  s.ReadPaths,
		writePaths: s.WritePaths,
		dir:        resolvePath(spec.Dir),
	}
	for _, r := range spec.ReadPaths {
		if _, err := os.Stat(r); err == nil {
			p.specReadPaths = append(p.specReadPaths, resolvePath(r))
		}
	}
	return p
}

// resolveExecutable returns the real path of the command's executable: a bare
// name is looked up on PATH, and symlinks are followed, because only the
// resolved location is bound inside the sandbox. An unresolvable command is
// returned unchanged and fails visibly when launched.
func resolveExecutable(argv0 string) string {
	if !strings.Contains(argv0, "/") {
		if found, err := exec.LookPath(argv0); err == nil {
			argv0 = found
		}
	}
	if abs, err := filepath.Abs(argv0); err == nil {
		argv0 = abs
	}
	if resolved, err := filepath.EvalSymlinks(argv0); err == nil {
		return resolved
	}
	return argv0
}

// defaultGitConfigs lists the operator's git config files that exist and are
// bound read-only by default: ~/.gitconfig, and $XDG_CONFIG_HOME/git/config
// (falling back to ~/.config/git/config). Only the config file is ever bound,
// never the git/ directory, so git-credential-store's git/credentials stays
// hidden.
func defaultGitConfigs() []string {
	home, _ := os.UserHomeDir()
	candidates := []string{}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".gitconfig"))
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".config")
	}
	if xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "git", "config"))
	}
	var out []string
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

// sandboxArgv renders the bubblewrap command line for plan: a fresh root that
// shows only what is bound below. Arguments apply in order and a later mount
// shadows an earlier one, so the run directory comes last and stays writable
// even when it sits inside a read-only grant. The network is shared (agents and
// dependency tooling need it) and the sandboxed process dies with the runner, so
// a killed sweep leaves nothing behind.
func sandboxArgv(p sandboxPlan, host hostRoot) []string {
	a := []string{p.bwrap, "--unshare-all", "--share-net", "--die-with-parent", "--ro-bind", "/usr", "/usr"}
	for _, d := range systemDirs {
		info, err := os.Lstat(host.path(d))
		switch {
		case err != nil:
		case info.Mode()&os.ModeSymlink != 0:
			if target, err := os.Readlink(host.path(d)); err == nil {
				a = append(a, "--symlink", target, d)
			}
		case info.IsDir():
			a = append(a, "--ro-bind", d, d)
		}
	}
	a = append(a, "--ro-bind", "/etc", "/etc")
	// resolv.conf is often a symlink out of /etc (systemd-resolved): bind its
	// target too, or name resolution breaks. An unresolvable target is skipped:
	// DNS failures surface in the agent.
	if t := host.resolveLink("/etc/resolv.conf"); t != "" && !strings.HasPrefix(t, "/etc/") {
		a = append(a, "--ro-bind", t, t)
	}
	a = append(a, "--ro-bind", "/sys", "/sys", "--proc", "/proc", "--dev", "/dev",
		"--tmpfs", "/tmp", "--tmpfs", "/var/tmp")
	a = appendBind(a, "--ro-bind", filepath.Dir(p.argv[0]))
	if p.repoRoot != "" {
		a = appendBind(a, "--ro-bind", p.repoRoot)
	}
	for _, group := range []struct {
		flag  string
		paths []string
	}{
		{"--ro-bind", p.gitConfigs},
		{"--ro-bind", p.readPaths},
		{"--ro-bind", p.specReadPaths},
		{"--bind", p.writePaths},
		{"--bind", []string{p.dir}},
	} {
		for _, path := range group.paths {
			a = appendBind(a, group.flag, path)
		}
	}
	a = append(a, "--chdir", p.dir, "--")
	return append(a, p.argv...)
}

// appendBind appends a same-path bind of p.
func appendBind(a []string, flag, p string) []string {
	return append(a, flag, p, p)
}
