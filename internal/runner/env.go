// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package runner

import (
	"strings"
)

// baselineEnv names the parent environment variables every agent process gets
// when set: enough for tools to find their way around and talk to the network,
// and nothing that carries a secret. Everything else in the operator's shell
// (GITHUB_TOKEN, AWS_*, the EVOLVE_* token-counting keys, ...) stays behind; the
// operator adds names with sandbox.env_passthrough, and each harness forwards
// only the credential variables its own CLI reads.
var baselineEnv = map[string]bool{
	"PATH": true, "HOME": true, "XDG_CONFIG_HOME": true,
	"LANG": true, "LANGUAGE": true, "TERM": true, "COLORTERM": true, "NO_COLOR": true, "TZ": true,
	"USER": true, "LOGNAME": true, "SHELL": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
}

// buildEnv assembles a child process's environment from parent (normally
// os.Environ()):
//
//   - Normally only the baseline names and the operator's passthrough names
//     survive, plus the spec's own entries, which come last and win.
//   - With inherit, the whole parent environment survives: for the
//     operator-context probes, which read the operator's real CLI configuration
//     and run no untrusted input.
//   - When sandboxed, TMPDIR is /tmp (the host's TMPDIR path is not mounted) and
//     the validated bubblewrap directory is first on PATH, so the agent CLIs'
//     own nested sandboxes use it (see sandboxBinDir). Unsandboxed, TMPDIR passes
//     through like any other operator variable.
func buildEnv(parent, specEnv []string, inherit bool, passthrough []string, sandboxed bool) []string {
	allowed := make(map[string]bool, len(passthrough)+1)
	for _, name := range passthrough {
		allowed[name] = true
	}
	if !sandboxed {
		allowed["TMPDIR"] = true
	}
	var env []string
	for _, kv := range parent {
		name, _, _ := strings.Cut(kv, "=")
		if inherit || baselineEnv[name] || strings.HasPrefix(name, "LC_") || allowed[name] {
			env = append(env, kv)
		}
	}
	if sandboxed {
		path := lastEnv(parent, "PATH")
		if path == "" { // an empty PATH element means the current directory
			path = "/usr/local/bin:/usr/bin:/bin"
		}
		env = append(env, "TMPDIR=/tmp", "PATH="+sandboxBinDir+":"+path)
	}
	return append(env, specEnv...)
}

// lastEnv returns the value of the last key=value entry for key in env (what
// exec honors when a key repeats), or "" when absent.
func lastEnv(env []string, key string) string {
	value := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			value = v
		}
	}
	return value
}
