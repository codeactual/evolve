// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/codeactual/evolve/internal/model"
)

func TestLinkFilePrefersSymlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.json")
	dst := filepath.Join(dir, "dst.json")
	body := []byte(`{"token":"x"}`)
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatal(err)
	}

	target := linkFile(src, dst)
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf("dst after linkFile: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Log("dst is a copy, not a symlink (acceptable fallback)")
		if target != "" {
			t.Errorf("linkFile reported target %q for a copy, want empty", target)
		}
	} else if want, _ := filepath.EvalSymlinks(src); target != want {
		t.Errorf("linkFile target = %q, want the real source path %q", target, want)
	}
	// A second call finds dst already linked and reports the same target.
	if again := linkFile(src, dst); again != target {
		t.Errorf("repeat linkFile target = %q, want %q", again, target)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("dst body = %q, want %q", got, body)
	}
}

func TestLinkFileNoOps(t *testing.T) {
	dir := t.TempDir()

	// Missing src leaves no dst behind and reports no target.
	if got := linkFile(filepath.Join(dir, "absent"), filepath.Join(dir, "dst")); got != "" {
		t.Errorf("linkFile(missing src) target = %q, want empty", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "dst")); !os.IsNotExist(err) {
		t.Errorf("expected no dst for missing src, err=%v", err)
	}

	// An existing dst is never overwritten.
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "existing")
	mustWriteFile(t, src, []byte("new"), 0o600)
	mustWriteFile(t, dst, []byte("old"), 0o600)
	if got := linkFile(src, dst); got != "" {
		t.Errorf("linkFile(existing regular dst) target = %q, want empty", got)
	}
	if got, _ := os.ReadFile(dst); string(got) != "old" {
		t.Errorf("existing dst overwritten: %q", got)
	}
}

// requireEnv fails unless env carries the exact entry.
func requireEnv(t *testing.T, env []string, entry string) {
	t.Helper()
	if !slices.Contains(env, entry) {
		t.Fatalf("want %q in env, got %v", entry, env)
	}
}

func TestClaudeIsolation(t *testing.T) {
	opDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", opDir)
	cred := []byte(`{"claudeAiOauth":{"accessToken":"x"}}`)
	if err := os.WriteFile(filepath.Join(opDir, ".credentials.json"), cred, 0o600); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	iso := isolatedDir(ws, claudeConfigRel)
	spec := NewClaude().TriggerSpec(ws, "q", "m", false)
	requireEnv(t, spec.Env, "CLAUDE_CONFIG_DIR="+iso)
	eval := NewClaude().EvalSpec(ws, model.EvalInput{Prompt: "p"}, "m")
	requireEnv(t, eval.Env, "CLAUDE_CONFIG_DIR="+iso)
	if iso == opDir {
		t.Fatal("isolated config dir must differ from operator CLAUDE_CONFIG_DIR")
	}

	// Onboarding state seeded so headless -p never stalls on first run.
	state, err := os.ReadFile(filepath.Join(iso, ".claude.json"))
	if err != nil {
		t.Fatalf(".claude.json: %v", err)
	}
	if !strings.Contains(string(state), "hasCompletedOnboarding") {
		t.Errorf(".claude.json = %q", state)
	}

	// Operator OAuth credentials bridged (Claude keeps them beside the config).
	got, err := os.ReadFile(filepath.Join(iso, ".credentials.json"))
	if err != nil {
		t.Fatalf(".credentials.json in isolated dir: %v", err)
	}
	if string(got) != string(cred) {
		t.Errorf(".credentials.json body = %q, want %q", got, cred)
	}
	// The bridged file's real path is reported so a sandboxed run binds it.
	wantCred := filepath.Join(opDir, ".credentials.json")
	if !slices.Equal(spec.ReadPaths, []string{wantCred}) || !slices.Equal(eval.ReadPaths, []string{wantCred}) {
		t.Errorf("ReadPaths = %v / %v, want [%s]", spec.ReadPaths, eval.ReadPaths, wantCred)
	}

	// No operator credentials → nothing bridged (env-key CI).
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	ws2 := t.TempDir()
	_ = NewClaude().TriggerSpec(ws2, "q", "m", false)
	if _, err := os.Lstat(filepath.Join(isolatedDir(ws2, claudeConfigRel), ".credentials.json")); !os.IsNotExist(err) {
		t.Errorf("expected no bridged credentials, err=%v", err)
	}
}

func TestCodexIsolation(t *testing.T) {
	opHome := t.TempDir()
	t.Setenv("CODEX_HOME", opHome)
	auth := []byte(`{"OPENAI_API_KEY":null,"tokens":{}}`)
	mustWriteFile(t, filepath.Join(opHome, "auth.json"), auth, 0o600)
	mustWriteFile(t, filepath.Join(opHome, "config.toml"), []byte(
		"model = \"gpt-5.2\"\ncli_auth_credentials_store = \"keyring\"\n[mcp_servers.github]\ncommand = \"gh-mcp\"\n",
	), 0o644)

	ws := t.TempDir()
	iso := isolatedDir(ws, codexHomeRel)
	spec := NewCodex().TriggerSpec(ws, "q", "m", false)
	requireEnv(t, spec.Env, "CODEX_HOME="+iso)
	eval := NewCodex().EvalSpec(ws, model.EvalInput{Prompt: "p"}, "m")
	requireEnv(t, eval.Env, "CODEX_HOME="+iso)

	got, err := os.ReadFile(filepath.Join(iso, "auth.json"))
	if err != nil {
		t.Fatalf("auth.json in isolated home: %v", err)
	}
	if string(got) != string(auth) {
		t.Errorf("auth.json body = %q, want %q", got, auth)
	}
	wantAuth := filepath.Join(opHome, "auth.json")
	if !slices.Equal(spec.ReadPaths, []string{wantAuth}) || !slices.Equal(eval.ReadPaths, []string{wantAuth}) {
		t.Errorf("ReadPaths = %v / %v, want [%s]", spec.ReadPaths, eval.ReadPaths, wantAuth)
	}

	// Seeded config carries only the credential-store selection — never the
	// operator's MCP servers, profiles, or trust.
	cfg, err := os.ReadFile(filepath.Join(iso, "config.toml"))
	if err != nil {
		t.Fatalf("config.toml in isolated home: %v", err)
	}
	if !strings.Contains(string(cfg), `cli_auth_credentials_store = "keyring"`) {
		t.Errorf("config.toml missing credential store: %q", cfg)
	}
	if strings.Contains(string(cfg), "mcp_servers") || strings.Contains(string(cfg), "gpt-5.2") {
		t.Errorf("config.toml leaked operator config: %q", cfg)
	}

	// No credential-store selection → no config.toml seeded at all.
	op2 := t.TempDir()
	t.Setenv("CODEX_HOME", op2)
	mustWriteFile(t, filepath.Join(op2, "config.toml"), []byte("model = \"gpt-5.2\"\n"), 0o644)
	ws2 := t.TempDir()
	_ = NewCodex().TriggerSpec(ws2, "q", "m", false)
	if _, err := os.Lstat(filepath.Join(isolatedDir(ws2, codexHomeRel), "config.toml")); !os.IsNotExist(err) {
		t.Errorf("expected no seeded config.toml, err=%v", err)
	}
}

// mustWriteFile writes fixture data, failing the test on error.
func mustWriteFile(t *testing.T, path string, data []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatal(err)
	}
}
