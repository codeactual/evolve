// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/codeactual/evolve/internal/model"
)

// This file holds the "local-only" surface of the agent sessions: the flags that
// keep an agent under test from reaching outward (the web, remote services,
// connectors, marketplace plugins, MCP servers), the capability that verifies
// them before a run, and the checks themselves. evolve evaluates first-party
// skills that are already on the filesystem, so nothing outside the workspace
// should be reachable through the agent's own tools either.

// claudeOutwardTools are the Claude Code built-in tools that reach outside the
// workspace and the sandboxed shell: the web, remote triggers, push
// notifications, scheduled and cross-session messaging, and the design sync
// service. They are denied with --disallowedTools (deny rules hold under
// bypassPermissions). In-process tools like WebFetch are not gated by the
// sandbox's network allowlist, so denying them is the only control.
//
// The names are those of claude 2.1.285 (observed in a real session's init
// event on 2026-09-30), plus Workflow. Workflow runs a script that orchestrates
// many background subagents (https://code.claude.com/docs/en/tools-reference),
// a fan-out the posture review never covered and an eval session never needs, so
// it is denied. claude lists it at init only where dynamic workflows are enabled
// (an account and settings matter, https://code.claude.com/docs/en/workflows):
// it was seen on claude 2.1.287, and claude 2.1.289 on the 2026-10-05
// verification host did not list it. A CLI update that adds another outward tool
// fails the posture preflight (see claudeReviewedTools) rather than slipping
// through.
var claudeOutwardTools = []string{
	"WebFetch", "WebSearch", "RemoteTrigger", "PushNotification",
	"CronCreate", "CronDelete", "CronList", "ScheduleWakeup",
	"SendMessage", "ListAgents", "DesignSync", "Workflow",
}

// claudeReviewedTools are the built-in tools a local-only agent session may
// list at init: everything claude 2.1.285 exposes after the deny list above, plus
// Glob and Grep, which other builds list. claude 2.1.289 lists a subset of these
// (checked 2026-10-05). The posture preflight fails on any tool outside this
// set, so a new tool arrives as a reviewed decision.
var claudeReviewedTools = []string{
	"Task", "Bash", "Edit", "EnterWorktree", "ExitWorktree", "Monitor", "NotebookEdit",
	"Read", "ReportFindings", "Skill", "TaskCreate", "TaskGet", "TaskList", "TaskStop",
	"TaskUpdate", "ToolSearch", "Write", "Glob", "Grep",
}

// claudeLocalOnlyArgs are the flags that keep a Claude agent session local:
//
//   - --strict-mcp-config with no --mcp-config: no MCP servers from project or
//     account configuration;
//   - --disallowedTools: the outward tools above.
//
// --setting-sources user is deliberately NOT used: verified live on 2026-09-30
// against claude 2.1.285 that it also stops the workspace's .claude/skills from
// loading, which would leave evals with no skill under test. Project settings
// that enable plugins, marketplaces or MCP servers are caught statically
// instead, by the Tier 0 checks.local_only check.
func claudeLocalOnlyArgs() []string {
	return []string{
		"--strict-mcp-config",
		"--disallowedTools", strings.Join(claudeOutwardTools, " "),
	}
}

// claudeLocalOnlyEnv is the environment half: ENABLE_CLAUDEAI_MCP_SERVERS=false
// opts out of the claude.ai account's MCP connectors. The variable exists in
// claude 2.1.285, but no connector was listed at session start in any run on the
// verification host, so its effect there is unverified; the posture preflight
// asserts the result (no MCP servers) regardless.
var claudeLocalOnlyEnv = []string{"ENABLE_CLAUDEAI_MCP_SERVERS=false"}

// codexOutwardFeatures are the Codex features, enabled by default in codex
// 0.159.2, that reach outside the workspace: app connectors, plugins and their
// remote catalog and sharing, MCP dependency installation for skills, browser
// and computer use, and image generation. They are turned off with --disable.
// skill_search, hooks and multi_agent stay on: they look local, and skill
// discovery is what trigger evals measure.
var codexOutwardFeatures = []string{
	"apps", "plugins", "remote_plugin", "plugin_sharing", "skill_mcp_dependency_install",
	"browser_use", "browser_use_external", "computer_use", "image_generation",
}

// codexLocalOnlyArgs disables those features and the web search tool
// (web_search defaults to cached results; "disabled" removes it).
func codexLocalOnlyArgs() []string {
	var args []string
	for _, f := range codexOutwardFeatures {
		args = append(args, "--disable", f)
	}
	return append(args, "-c", `web_search="disabled"`)
}

