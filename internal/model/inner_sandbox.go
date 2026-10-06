// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package model

import (
	"fmt"
	"strings"
)

// InnerSandbox is the operator's configuration of the agent CLIs' own sandboxes,
// which run inside evolve's outer sandbox and are always on: layered, so the
// inner layer adds per-command write and network confinement on top of the
// filesystem policy. Agent shell commands get no network by default — the
// inner sandboxes' native default — until the operator opts in here.
type InnerSandbox struct {
	// ClaudeAllowedDomains are the hosts Claude Code's Bash commands may reach
	// (sandbox.claude_allowed_domains). Empty means no network for commands.
	ClaudeAllowedDomains []string
	// CodexNetworkAccess lets Codex's commands reach the network under its
	// workspace-write sandbox (sandbox.codex_network_access). Off by default.
	CodexNetworkAccess bool
}

// Validate rejects configuration the agent CLIs cannot honor, so a mistake
// fails at load instead of mid-run. Claude Code's sandbox runtime (srt) accepts
// exact hosts ("example.com") and subdomain wildcards under a real domain
// ("*.example.com"); it rejects a bare "*" and a top-level wildcard such as
// "*.com", so "allow all" cannot be expressed.
func (in InnerSandbox) Validate() error {
	for _, d := range in.ClaudeAllowedDomains {
		if err := validateClaudeDomain(d); err != nil {
			return fmt.Errorf("sandbox.claude_allowed_domains entry %q: %w", d, err)
		}
	}
	return nil
}

func validateClaudeDomain(d string) error {
	switch {
	case d == "":
		return fmt.Errorf("empty; list hosts like example.com or *.example.com")
	case d == "*":
		return fmt.Errorf("the Claude Code sandbox runtime (srt) rejects a bare \"*\", so \"allow all\" cannot be " +
			"expressed; list specific hosts like example.com or *.example.com")
	case strings.ContainsAny(d, "/: \t"):
		return fmt.Errorf("want a bare host name like example.com or *.example.com, without a scheme, port, path or spaces")
	}
	if rest, ok := strings.CutPrefix(d, "*."); ok {
		if !strings.Contains(rest, ".") {
			return fmt.Errorf("the Claude Code sandbox runtime (srt) rejects top-level wildcards like %q; "+
				"use *.example.com or list specific hosts", d)
		}
		d = rest
	}
	if strings.Contains(d, "*") {
		return fmt.Errorf("a wildcard is only allowed as a leading \"*.\" label, as in *.example.com")
	}
	return nil
}
