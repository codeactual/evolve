// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package harness

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/codeactual/evolve/internal/model"
)

// Claude Code emits one JSON event per line under --output-format stream-json
// --verbose: assistant events carry tool_use content blocks, and a terminal
// type:"result" event carries the final answer, usage, cost, and error
// envelope. These fixtures mirror that shape (the same one ScanLine parses).
const (
	claudeStreamSuccess = `{"type":"system","subtype":"init","session_id":"s1"}
{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Creating the file."}]}}
{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":"t1","name":"Write","input":{"file_path":"foo.txt","content":"hello"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}
{"type":"assistant","message":{"id":"m3","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"terraform plan"}}]}}
{"type":"result","subtype":"success","is_error":false,"result":"Done.","total_cost_usd":0.0123,"usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":50,"output_tokens":30}}`

	claudeStreamNoTools = `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"hi","usage":{"input_tokens":5,"output_tokens":2}}`

	claudeStreamMaxTurns = `{"type":"system","subtype":"init"}
{"type":"result","subtype":"error_max_turns","is_error":true,"result":"","errors":["hit max turns"]}`

	// A usage-limit rejection: the CLI reports it as a success-shaped result
	// (exit 0, zero output tokens) after a rate_limit_event flips to rejected.
	claudeStreamRateLimited = `{"type":"system","subtype":"init"}
{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1755772800,"rateLimitType":"five_hour","overageStatus":"rejected"}}
{"type":"result","subtype":"success","is_error":false,"result":"Claude AI usage limit reached|1755772800","usage":{"input_tokens":10,"output_tokens":0}}`

	// The banner-only shape: no rate_limit_event, the limit message as result.
	claudeStreamLimitBanner = `{"type":"system","subtype":"init"}
{"type":"result","subtype":"success","is_error":false,"result":"Claude AI usage limit reached|1755772800"}`

	// A limit event that stayed allowed alongside a real answer — gradable.
	claudeStreamLimitAllowed = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1755772800,"rateLimitType":"five_hour","overageStatus":"rejected"}}
{"type":"result","subtype":"success","is_error":false,"result":"Done.","usage":{"input_tokens":100,"output_tokens":30}}`

	// The limit was hit mid-run but the session still produced real output
	// (non-zero output tokens, substantive result) — stays gradable.
	claudeStreamLimitMidRun = `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1755772800,"rateLimitType":"five_hour"}}
{"type":"result","subtype":"success","is_error":false,"result":"Created the file and ran the tests.","usage":{"input_tokens":100,"output_tokens":250}}`
)

func containsPair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

// TestClaudeEvalSpec locks in the bypass-permissions eval posture: no tool
// allowlist, and always-on sandbox settings regardless of how evolve's own
// sandbox is configured.
func TestClaudeEvalSpec(t *testing.T) {
	c := NewClaude()
	ws := t.TempDir()
	spec := c.EvalSpec(ws, model.EvalInput{Prompt: "fix it"}, "opus")
	if !containsPair(spec.Argv, "--permission-mode", "bypassPermissions") {
		t.Errorf("want --permission-mode bypassPermissions: %v", spec.Argv)
	}
	if slices.Contains(spec.Argv, "--allowedTools") {
		t.Errorf("want no --allowedTools on evals: %v", spec.Argv)
	}
	if !containsPair(spec.Argv, "--max-turns", "20") {
		t.Errorf("want default max-turns 20: %v", spec.Argv)
	}
	if !slices.Contains(spec.Argv, "--settings") {
		t.Errorf("want always-on sandbox settings: %v", spec.Argv)
	}

	spec = c.EvalSpec(ws, model.EvalInput{Prompt: "x", MaxTurns: 5}, "opus")
	if !containsPair(spec.Argv, "--max-turns", "5") {
		t.Errorf("want max-turns 5: %v", spec.Argv)
	}
}

// claudeSettingsOf decodes the --settings JSON of an argv into its sandbox
// block, failing the test when it is absent or malformed.
func claudeSettingsOf(t *testing.T, argv []string) (sandbox struct {
	Enabled                  bool `json:"enabled"`
	FailIfUnavailable        bool `json:"failIfUnavailable"`
	AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
	Network                  struct {
		AllowedDomains  []string `json:"allowedDomains"`
		StrictAllowlist bool     `json:"strictAllowlist"`
	} `json:"network"`
},
) {
	t.Helper()
	i := slices.Index(argv, "--settings")
	if i < 0 || i+1 >= len(argv) {
		t.Fatalf("argv has no --settings: %v", argv)
	}
	var settings struct {
		Sandbox json.RawMessage `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(argv[i+1]), &settings); err != nil {
		t.Fatalf("--settings is not JSON: %v\n%s", err, argv[i+1])
	}
	if !strings.Contains(argv[i+1], `"allowedDomains":[`) {
		t.Errorf("allowedDomains must serialize as an array, never null: %s", argv[i+1])
	}
	if err := json.Unmarshal(settings.Sandbox, &sandbox); err != nil {
		t.Fatal(err)
	}
	return sandbox
}

