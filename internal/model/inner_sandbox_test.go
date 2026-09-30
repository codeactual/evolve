// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package model

import (
	"strings"
	"testing"
)

func TestInnerSandboxRejectsWildcardDomains(t *testing.T) {
	for _, bad := range []string{"*", "*.com", "*.io", "", "https://example.com", "example.com:443", "exa*mple.com", "*.*.com"} {
		err := InnerSandbox{ClaudeAllowedDomains: []string{"good.example.com", bad}}.Validate()
		if err == nil {
			t.Errorf("Validate accepted %q, want a rejection", bad)
			continue
		}
		if !strings.Contains(err.Error(), "sandbox.claude_allowed_domains") {
			t.Errorf("error for %q = %v, want it to name the config key", bad, err)
		}
	}
	for _, bad := range []string{"*", "*.com"} {
		err := InnerSandbox{ClaudeAllowedDomains: []string{bad}}.Validate()
		if err == nil || !strings.Contains(err.Error(), "srt") {
			t.Errorf("error for %q = %v, want the srt-specific explanation", bad, err)
		}
	}
}

func TestInnerSandboxAcceptsHostsAndSubdomainWildcards(t *testing.T) {
	in := InnerSandbox{ClaudeAllowedDomains: []string{"example.com", "*.example.com", "registry.npmjs.org", "proxy.golang.org"}}
	if err := in.Validate(); err != nil {
		t.Errorf("Validate = %v, want accepted", err)
	}
	if err := (InnerSandbox{}).Validate(); err != nil {
		t.Errorf("zero InnerSandbox Validate = %v, want accepted", err)
	}
}
