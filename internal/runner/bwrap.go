// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

//go:build linux

package runner

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// bwrapEnv is the host access bubblewrap provenance checking needs, injectable
// so tests can present invented owners and modes (they cannot create setuid or
// foreign-owned files).
type bwrapEnv struct {
	euid         int
	lookPath     func(string) (string, error)
	evalSymlinks func(string) (string, error)
	lstat        func(string) (fs.FileInfo, error)
}

func defaultBwrapEnv() bwrapEnv {
	return bwrapEnv{euid: os.Geteuid(), lookPath: exec.LookPath, evalSymlinks: filepath.EvalSymlinks, lstat: os.Lstat}
}

// ResolveBwrap selects the bubblewrap to run and validates its provenance. It
// runs every time a sandbox is constructed and is never cached, so a package
// upgrade or a tampered binary is noticed on the next run.
func ResolveBwrap(configured string) (string, error) {
	return defaultBwrapEnv().resolve(configured)
}

// resolve picks the operator's configured path (sandbox.bwrap_path), else
// bwrap on PATH, and returns its resolved path once it passes every check. bwrap
// runs before the security boundary, so its selection is pinned: the path is
// absolute and resolves to a regular file; the file is not setuid or setgid
// (setuid bubblewrap is the mode behind GHSA-xq78-7hw4-5jvp, and it prevents
// the user-namespace nesting the agents' own sandboxes need); the file and
// every ancestor directory up to / are owned by root or the effective uid and
// are neither group- nor other-writable (sticky world-writable directories
// such as /tmp are rejected too). The candidate is inspected, never executed.
func (e bwrapEnv) resolve(configured string) (string, error) {
	candidate, source := configured, "sandbox.bwrap_path"
	if candidate == "" {
		found, err := e.lookPath("bwrap")
		if err != nil {
			return "", fmt.Errorf("sandbox enabled but bwrap (bubblewrap) not found on PATH; install it, "+
				"set sandbox.bwrap_path, or pass --no-sandbox: %w", err)
		}
		candidate, source = found, "bwrap on PATH"
	}
	if !filepath.IsAbs(candidate) {
		return "", fmt.Errorf("%s %q: bubblewrap must be an absolute path", source, candidate)
	}
	resolved, err := e.evalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("%s %q: %w", source, candidate, err)
	}
	info, err := e.lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("%s %q: %w", source, candidate, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s %q: %s is not a regular file", source, candidate, resolved)
	}
	if info.Mode()&fs.ModeSetuid != 0 || info.Mode()&fs.ModeSetgid != 0 {
		kind := "setuid"
		if info.Mode()&fs.ModeSetuid == 0 {
			kind = "setgid"
		}
		return "", fmt.Errorf("%s %q: %s is %s; a %s bubblewrap is refused (GHSA-xq78-7hw4-5jvp) and cannot nest "+
			"the agents' own sandboxes", source, candidate, resolved, kind, kind)
	}
	if err := e.checkTrusted(resolved, info); err != nil {
		return "", fmt.Errorf("%s %q: %w", source, candidate, err)
	}
	for dir := filepath.Dir(resolved); ; dir = filepath.Dir(dir) {
		dirInfo, err := e.lstat(dir)
		if err != nil {
			return "", fmt.Errorf("%s %q: %w", source, candidate, err)
		}
		if err := e.checkTrusted(dir, dirInfo); err != nil {
			return "", fmt.Errorf("%s %q: %w", source, candidate, err)
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	return resolved, nil
}

// checkTrusted requires that path is owned by root or the effective uid and is
// not group- or other-writable: only a principal the operator already trusts
// could have put it there.
func (e bwrapEnv) checkTrusted(path string, info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine the owner of %s", path)
	}
	if st.Uid != 0 && int(st.Uid) != e.euid {
		return fmt.Errorf("%s is owned by uid %d; want root or you (uid %d)", path, st.Uid, e.euid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is group- or other-writable (mode %o)", path, info.Mode().Perm())
	}
	return nil
}