func TestClaudeTriggerSpecSandboxSettings(t *testing.T) {
	spec := NewClaude().TriggerSpec(t.TempDir(), "q", "opus", model.InnerSandbox{})
	sb := claudeSettingsOf(t, spec.Argv)
	if !sb.Enabled || !sb.FailIfUnavailable || sb.AllowUnsandboxedCommands || !sb.Network.StrictAllowlist {
		t.Errorf("sandbox settings = %+v, want enabled, failIfUnavailable, no unsandboxed commands, strictAllowlist", sb)
	}
	if len(sb.Network.AllowedDomains) != 0 {
		t.Errorf("allowedDomains = %v, want none by default", sb.Network.AllowedDomains)
	}
	// The trigger posture is unchanged around the settings.
	if !containsPair(spec.Argv, "--allowedTools", "Skill Read") {
		t.Errorf("trigger runs keep their Skill/Read allowlist: %v", spec.Argv)
	}
}

func TestClaudeEvalSpecSandboxSettings(t *testing.T) {
	inner := model.InnerSandbox{ClaudeAllowedDomains: []string{"proxy.golang.org", "*.npmjs.org"}}
	spec := NewClaude().EvalSpec(t.TempDir(), model.EvalInput{Prompt: "p", InnerSandbox: inner}, "opus")
	sb := claudeSettingsOf(t, spec.Argv)
	if !sb.Enabled || !sb.FailIfUnavailable || sb.AllowUnsandboxedCommands {
		t.Errorf("sandbox settings = %+v, want the fail-closed always-on settings", sb)
	}
	if !slices.Equal(sb.Network.AllowedDomains, inner.ClaudeAllowedDomains) {
		t.Errorf("allowedDomains = %v, want %v verbatim", sb.Network.AllowedDomains, inner.ClaudeAllowedDomains)
	}
	// The eval keeps its prompts-off posture.
	if !containsPair(spec.Argv, "--permission-mode", "bypassPermissions") {
		t.Errorf("evals keep bypassPermissions: %v", spec.Argv)
	}
}

func TestClaudeParseToolCalls(t *testing.T) {
	c := NewClaude()
	calls := c.ParseToolCalls([]byte(claudeStreamSuccess))
	if len(calls) != 2 {
		t.Fatalf("ParseToolCalls = %d calls, want 2: %+v", len(calls), calls)
	}
	if calls[0].Name != "Write" || !strings.Contains(string(calls[0].Input), `"foo.txt"`) {
		t.Errorf("call[0] = %+v, want Write with foo.txt", calls[0])
	}
	if calls[1].Name != "Bash" || !strings.Contains(string(calls[1].Input), "terraform plan") {
		t.Errorf("call[1] = %+v, want Bash with terraform plan", calls[1])
	}

	// A run with no tool_use blocks reports nil; the engine normalizes that to a
	// non-nil empty slice so a tool_call assertion fails rather than skips.
	if got := c.ParseToolCalls([]byte(claudeStreamNoTools)); got != nil {
		t.Errorf("ParseToolCalls(no tools) = %+v, want nil", got)
	}
	if got := c.ParseToolCalls([]byte("not json\n")); got != nil {
		t.Errorf("ParseToolCalls(garbage) = %+v, want nil", got)
	}
}

