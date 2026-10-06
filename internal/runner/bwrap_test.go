// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

//go:build linux

package runner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeInfo is a fs.FileInfo with a chosen mode and owner, so provenance checks
// can be exercised without creating setuid or foreign-owned files.
type fakeInfo struct {
	name string
	mode fs.FileMode
	uid  uint32
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return &syscall.Stat_t{Uid: f.uid} }

const fakeEUID = 1000

// fakeTree is a bwrapEnv over an invented file tree: every path maps to its
// info, symlinks are not modeled (EvalSymlinks is the identity), and a path
// absent from the map does not exist.
func fakeTree(tree map[string]fakeInfo) bwrapEnv {
	return bwrapEnv{
		euid:         fakeEUID,
		lookPath:     func(string) (string, error) { return "", errors.New("not on PATH") },
		evalSymlinks: func(p string) (string, error) { return p, nil },
		lstat: func(p string) (fs.FileInfo, error) {
			if info, ok := tree[p]; ok {
				return info, nil
			}
			return nil, fs.ErrNotExist
		},
	}
}

// goodTree is the accepted shape: a 0755 operator-owned binary under 0755
// root-owned directories.
func goodTree() map[string]fakeInfo {
	return map[string]fakeInfo{
		"/":                  {"/", fs.ModeDir | 0o755, 0},
		"/opt":               {"opt", fs.ModeDir | 0o755, 0},
		"/opt/evolve":        {"evolve", fs.ModeDir | 0o755, fakeEUID},
		"/opt/evolve/bwrap":  {"bwrap", 0o755, fakeEUID},
		"/usr":               {"usr", fs.ModeDir | 0o755, 0},
		"/usr/bin":           {"bin", fs.ModeDir | 0o755, 0},
		"/usr/bin/bwrap":     {"bwrap", 0o755, 0},
		"/home":              {"home", fs.ModeDir | 0o755, 0},
		"/home/u":            {"u", fs.ModeDir | 0o755, fakeEUID},
		"/home/u/bin":        {"bin", fs.ModeDir | 0o755, fakeEUID},
		"/home/u/bin/bwrap2": {"bwrap2", 0o755, fakeEUID},
	}
}

func TestBwrapAcceptsOperatorOwnedCopy(t *testing.T) {
	env := fakeTree(goodTree())
	for _, path := range []string{"/opt/evolve/bwrap", "/usr/bin/bwrap", "/home/u/bin/bwrap2"} {
		got, err := env.resolve(path)
		if err != nil || got != path {
			t.Errorf("resolve(%q) = %q, %v; want accepted", path, got, err)
		}
	}
}

func TestBwrapResolvesFromPath(t *testing.T) {
	env := fakeTree(goodTree())
	env.lookPath = func(name string) (string, error) {
		if name != "bwrap" {
			t.Errorf("lookPath(%q), want bwrap", name)
		}
		return "/usr/bin/bwrap", nil
	}
	if got, err := env.resolve(""); err != nil || got != "/usr/bin/bwrap" {
		t.Errorf("resolve(\"\") = %q, %v; want the PATH hit", got, err)
	}
	env.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if _, err := env.resolve(""); err == nil || !strings.Contains(err.Error(), "bwrap") {
		t.Errorf("resolve with no bwrap on PATH = %v, want an error naming bwrap", err)
	}
}

func TestBwrapRejectsRelativePath(t *testing.T) {
	env := fakeTree(goodTree())
	if _, err := env.resolve("bin/bwrap"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("relative configured path = %v, want an absolute-path rejection", err)
	}
	env.lookPath = func(string) (string, error) { return "./bwrap", nil }
	if _, err := env.resolve(""); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("relative PATH hit = %v, want an absolute-path rejection", err)
	}
}

func TestBwrapRejectsNonRegularFile(t *testing.T) {
	tree := goodTree()
	tree["/opt/evolve/bwrap"] = fakeInfo{"bwrap", fs.ModeDir | 0o755, fakeEUID}
	if _, err := fakeTree(tree).resolve("/opt/evolve/bwrap"); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Errorf("directory candidate = %v, want a regular-file rejection", err)
	}
}

