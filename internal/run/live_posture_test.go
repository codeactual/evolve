// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

//go:build live && linux

package run

import (
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/codeactual/evolve/internal/harness"
	"github.com/codeactual/evolve/internal/model"
)

// These tests drive the real claude and codex inside the real sandbox to prove
// the first-party-only posture: no outward tools, no MCP servers, no project
// settings, no connectors or plugins. Each has a negative control, so a pass
// means the check can fail.

// unflaggedClaude is Claude with its local-only flags stripped from the posture
// probe: the negative control for the preflight.
type unflaggedClaude struct{ *harness.Claude }

func (u unflaggedClaude) PostureSpec(ws, cliModelID string, inner model.InnerSandbox) model.CommandSpec {
	spec := u.Claude.PostureSpec(ws, cliModelID, inner)
	spec.Argv = stripClaudeLocalOnly(spec.Argv)
	return spec
}

// stripClaudeLocalOnly removes --strict-mcp-config and --disallowedTools <list>
// from argv.
func stripClaudeLocalOnly(argv []string) []string {
	var out []string
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--strict-mcp-config":
		case "--disallowedTools":
			i++
		default:
			out = append(out, argv[i])
		}
	}
	return out
}

// unflaggedCodex is Codex with its --disable flags stripped from the probe.
type unflaggedCodex struct{ *harness.Codex }

func (u unflaggedCodex) PostureSpec(ws, cliModelID string, inner model.InnerSandbox) model.CommandSpec {
	spec := u.Codex.PostureSpec(ws, cliModelID, inner)
	var argv []string
	for i := 0; i < len(spec.Argv); i++ {
		if spec.Argv[i] == "--disable" || spec.Argv[i] == "-c" {
			i++
			continue
		}
		argv = append(argv, spec.Argv[i])
	}
	spec.Argv = argv
	return spec
}

func TestLiveAgentsHaveNoOutwardSurface(t *testing.T) {
	sels := judgeSelections()
	for name, h := range map[string]harness.Harness{"claude": sels["claude"].Harness, "codex": sels["codex"].Harness} {
		t.Run(name, func(t *testing.T) {
			cliModelID, _ := sels[name].Model.CLIModelID(name)
			r := liveJudgeRunner(t)
			if err := CheckPosture(context.Background(), r, h, cliModelID, model.InnerSandbox{}, PostureTimeout); err != nil {
				t.Fatalf("the real %s CLI does not present a local-only surface: %v", name, err)
			}
		})
	}
}

// TestLiveAgentPostureNegativeControls shows the preflight fails when the flags
// are missing, so the pass above is not vacuous.
func TestLiveAgentPostureNegativeControls(t *testing.T) {
	sels := judgeSelections()
	claudeModel, _ := sels["claude"].Model.CLIModelID("claude")
	err := CheckPosture(context.Background(), liveJudgeRunner(t), unflaggedClaude{harness.NewClaude()}, claudeModel, model.InnerSandbox{}, PostureTimeout)
	if err == nil || !strings.Contains(err.Error(), "WebFetch") {
		t.Errorf("claude without its local-only flags: CheckPosture = %v, want it to name an outward tool such as WebFetch", err)
	}
	err = CheckPosture(context.Background(), liveJudgeRunner(t), unflaggedCodex{harness.NewCodex()}, "", model.InnerSandbox{}, PostureTimeout)
	if err == nil || !strings.Contains(err.Error(), "still enabled") {
		t.Errorf("codex without its --disable flags: CheckPosture = %v, want a still-enabled feature", err)
	}
}

// initOf extracts the session init event's skills and MCP servers from stdout.
func initOf(t *testing.T, stdout []byte) (skills []string, mcp string) {
	t.Helper()
	for _, line := range strings.Split(string(stdout), "\n") {
		var ev struct {
			Type, Subtype string
			Skills        []string        `json:"skills"`
			McpServers    json.RawMessage `json:"mcp_servers"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "system" && ev.Subtype == "init" {
			return ev.Skills, strings.TrimSpace(string(ev.McpServers))
		}
	}
	t.Fatalf("no init event in the session output:\n%.1500s", stdout)
	return nil, ""
}

// TestLiveClaudeIgnoresFixtureMCP plants project settings that enable MCP servers
// and an .mcp.json in the workspace, next to a real project skill. With the
// local-only flags no MCP server is configured and the skill still loads. Without
// them the same workspace lists the planted server (the negative control), so a
// pass shows the flags doing the work.
func TestLiveClaudeIgnoresFixtureMCP(t *testing.T) {
	run := func(t *testing.T, strip bool) (skills []string, mcp string) {
		t.Helper()
		ws := t.TempDir()
		writeWS(t, ws, ".claude/settings.json", `{"enableAllProjectMcpServers":true}`)
		writeWS(t, ws, ".mcp.json", `{"mcpServers":{"planted":{"command":"/bin/true"}}}`)
		writeWS(t, ws, ".claude/skills/probe-skill/SKILL.md",
			"---\nname: probe-skill\ndescription: Use when asked about probe things.\n---\nbody\n")
		spec := harness.NewClaude().EvalSpec(ws, model.EvalInput{Prompt: "reply with the word ok", MaxTurns: 1},
			envOr("EVOLVE_LIVE_CLAUDE_MODEL", "claude-haiku-4-5"))
		if strip {
			spec.Argv = stripClaudeLocalOnly(spec.Argv)
		}
		cli, err := exec.LookPath("claude")
		if err != nil {
			t.Fatal(err)
		}
		spec.Argv[0] = cli
		res, err := liveJudgeRunner(t).Run(context.Background(), spec, 3*time.Minute, nil)
		if err != nil {
			t.Fatal(err)
		}
		return initOf(t, res.Stdout)
	}

	skills, mcp := run(t, false)
	if mcp != "[]" {
		t.Errorf("mcp_servers = %s, want none with --strict-mcp-config", mcp)
	}
	if !slices.Contains(skills, "probe-skill") {
		t.Errorf("the project skill did not load: skills = %v", skills)
	}
	controlSkills, controlMCP := run(t, true)
	if !strings.Contains(controlMCP, "planted") {
		t.Errorf("negative control: without the flags the planted MCP server was not listed (mcp_servers = %s), so this test cannot show the flag working", controlMCP)
	}
	if !slices.Contains(controlSkills, "probe-skill") {
		t.Errorf("control: the project skill did not load even without the flags: %v", controlSkills)
	}
}