func TestClaudeParseEvalOutput(t *testing.T) {
	c := NewClaude()
	text, usage := c.ParseEvalOutput([]byte(claudeStreamSuccess))
	if text != "Done." {
		t.Errorf("text = %q, want %q", text, "Done.")
	}
	if usage == nil {
		t.Fatal("usage = nil, want populated")
	}
	// Fresh input, cache read, and cache write stay on their own fields.
	if got := derefInt(usage.InputTokens); got != 100 {
		t.Errorf("InputTokens = %d, want 100", got)
	}
	if got := derefInt(usage.CacheReadTokens); got != 50 {
		t.Errorf("CacheReadTokens = %d, want 50", got)
	}
	if got := derefInt(usage.CacheCreationTokens); got != 20 {
		t.Errorf("CacheCreationTokens = %d, want 20", got)
	}
	if got := derefInt(usage.OutputTokens); got != 30 {
		t.Errorf("OutputTokens = %d, want 30", got)
	}
	if usage.CostUSD == nil || *usage.CostUSD != 0.0123 {
		t.Errorf("CostUSD = %v, want 0.0123", usage.CostUSD)
	}

	// No result event: fall back to raw stdout with nil usage.
	raw := "plain text answer\n"
	if text, usage := c.ParseEvalOutput([]byte(raw)); text != raw || usage != nil {
		t.Errorf("ParseEvalOutput(plain) = (%q, %v), want (%q, nil)", text, usage, raw)
	}
}

