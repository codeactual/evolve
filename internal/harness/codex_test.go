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

// codexStream is a real `codex exec --json` capture (aggregated_output trimmed):
// a non-JSON ERROR log line, file_change and command_execution tool items each
// reported at item.started then item.completed, two agent_message items, and the
// terminal turn.completed usage. The mcp_tool_call line is synthetic — codex
// emits that item type for MCP tools but it was not in the capture, so it
// exercises the whole-item fallback rather than a pinned field layout.
const codexStream = `{"type":"thread.started","thread_id":"t1"}
{"type":"turn.started"}
2026-06-23T16:20:48.507788Z ERROR codex_memories_write::phase2: Phase 2 no changes
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"I'll add the file."}}
{"type":"item.started","item":{"id":"item_1","type":"file_change","changes":[{"path":"/repo/foo.txt","kind":"add"}],"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"file_change","changes":[{"path":"/repo/foo.txt","kind":"add"}],"status":"completed"}}
{"type":"item.started","item":{"id":"item_2","type":"command_execution","command":"/opt/homebrew/bin/zsh -lc 'ls -la'","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_2","type":"command_execution","command":"/opt/homebrew/bin/zsh -lc 'ls -la'","aggregated_output":"total 0\nfoo.txt\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_3","type":"mcp_tool_call","server":"github","tool":"create_issue","arguments":{"title":"bug"}}}
{"type":"item.completed","item":{"id":"item_4","type":"agent_message","text":"Created foo.txt and ran ls -la."}}
{"type":"turn.completed","usage":{"input_tokens":61395,"cached_input_tokens":51328,"output_tokens":184,"reasoning_output_tokens":0}}`

func TestCodexParseToolCalls(t *testing.T) {
	c := NewCodex()
	calls := c.ParseToolCalls([]byte(codexStream))
	// file_change, command_execution, mcp_tool_call — agent_message excluded and
	// item.started not double-counted.
	if len(calls) != 3 {
		t.Fatalf("ParseToolCalls = %d calls, want 3: %+v", len(calls), calls)
	}
	if calls[0].Name != "file_change" || !strings.Contains(string(calls[0].Input), "foo.txt") {
		t.Errorf("call[0] = %+v, want file_change touching foo.txt", calls[0])
	}
	// command_execution's args are the invocation, not its output: the command
	// matches but the listing (total 0 / foo.txt) is absent from Input.
	if calls[1].Name != "command_execution" || !strings.Contains(string(calls[1].Input), "ls -la") {
		t.Errorf("call[1] = %+v, want command_execution running ls -la", calls[1])
	}
	if strings.Contains(string(calls[1].Input), "total 0") {
		t.Errorf("call[1].Input includes command output, want command only: %s", calls[1].Input)
	}
	// An unpinned tool item surfaces under its item type with the whole item as
	// arguments (so the MCP tool name and args still match).
	if calls[2].Name != "mcp_tool_call" || !strings.Contains(string(calls[2].Input), "create_issue") {
		t.Errorf("call[2] = %+v, want mcp_tool_call carrying create_issue", calls[2])
	}

	if got := c.ParseToolCalls([]byte("")); got != nil {
		t.Errorf("ParseToolCalls(empty) = %+v, want nil", got)
	}
	if got := c.ParseToolCalls([]byte("not json\n")); got != nil {
		t.Errorf("ParseToolCalls(garbage) = %+v, want nil", got)
	}
}

func TestCodexParseEvalOutput(t *testing.T) {
	c := NewCodex()
	text, usage := c.ParseEvalOutput([]byte(codexStream))
	if want := "I'll add the file.\nCreated foo.txt and ran ls -la."; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if usage == nil {
		t.Fatal("usage = nil, want populated")
	}
	// Codex reports the whole prompt as input with a cached subset; the contract
	// wants fresh input split from cache reads: 61395 - 51328 = 10067 fresh.
	if got := derefInt(usage.InputTokens); got != 10067 {
		t.Errorf("InputTokens = %d, want 10067", got)
	}
	if got := derefInt(usage.CacheReadTokens); got != 51328 {
		t.Errorf("CacheReadTokens = %d, want 51328", got)
	}
	if got := derefInt(usage.OutputTokens); got != 184 {
		t.Errorf("OutputTokens = %d, want 184", got)
	}
}