func TestBwrapRejectsGroupWritableFile(t *testing.T) {
	for name, mode := range map[string]fs.FileMode{"group": 0o775, "other": 0o757} {
		tree := goodTree()
		tree["/opt/evolve/bwrap"] = fakeInfo{"bwrap", mode, fakeEUID}
		if _, err := fakeTree(tree).resolve("/opt/evolve/bwrap"); err == nil || !strings.Contains(err.Error(), "writable") {
			t.Errorf("%s-writable file = %v, want a writable rejection", name, err)
		}
	}
}

func TestBwrapRejectsOtherWritableAncestor(t *testing.T) {
	tree := goodTree()
	tree["/opt"] = fakeInfo{"opt", fs.ModeDir | 0o777, 0}
	if _, err := fakeTree(tree).resolve("/opt/evolve/bwrap"); err == nil || !strings.Contains(err.Error(), "/opt") {
		t.Errorf("world-writable ancestor = %v, want a rejection naming /opt", err)
	}
	tree = goodTree()
	tree["/opt/evolve"] = fakeInfo{"evolve", fs.ModeDir | 0o775, fakeEUID}
	if _, err := fakeTree(tree).resolve("/opt/evolve/bwrap"); err == nil || !strings.Contains(err.Error(), "/opt/evolve") {
		t.Errorf("group-writable ancestor = %v, want a rejection naming /opt/evolve", err)
	}
}

func TestBwrapRejectsStickyTmpAncestor(t *testing.T) {
	tree := goodTree()
	tree["/opt"] = fakeInfo{"opt", fs.ModeDir | fs.ModeSticky | 0o777, 0}
	if _, err := fakeTree(tree).resolve("/opt/evolve/bwrap"); err == nil || !strings.Contains(err.Error(), "/opt") {
		t.Errorf("sticky world-writable ancestor (like /tmp) = %v, want a rejection", err)
	}
}

func TestBwrapRejectsSetuid(t *testing.T) {
	tree := goodTree()
	tree["/usr/bin/bwrap"] = fakeInfo{"bwrap", fs.ModeSetuid | 0o755, 0}
	_, err := fakeTree(tree).resolve("/usr/bin/bwrap")
	if err == nil || !strings.Contains(err.Error(), "setuid") {
		t.Errorf("setuid candidate = %v, want a setuid rejection", err)
	}
	tree["/usr/bin/bwrap"] = fakeInfo{"bwrap", fs.ModeSetgid | 0o755, 0}
	if _, err := fakeTree(tree).resolve("/usr/bin/bwrap"); err == nil || !strings.Contains(err.Error(), "setgid") {
		t.Errorf("setgid candidate = %v, want a setgid rejection", err)
	}
}

func TestBwrapRejectsForeignOwner(t *testing.T) {
	tree := goodTree()
	tree["/opt/evolve/bwrap"] = fakeInfo{"bwrap", 0o755, 4242}
	if _, err := fakeTree(tree).resolve("/opt/evolve/bwrap"); err == nil || !strings.Contains(err.Error(), "owned") {
		t.Errorf("foreign-owned file = %v, want an ownership rejection", err)
	}
	tree = goodTree()
	tree["/opt/evolve"] = fakeInfo{"evolve", fs.ModeDir | 0o755, 4242}
	if _, err := fakeTree(tree).resolve("/opt/evolve/bwrap"); err == nil || !strings.Contains(err.Error(), "/opt/evolve") {
		t.Errorf("foreign-owned ancestor = %v, want a rejection naming /opt/evolve", err)
	}
}

// TestBwrapNeverExecutesCandidate pins that validation only inspects the
// candidate: a script that would create a marker file never runs.
func TestBwrapNeverExecutesCandidate(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	script := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _ = defaultBwrapEnv().resolve(script) // accepted or rejected: either way, never run
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("validation executed the candidate (marker stat err = %v)", err)
	}
}

// TestBwrapAcceptsSystemBinary validates the real bubblewrap on PATH: the live
// sandbox tests depend on it, so a host without a non-setuid, root- or
// operator-owned bwrap fails here with the reason.
func TestBwrapAcceptsSystemBinary(t *testing.T) {
	got, err := ResolveBwrap("")
	if err != nil {
		t.Fatalf("the bubblewrap on PATH failed validation (install bubblewrap, non-setuid, in a root-owned directory): %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("resolved path %q is not absolute", got)
	}
}