func TestClaudeRuntimeError(t *testing.T) {
	c := NewClaude()
	resets := time.Unix(1755772800, 0).UTC().Format(time.RFC3339)
	tests := []struct {
		name     string
		stdout   string
		exitCode int
		want     string
	}{
		{"gradable result", claudeStreamSuccess, 0, ""},
		{"empty", "", 1, "empty CLI output"},
		{"plain text clean exit", "hello\n", 0, ""},
		{"plain text crash", "boom\n", 1, "unparseable CLI output"},
		{"max turns empty result", claudeStreamMaxTurns, 1, "claude run error (error_max_turns): hit max turns"},
		{
			"rejected event, zero output tokens", claudeStreamRateLimited, 0,
			"usage limit reached (five_hour), resets " + resets,
		},
		{
			"limit banner without event", claudeStreamLimitBanner, 0,
			"usage limit reached: Claude AI usage limit reached|1755772800",
		},
		{"allowed event with real result", claudeStreamLimitAllowed, 0, ""},
		{"rejected mid-run with real output", claudeStreamLimitMidRun, 0, ""},
	}
	for _, tt := range tests {
		if got := c.RuntimeError([]byte(tt.stdout), tt.exitCode, false); got != tt.want {
			t.Errorf("%s: RuntimeError = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestClaudeScanLine(t *testing.T) {
	c := NewClaude()
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"skill tool_use", `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Skill","input":{"command":"my-skill"}}]}}`, true},
		{"read skill.md", `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"skills/my-skill/SKILL.md"}}]}}`, true},
		{"unrelated tool", `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"x"}}]}}`, false},
		{"garbage", "not json", false},
	}
	for _, tt := range tests {
		if hit, _ := c.ScanLine([]byte(tt.line), "my-skill", ""); hit != tt.want {
			t.Errorf("%s: ScanLine = %v, want %v", tt.name, hit, tt.want)
		}
	}
}

func derefInt(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

// TestClaudeProbeSpecIgnoresProjectSettingsAndMCP pins that the offered-models
// probe never loads project settings (hooks) or MCP servers: a headless
// `claude -p` would otherwise run a repository's hooks and connect its
// .mcp.json servers without a trust prompt.
func TestClaudeProbeSpecIgnoresProjectSettingsAndMCP(t *testing.T) {
	for _, alias := range []string{"", "sonnet"} {
		spec := claudeProbeSpec(alias)
		if !containsPair(spec.Argv, "--setting-sources", "user") {
			t.Errorf("claudeProbeSpec(%q) lacks --setting-sources user: %v", alias, spec.Argv)
		}
		if !slices.Contains(spec.Argv, "--strict-mcp-config") {
			t.Errorf("claudeProbeSpec(%q) lacks --strict-mcp-config: %v", alias, spec.Argv)
		}
	}
}

// TestClaudeEnvCredentials pins the agent process's credential environment: only
// the variables the claude CLI itself reads are forwarded, and only when set in
// the parent. It also pins that CLAUDE_CODE_SUBPROCESS_ENV_SCRUB stays unset:
// claude 2.1.285 forces the permission mode to default when it is set, which
// would end the prompts-off eval posture (see claudeEnv).
func TestClaudeEnvCredentials(t *testing.T) {
	for _, k := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "EVOLVE_ANTHROPIC_API_KEY", "GITHUB_TOKEN"} {
		t.Setenv(k, "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	env, _ := claudeEnv(t.TempDir())
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=") {
			t.Errorf("env sets %q: it forces Claude's permission mode to default and ends the prompts-off posture", e)
		}
		for _, unwanted := range []string{"ANTHROPIC_API_KEY=", "CLAUDE_CODE_OAUTH_TOKEN=", "ANTHROPIC_AUTH_TOKEN=", "GITHUB_TOKEN=", "EVOLVE_"} {
			if strings.HasPrefix(e, unwanted) {
				t.Errorf("unset credential leaked into env as %q", e)
			}
		}
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("EVOLVE_ANTHROPIC_API_KEY", "counting-only")
	t.Setenv("GITHUB_TOKEN", "ghp_unrelated")
	env, _ = claudeEnv(t.TempDir())
	if !slices.Contains(env, "ANTHROPIC_API_KEY=sk-ant-test") {
		t.Errorf("a set ANTHROPIC_API_KEY must be forwarded to the CLI: %v", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "EVOLVE_") || strings.HasPrefix(e, "GITHUB_TOKEN=") {
			t.Errorf("env forwards %q, which the claude CLI does not read", e)
		}
	}
}

func TestClaudeJudgeSpec(t *testing.T) {
	judgeDir, ws := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	schema := `{"type":"object"}`
	spec := NewClaude().JudgeSpec(judgeDir, model.JudgeInput{
		Prompt: "grade it", MaxTurns: 16, Workspace: ws, Schema: schema,
	}, "sonnet")

	want := []string{
		"claude", "-p", "grade it", "--model", "sonnet",
		"--output-format", "stream-json", "--verbose", "--max-turns", "16",
		"--restricted", "--safe-mode", "--strict-mcp-config",
		"--tools", "Read,Grep,Glob",
		"--permission-mode", "dontAsk",
		"--add-dir", ws,
		"--json-schema", schema,
	}
	if !slices.Equal(spec.Argv, want) {
		t.Errorf("judge argv =\n%v\nwant\n%v", spec.Argv, want)
	}
	for _, banned := range []string{"bypassPermissions", "--allowedTools", "--settings"} {
		if slices.Contains(spec.Argv, banned) {
			t.Errorf("judge argv must not carry %s: %v", banned, spec.Argv)
		}
	}
	if spec.Dir != judgeDir {
		t.Errorf("Dir = %q, want the judge directory %q, never the workspace", spec.Dir, judgeDir)
	}
	if !slices.Contains(spec.ReadPaths, ws) {
		t.Errorf("ReadPaths = %v, want the workspace (read-only view)", spec.ReadPaths)
	}
	cfg := "CLAUDE_CONFIG_DIR=" + isolatedDir(judgeDir, claudeConfigRel)
	if !slices.Contains(spec.Env, cfg) {
		t.Errorf("env lacks %s: %v", cfg, spec.Env)
	}
	for _, e := range spec.Env {
		if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") && strings.Contains(e, ws) {
			t.Errorf("the judge's config dir %q is inside the agent-writable workspace", e)
		}
	}
}

func TestClaudeParseJudgeOutputStructured(t *testing.T) {
	const payload = `{"verdicts":[{"id":1,"passed":true,"evidence":"e"}]}`
	stream := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Sure, here:"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"some prose","structured_output":` + payload + `}`
	got, err := NewClaude().ParseJudgeOutput([]byte(stream))
	if err != nil || string(got) != payload {
		t.Errorf("ParseJudgeOutput = %q, %v; want the structured_output object %s", got, err, payload)
	}
}

func TestClaudeParseJudgeOutputStrictFallback(t *testing.T) {
	const payload = `{"verdicts":[{"id":1,"passed":true,"evidence":"e"}]}`
	result := func(text string) []byte {
		b, _ := json.Marshal(text)
		return []byte(`{"type":"result","subtype":"success","is_error":false,"result":` + string(b) + `}`)
	}
	if got, err := NewClaude().ParseJudgeOutput(result("\n" + payload + "\n")); err != nil || string(got) != payload {
		t.Errorf("a whole-JSON result = %q, %v; want it accepted as %s", got, err, payload)
	}
	for name, text := range map[string]string{
		"prose-wrapped JSON": "Sure! " + payload,
		"code fence":         "```json\n" + payload + "\n```",
		"empty result":       "",
	} {
		if got, err := NewClaude().ParseJudgeOutput(result(text)); err == nil {
			t.Errorf("%s: accepted %q, want an error (no substring scan)", name, got)
		}
	}
	if _, err := NewClaude().ParseJudgeOutput([]byte("not a stream")); err == nil {
		t.Error("a stream without a result event must be an error")
	}
}