func TestCodexRuntimeError(t *testing.T) {
	c := NewCodex()
	tests := []struct {
		name     string
		stdout   string
		exitCode int
		want     string
	}{
		{"gradable agent output", codexStream, 0, ""},
		{"empty", "", 1, "empty CLI output"},
		{"no agent output crash", `{"type":"turn.started"}`, 1, "codex produced no agent output"},
	}
	for _, tt := range tests {
		if got := c.RuntimeError([]byte(tt.stdout), tt.exitCode, false); got != tt.want {
			t.Errorf("%s: RuntimeError = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCodexTriggerSpecReadOnly(t *testing.T) {
	spec := NewCodex().TriggerSpec(t.TempDir(), "q", "gpt-5.5", model.InnerSandbox{})
	if !containsPair(spec.Argv, "--sandbox", "read-only") {
		t.Errorf("trigger argv lacks --sandbox read-only: %v", spec.Argv)
	}
	if slices.Contains(spec.Argv, "danger-full-access") {
		t.Errorf("trigger argv must never disable Codex's sandbox: %v", spec.Argv)
	}
}

func TestCodexEvalSpecNetworkOffByDefault(t *testing.T) {
	spec := NewCodex().EvalSpec(t.TempDir(), model.EvalInput{Prompt: "p"}, "gpt-5.5")
	if !containsPair(spec.Argv, "--sandbox", "workspace-write") {
		t.Errorf("eval argv lacks --sandbox workspace-write: %v", spec.Argv)
	}
	if !containsPair(spec.Argv, "-c", "sandbox_workspace_write.network_access=false") {
		t.Errorf("eval argv lacks network_access=false by default: %v", spec.Argv)
	}
	if slices.Contains(spec.Argv, "danger-full-access") {
		t.Errorf("eval argv must never disable Codex's sandbox: %v", spec.Argv)
	}
}

func TestCodexEvalSpecNetworkAccessOptIn(t *testing.T) {
	in := model.EvalInput{Prompt: "p", InnerSandbox: model.InnerSandbox{CodexNetworkAccess: true}}
	spec := NewCodex().EvalSpec(t.TempDir(), in, "gpt-5.5")
	if !containsPair(spec.Argv, "-c", "sandbox_workspace_write.network_access=true") {
		t.Errorf("eval argv lacks network_access=true after the opt-in: %v", spec.Argv)
	}
}

// TestCodexEnvCredentials pins that only the credential variables the codex CLI
// reads are forwarded, and only when set in the parent.
func TestCodexEnvCredentials(t *testing.T) {
	for _, k := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "EVOLVE_OPENAI_API_KEY", "GITHUB_TOKEN"} {
		t.Setenv(k, "")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	env, _ := codexEnv(t.TempDir())
	for _, e := range env {
		for _, unwanted := range []string{"OPENAI_API_KEY=", "CODEX_API_KEY=", "GITHUB_TOKEN=", "EVOLVE_"} {
			if strings.HasPrefix(e, unwanted) {
				t.Errorf("unset credential leaked into env as %q", e)
			}
		}
	}

	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("CODEX_API_KEY", "codex-test")
	t.Setenv("EVOLVE_OPENAI_API_KEY", "counting-only")
	t.Setenv("GITHUB_TOKEN", "ghp_unrelated")
	env, _ = codexEnv(t.TempDir())
	for _, want := range []string{"OPENAI_API_KEY=sk-test", "CODEX_API_KEY=codex-test"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %q: %v", want, env)
		}
	}
	for _, e := range env {
		if strings.HasPrefix(e, "EVOLVE_") || strings.HasPrefix(e, "GITHUB_TOKEN=") {
			t.Errorf("env forwards %q, which the codex CLI does not read", e)
		}
	}
	if !slices.Contains(NewCodex().EnvKeys(), "CODEX_API_KEY") {
		t.Errorf("EnvKeys = %v, want CODEX_API_KEY among the credential variables", NewCodex().EnvKeys())
	}
}

// TestCodexSpecsEnableDefaultExcludes pins that both specs turn Codex's own
// *KEY*/*SECRET*/*TOKEN* shell-environment excludes back on: Codex defaults
// ignore_default_excludes to true, which leaves its shell commands the agent
// process's whole environment, credentials included.
func TestCodexSpecsEnableDefaultExcludes(t *testing.T) {
	const flag = "shell_environment_policy.ignore_default_excludes=false"
	trigger := NewCodex().TriggerSpec(t.TempDir(), "q", "gpt-5.5", model.InnerSandbox{})
	eval := NewCodex().EvalSpec(t.TempDir(), model.EvalInput{Prompt: "p"}, "gpt-5.5")
	for name, argv := range map[string][]string{"trigger": trigger.Argv, "eval": eval.Argv} {
		if !containsPair(argv, "-c", flag) {
			t.Errorf("%s argv lacks -c %s: %v", name, flag, argv)
		}
	}
}

func TestCodexJudgeSpec(t *testing.T) {
	judgeDir, ws := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	schema := `{"type":"object","additionalProperties":false}`
	spec := NewCodex().JudgeSpec(judgeDir, model.JudgeInput{
		Prompt: "grade it", MaxTurns: 16, Workspace: ws, Schema: schema,
	}, "gpt-5.5")

	schemaPath := filepath.Join(judgeDir, "verdicts.schema.json")
	want := []string{
		"codex", "exec", "grade it", "--json", "--skip-git-repo-check",
		"--sandbox", "read-only", "--ignore-rules", "--ephemeral",
		"-m", "gpt-5.5",
		"--output-schema", schemaPath,
		"-c", "shell_environment_policy.ignore_default_excludes=false",
	}
	if !slices.Equal(spec.Argv, want) {
		t.Errorf("judge argv =\n%v\nwant\n%v", spec.Argv, want)
	}
	if spec.Dir != judgeDir {
		t.Errorf("Dir = %q, want the judge directory %q, never the workspace", spec.Dir, judgeDir)
	}
	if !slices.Contains(spec.ReadPaths, ws) {
		t.Errorf("ReadPaths = %v, want the workspace (read-only view)", spec.ReadPaths)
	}
	if got, err := os.ReadFile(schemaPath); err != nil || string(got) != schema {
		t.Errorf("schema file = %q, %v; want the grade schema bytes", got, err)
	}
	if !slices.Contains(spec.Env, "CODEX_HOME="+isolatedDir(judgeDir, codexHomeRel)) {
		t.Errorf("CODEX_HOME must live under the judge directory: %v", spec.Env)
	}
}

func TestCodexParseJudgeOutput(t *testing.T) {
	const payload = `{"verdicts":[{"id":1,"passed":true,"evidence":"e"}]}`
	stream := `{"type":"thread.started","thread_id":"t"}
{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"Let me look at the files."}}
{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":` + "\"" + strings.ReplaceAll(payload, `"`, `\"`) + "\"" + `}}
{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`
	got, err := NewCodex().ParseJudgeOutput([]byte(stream))
	if err != nil || string(got) != payload {
		t.Errorf("ParseJudgeOutput = %q, %v; want the last agent message %s", got, err, payload)
	}
	if _, err := NewCodex().ParseJudgeOutput([]byte(`{"type":"turn.started"}`)); err == nil {
		t.Error("a stream with no agent message must be an error")
	}
}