// PostureChecker is the optional capability of verifying, before any agent
// runs, that a harness's configured session surface is local-only. The engine
// runs PostureSpec through the same runner, sandbox and environment as real
// agent runs, feeds stdout lines to PostureDone to end a probe early, and asks
// CheckPosture for the verdict, so a renamed flag, a new outward tool or a
// changed default fails at the start of a run instead of mid-sweep.
type PostureChecker interface {
	// PostureSpec builds the probe. ws is a scratch directory; cliModelID is the
	// harness-specific model the run would use (ignored where no session starts).
	PostureSpec(ws, cliModelID string, inner model.InnerSandbox) model.CommandSpec
	// PostureDone reports whether line already carries what CheckPosture needs, so
	// the probe can be cancelled before it does anything more.
	PostureDone(line []byte) bool
	// CheckPosture returns nil when stdout shows a local-only surface, otherwise an
	// error naming every violation (or why the surface could not be read).
	CheckPosture(stdout []byte) error
}

// PostureSpec builds a real eval session with a trivial prompt, so the flags
// are exactly the engine's; the probe is cancelled at the init event, before the
// session does anything.
func (c *Claude) PostureSpec(ws, cliModelID string, inner model.InnerSandbox) model.CommandSpec {
	return c.EvalSpec(ws, model.EvalInput{Prompt: "posture check", MaxTurns: 1, InnerSandbox: inner}, cliModelID)
}

// PostureDone is true on the session's init event.
func (c *Claude) PostureDone(line []byte) bool {
	_, ok := parseClaudeInit(line)
	return ok
}

type claudeInit struct {
	Tools          []string        `json:"tools"`
	McpServers     json.RawMessage `json:"mcp_servers"`
	PermissionMode string          `json:"permissionMode"`
}

func parseClaudeInit(line []byte) (claudeInit, bool) {
	var ev struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		claudeInit
	}
	if json.Unmarshal(bytes.TrimSpace(line), &ev) != nil || ev.Type != "system" || ev.Subtype != "init" {
		return claudeInit{}, false
	}
	return ev.claudeInit, true
}

// CheckPosture reads the init event and fails on: an outward tool, a tool nobody
// has reviewed (a CLI update added it), an MCP tool or server, or a permission
// mode other than bypassPermissions (which would end the prompts-off posture).
func (c *Claude) CheckPosture(stdout []byte) error {
	var init claudeInit
	found := false
	for line := range bytes.SplitSeq(stdout, []byte{'\n'}) {
		if got, ok := parseClaudeInit(line); ok {
			init, found = got, true
			break
		}
	}
	if !found {
		return errors.New("claude produced no init event, so its tool surface could not be checked")
	}
	var problems []string
	for _, tool := range init.Tools {
		switch {
		case slices.Contains(claudeOutwardTools, tool):
			problems = append(problems, fmt.Sprintf("outward tool %s is available despite --disallowedTools", tool))
		case strings.HasPrefix(tool, "mcp__"):
			problems = append(problems, fmt.Sprintf("MCP tool %s is available", tool))
		case !slices.Contains(claudeReviewedTools, tool):
			problems = append(problems, fmt.Sprintf("tool %s is new and has not been reviewed: add it to claudeReviewedTools, or to claudeOutwardTools to deny it", tool))
		}
	}
	if servers := bytes.TrimSpace(init.McpServers); len(servers) > 0 && !bytes.Equal(servers, []byte("[]")) && !bytes.Equal(servers, []byte("null")) {
		problems = append(problems, "MCP servers are configured for the session: "+string(servers))
	}
	if init.PermissionMode != "bypassPermissions" {
		problems = append(problems, fmt.Sprintf("permission mode is %q, want bypassPermissions", init.PermissionMode))
	}
	if len(problems) > 0 {
		return errors.New("claude session is not local-only: " + strings.Join(problems, "; "))
	}
	return nil
}

// PostureSpec lists the feature table with the same --disable flags the agent
// runs use. No session starts, so it costs no tokens and needs no network.
func (c *Codex) PostureSpec(ws, _ string, _ model.InnerSandbox) model.CommandSpec {
	env, readPaths := codexEnv(ws)
	argv := append([]string{"codex"}, codexLocalOnlyArgs()...)
	return model.CommandSpec{Argv: append(argv, "features", "list"), Dir: ws, Env: env, ReadPaths: readPaths}
}

// PostureDone never ends the probe early: `codex features list` exits on its own.
func (c *Codex) PostureDone([]byte) bool { return false }

// CheckPosture parses `codex features list` and requires every outward feature
// to read false. A feature the CLI no longer lists is a violation too: it was
// renamed or removed, so the --disable flag no longer does what it says.
func (c *Codex) CheckPosture(stdout []byte) error {
	state := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) >= 2 {
			state[fields[0]] = fields[len(fields)-1]
		}
	}
	if len(state) == 0 {
		return errors.New("codex produced no feature list, so its feature surface could not be checked")
	}
	var problems []string
	for _, f := range codexOutwardFeatures {
		switch state[f] {
		case "false":
		case "":
			problems = append(problems, fmt.Sprintf("feature %s is no longer listed by codex (renamed or removed): review codexOutwardFeatures", f))
		default:
			problems = append(problems, fmt.Sprintf("feature %s is still enabled despite --disable", f))
		}
	}
	if len(problems) > 0 {
		return errors.New("codex session is not local-only: " + strings.Join(problems, "; "))
	}
	return nil
}
