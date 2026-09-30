// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package runner

import (
	"slices"
	"strings"
	"testing"
)

// envMap indexes KEY=value entries by key (the last entry wins, as in exec).
func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestEnvDropsUnlisted(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin", "GITHUB_TOKEN=ghp_secret", "AWS_SECRET_ACCESS_KEY=aws_secret",
		"EVOLVE_ANTHROPIC_API_KEY=counting_key", "ANTHROPIC_API_KEY=would_leak", "DATABASE_URL=postgres://x",
	}
	got := envMap(buildEnv(parent, nil, false, nil, false))
	for _, leaked := range []string{"GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "EVOLVE_ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY", "DATABASE_URL"} {
		if v, ok := got[leaked]; ok {
			t.Errorf("%s=%q reached the child, want it dropped", leaked, v)
		}
	}
	if got["PATH"] != "/usr/bin" {
		t.Errorf("PATH = %q, want the passthrough value", got["PATH"])
	}
}

func TestEnvKeepsBaseline(t *testing.T) {
	want := map[string]string{
		"PATH": "/usr/bin", "HOME": "/home/u", "XDG_CONFIG_HOME": "/home/u/.config",
		"LANG": "C.UTF-8", "LC_ALL": "C", "LC_CTYPE": "en_US.UTF-8", "LANGUAGE": "en", "TERM": "xterm", "COLORTERM": "truecolor",
		"NO_COLOR": "1", "TZ": "UTC", "USER": "u", "LOGNAME": "u", "SHELL": "/bin/bash",
		"HTTP_PROXY": "http://p:1", "HTTPS_PROXY": "http://p:2", "NO_PROXY": "localhost",
		"http_proxy": "http://p:3", "https_proxy": "http://p:4", "no_proxy": "localhost",
		"SSL_CERT_FILE": "/etc/ssl/cert.pem", "SSL_CERT_DIR": "/etc/ssl/certs",
	}
	var parent []string
	for k, v := range want {
		parent = append(parent, k+"="+v)
	}
	got := envMap(buildEnv(parent, nil, false, nil, false))
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want the passthrough value %q", k, got[k], v)
		}
	}
}

func TestEnvOperatorPassthrough(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "GOPATH=/home/u/go", "NPM_TOKEN=secret", "GOFLAGS=-mod=vendor"}
	got := envMap(buildEnv(parent, nil, false, []string{"GOPATH", "GOFLAGS"}, false))
	if got["GOPATH"] != "/home/u/go" || got["GOFLAGS"] != "-mod=vendor" {
		t.Errorf("operator-listed names = %v, want GOPATH and GOFLAGS passed through", got)
	}
	if _, ok := got["NPM_TOKEN"]; ok {
		t.Error("NPM_TOKEN was not listed but reached the child")
	}
}

func TestEnvInheritForProbes(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "CLAUDE_CONFIG_DIR=/home/u/.claude", "ARBITRARY_VAR=1"}
	got := envMap(buildEnv(parent, []string{"DISABLE_AUTOUPDATER=1"}, true, nil, false))
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "ARBITRARY_VAR", "DISABLE_AUTOUPDATER"} {
		if _, ok := got[k]; !ok {
			t.Errorf("InheritEnv dropped %s, want the full environment plus the spec extras", k)
		}
	}
}

func TestEnvSandboxedTmpdir(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "TMPDIR=/var/folders/host/tmp"}
	got := envMap(buildEnv(parent, nil, false, nil, true))
	if got["TMPDIR"] != "/tmp" {
		t.Errorf("sandboxed TMPDIR = %q, want /tmp (the host path is not mounted)", got["TMPDIR"])
	}
	if !strings.HasPrefix(got["PATH"], sandboxBinDir+":") || !strings.HasSuffix(got["PATH"], ":/usr/bin") {
		t.Errorf("sandboxed PATH = %q, want the validated bubblewrap directory prefixed to /usr/bin", got["PATH"])
	}
}

func TestEnvUnsandboxedTmpdir(t *testing.T) {
	got := envMap(buildEnv([]string{"PATH=/usr/bin", "TMPDIR=/var/tmp/host"}, nil, false, nil, false))
	if got["TMPDIR"] != "/var/tmp/host" {
		t.Errorf("unsandboxed TMPDIR = %q, want the parent's", got["TMPDIR"])
	}
	if got["PATH"] != "/usr/bin" {
		t.Errorf("unsandboxed PATH = %q, want it unchanged", got["PATH"])
	}
}

func TestEnvSpecEnvWinsAndComesLast(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "HOME=/home/u"}
	env := buildEnv(parent, []string{"HOME=/elsewhere", "CLAUDE_CONFIG_DIR=/ws/.evolve/claude-home"}, false, nil, false)
	if got := envMap(env); got["HOME"] != "/elsewhere" || got["CLAUDE_CONFIG_DIR"] != "/ws/.evolve/claude-home" {
		t.Errorf("env = %v, want the spec's entries to win", got)
	}
	if !slices.Equal(env[len(env)-2:], []string{"HOME=/elsewhere", "CLAUDE_CONFIG_DIR=/ws/.evolve/claude-home"}) {
		t.Errorf("env tail = %v, want the spec extras last", env[len(env)-2:])
	}
}
