// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package harness

import (
	"slices"
	"strings"
	"testing"

	"github.com/codeactual/evolve/internal/model"
)

func TestClaudeSpecsAreLocalOnly(t *testing.T) {
	c := NewClaude()
	ws := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	judgeless := map[string]model.CommandSpec{
		"trigger": c.TriggerSpec(ws, "q", "m", model.InnerSandbox{}),
		"eval":    c.EvalSpec(ws, model.EvalInput{Prompt: "p"}, "m"),
	}
	for name, spec := range judgeless {
		if !slices.Contains(spec.Argv, "--strict-mcp-config") {
			t.Errorf("%s argv lacks --strict-mcp-config: %v", name, spec.Argv)
		}
		if slices.Contains(spec.Argv, "--setting-sources") {
			t.Errorf("%s argv uses --setting-sources, which stops project skills loading: %v", name, spec.Argv)
		}
		i := slices.Index(spec.Argv, "--disallowedTools")
		if i < 0 {
			t.Fatalf("%s argv lacks --disallowedTools: %v", name, spec.Argv)
		}
		for _, tool := range claudeOutwardTools {
			if !strings.Contains(spec.Argv[i+1], tool) {
				t.Errorf("%s --disallowedTools %q lacks %s", name, spec.Argv[i+1], tool)
			}
		}
		if !slices.Contains(spec.Env, "ENABLE_CLAUDEAI_MCP_SERVERS=false") {
			t.Errorf("%s env lacks ENABLE_CLAUDEAI_MCP_SERVERS=false: %v", name, spec.Env)
		}
	}
	// The eval keeps its prompts-off posture and the trigger its allowlist.
	if !containsPair(judgeless["eval"].Argv, "--permission-mode", "bypassPermissions") {
		t.Error("eval lost bypassPermissions")
	}
	if !containsPair(judgeless["trigger"].Argv, "--allowedTools", "Skill Read") {
		t.Error("trigger lost its allowedTools")
	}
}

func TestCodexSpecsAreLocalOnly(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	ws := t.TempDir()
	specs := map[string]model.CommandSpec{
		"trigger": NewCodex().TriggerSpec(ws, "q", "m", model.InnerSandbox{}),
		"eval":    NewCodex().EvalSpec(ws, model.EvalInput{Prompt: "p"}, "m"),
		"judge":   NewCodex().JudgeSpec(t.TempDir(), model.JudgeInput{Prompt: "p", Workspace: ws, Schema: "{}"}, "m"),
	}
	for name, spec := range specs {
		for _, f := range codexOutwardFeatures {
			if !containsPair(spec.Argv, "--disable", f) {
				t.Errorf("%s argv does not disable %s: %v", name, f, spec.Argv)
			}
		}
		if !containsPair(spec.Argv, "-c", `web_search="disabled"`) {
			t.Errorf("%s argv does not disable web search: %v", name, spec.Argv)
		}
	}
}

// claudeInitLine is a session init event with the given tools and MCP servers.
func claudeInitLine(tools, mcp, mode string) []byte {
	return []byte(`{"type":"system","subtype":"init","tools":[` + tools + `],"mcp_servers":` + mcp + `,"permissionMode":"` + mode + `"}`)
}

const claudeGoodTools = `"Task","Bash","Edit","Read","Skill","ToolSearch","Write","Monitor","NotebookEdit","EnterWorktree","ExitWorktree","ReportFindings","TaskCreate","TaskGet","TaskList","TaskStop","TaskUpdate"`

func TestClaudePostureAccepts(t *testing.T) {
	c := NewClaude()
	line := claudeInitLine(claudeGoodTools, "[]", "bypassPermissions")
	if !c.PostureDone(line) {
		t.Error("PostureDone must fire on the init event")
	}
	if c.PostureDone([]byte(`{"type":"assistant"}`)) || c.PostureDone([]byte("not json")) {
		t.Error("PostureDone fired on a non-init line")
	}
	stdout := append([]byte(`{"type":"stream_event"}`+"\n"), line...)
	if err := c.CheckPosture(stdout); err != nil {
		t.Errorf("CheckPosture on a local-only session = %v", err)
	}
}

func TestClaudePostureRejects(t *testing.T) {
	c := NewClaude()
	for name, tc := range map[string]struct {
		line []byte
		want string
	}{
		"outward tool":    {claudeInitLine(claudeGoodTools+`,"WebFetch"`, "[]", "bypassPermissions"), "WebFetch"},
		"unreviewed tool": {claudeInitLine(claudeGoodTools+`,"BrandNewTool"`, "[]", "bypassPermissions"), "BrandNewTool is new"},
		"mcp tool":        {claudeInitLine(claudeGoodTools+`,"mcp__srv__do"`, "[]", "bypassPermissions"), "mcp__srv__do"},
		"mcp server":      {claudeInitLine(claudeGoodTools, `[{"name":"claude.ai Gmail","status":"needs-auth"}]`, "bypassPermissions"), "MCP servers"},
		"permission mode": {claudeInitLine(claudeGoodTools, "[]", "default"), `permission mode is "default"`},
	} {
		err := c.CheckPosture(tc.line)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: CheckPosture = %v, want it to mention %q", name, err, tc.want)
		}
	}
	if err := c.CheckPosture([]byte(`{"type":"result"}`)); err == nil || !strings.Contains(err.Error(), "no init event") {
		t.Errorf("no init: CheckPosture = %v, want a no-init error", err)
	}
}

func codexFeatureTable(overrides map[string]string) []byte {
	var b strings.Builder
	for _, f := range codexOutwardFeatures {
		state := "false"
		if v, ok := overrides[f]; ok {
			state = v
		}
		if state != "" {
			b.WriteString(f + strings.Repeat(" ", 30) + "stable             " + state + "\n")
		}
	}
	b.WriteString("hooks                                    stable             true\n")
	return []byte(b.String())
}

func TestCodexPosture(t *testing.T) {
	c := NewCodex()
	if c.PostureDone([]byte("anything")) {
		t.Error("the codex probe exits on its own and must never end early")
	}
	if err := c.CheckPosture(codexFeatureTable(nil)); err != nil {
		t.Errorf("all outward features off: CheckPosture = %v", err)
	}
	if err := c.CheckPosture(codexFeatureTable(map[string]string{"apps": "true"})); err == nil || !strings.Contains(err.Error(), "apps is still enabled") {
		t.Errorf("an enabled feature: CheckPosture = %v", err)
	}
	if err := c.CheckPosture(codexFeatureTable(map[string]string{"plugins": ""})); err == nil || !strings.Contains(err.Error(), "plugins is no longer listed") {
		t.Errorf("a missing feature: CheckPosture = %v", err)
	}
	if err := c.CheckPosture(nil); err == nil {
		t.Error("an empty feature list must be an error")
	}
	spec := c.PostureSpec(t.TempDir(), "", model.InnerSandbox{})
	if !slices.Equal(spec.Argv[len(spec.Argv)-2:], []string{"features", "list"}) || !containsPair(spec.Argv, "--disable", "apps") {
		t.Errorf("posture argv = %v, want the disable flags then `features list`", spec.Argv)
	}
}
