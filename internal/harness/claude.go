// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codeactual/evolve/internal/model"
)

// Claude drives the `claude` CLI (Claude Code).
type Claude struct {
	base
}

// NewClaude returns the builtin Claude Code harness.
func NewClaude() *Claude {
	return &Claude{base: base{
		id:   model.HarnessClaude,
		name: "Claude Code",
		clis: []string{"claude"},
		// Credentials the claude CLI itself authenticates with. Both an API-key
		// and an OAuth-token form are accepted.
		envKeys: []string{
			"EVOLVE_ANTHROPIC_API_KEY", "EVOLVE_CLAUDE_CODE_OAUTH_TOKEN",
			"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN",
		},
		skillDirs: []string{filepath.Join(".claude", "skills")},
	}}
}

// claudeSandboxSettings renders the inline --settings JSON that turns Claude
// Code's own Bash-tool OS sandbox on, fail-closed, inside evolve's outer
// sandbox (the layers nest on Linux, where both use bubblewrap):
//
//   - enabled turns the sandbox on. It is off by default, and evolve's isolated
//     CLAUDE_CONFIG_DIR hides the operator's user settings, so without this
//     Claude would never sandbox Bash under evolve.
//   - failIfUnavailable makes a sandbox that cannot start (no bubblewrap or
//     socat, or no nested user namespaces) an error instead of a warning.
//   - allowUnsandboxedCommands=false removes the dangerouslyDisableSandbox
//     retry that would let a command run outside it.
//   - network.allowedDomains lists the hosts Bash commands may reach; empty
//     means none. The sandbox runtime (srt) cannot express "allow all".
//   - network.strictAllowlist denies any other host deterministically: a
//     headless run cannot answer the approval prompt srt would otherwise raise.
//
// Keys verified 2026-09-30 against the settings schema embedded in claude
// 2.1.285 (the Zod definitions in its binary: each description names --settings
// as a honored source for strictAllowlist and allowUnsandboxedCommands) and the
// sandboxing docs at https://code.claude.com/docs/en/sandboxing.
func claudeSandboxSettings(in model.InnerSandbox) string {
	type network struct {
		AllowedDomains  []string `json:"allowedDomains"`
		StrictAllowlist bool     `json:"strictAllowlist"`
	}
	type sandbox struct {
		Enabled                  bool    `json:"enabled"`
		FailIfUnavailable        bool    `json:"failIfUnavailable"`
		AllowUnsandboxedCommands bool    `json:"allowUnsandboxedCommands"`
		Network                  network `json:"network"`
	}
	domains := in.ClaudeAllowedDomains
	if domains == nil {
		domains = []string{} // marshal as [], never null
	}
	out, err := json.Marshal(struct {
		Sandbox sandbox `json:"sandbox"`
	}{sandbox{
		Enabled: true, FailIfUnavailable: true, AllowUnsandboxedCommands: false,
		Network: network{AllowedDomains: domains, StrictAllowlist: true},
	}})
	if err != nil { // plain strings and bools: cannot happen
		panic(err)
	}
	return string(out)
}

// claudeConfigRel is the workspace-relative CLAUDE_CONFIG_DIR evolve gives the
// claude CLI. Sessions, project history, and auto-memory live here so runs do
// not touch the operator's real ~/.claude; the tree dies with the workspace.
// Project skills stay at .claude/skills (the skillDirs mount).
const claudeConfigRel = ".evolve/claude-home"

// claudeEnv returns the process env extras that point a claude invocation in
// ws at a throwaway workspace-rooted config dir, and the operator files the
// run reads through that dir (the bridged credentials), which a sandboxed run
// must bind read-only.
//
// Credentials are the one thing the agent process needs from the operator's
// environment: only the variables the claude CLI itself reads are forwarded,
// and only when set.
//
// CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1, which would strip those credentials from
// the tool subprocesses' environment, is deliberately NOT set: in claude 2.1.285
// it forces the permission mode to default whatever --permission-mode says
// (Claude Code's "allowed_non_write_users hardening"), which would end the
// prompts-off eval posture and deny every tool the eval did not list in
// --allowedTools. Verified live on 2026-09-30. A credential the operator
// exports is therefore visible to Claude's shell commands; a file-based login
// (the default) never reaches the environment at all.
func claudeEnv(ws string) (env, readPaths []string) {
	dir := isolatedDir(ws, claudeConfigRel)
	if target := ensureClaudeConfig(dir); target != "" {
		readPaths = append(readPaths, target)
	}
	env = []string{
		"CLAUDE_CONFIG_DIR=" + dir,
		"DISABLE_AUTOUPDATER=1",
	}
	return append(env, forwardedEnv(claudeCredentialEnv)...), readPaths
}

// claudeCredentialEnv are the variables the claude CLI reads to authenticate.
// The EVOLVE_-prefixed keys are token-counting credentials and never reach it.
var claudeCredentialEnv = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"}

// ensureClaudeConfig creates the isolated config dir, seeds the state file,
// and links the operator's OAuth credentials: Claude keeps .credentials.json
// beside its config, so the link is what carries auth. It returns the real
// path of the linked credentials file ("" when none is linked). Best-effort per
// the isolate.go contract.
func ensureClaudeConfig(dir string) string {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	seedClaudeState(dir)
	opDir := operatorDir("CLAUDE_CONFIG_DIR", ".claude")
	return linkFile(filepath.Join(opDir, ".credentials.json"), filepath.Join(dir, ".credentials.json"))
}

// seedClaudeState writes the isolated .claude.json (with CLAUDE_CONFIG_DIR
// set, the state file lives inside the config dir) with onboarding marked done
// so headless -p runs never stall on first-run prompts. Nothing else carries
// over: logged-in state is purely a matter of reachable credentials (env var
// or .credentials.json), and the operator's session
// history, project state, and caches deliberately stay behind.
func seedClaudeState(dir string) {
	state := filepath.Join(dir, ".claude.json")
	if _, err := os.Lstat(state); err != nil {
		_ = os.WriteFile(state, []byte(`{"hasCompletedOnboarding":true}`+"\n"), 0o600)
	}
}

// TriggerSpec builds the headless `claude -p` command for one trigger query.
func (c *Claude) TriggerSpec(ws, query, cliModelID string, inner model.InnerSandbox) model.CommandSpec {
	argv := []string{
		"claude", "-p", query,
		"--model", cliModelID,
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", "2",
		"--allowedTools", "Skill Read",
		"--settings", claudeSandboxSettings(inner),
	}
	env, readPaths := claudeEnv(ws)
	return model.CommandSpec{Argv: argv, Dir: ws, Env: env, ReadPaths: readPaths}
}

// claudeContentBlock is one content block of a Claude message in stream-json
// output. A tool_use block carries the invoked tool's name and the raw JSON
// arguments (an MCP tool surfaces with name "mcp__<server>__<tool>").
type claudeContentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// claudeUsage is the token/cost accounting Claude reports on its terminal
// result event. Cache reads and writes are kept on their own fields; see
// ParseEvalOutput for why they are not folded into input.
type claudeUsage struct {
	InputTokens              int  `json:"input_tokens"`
	CacheCreationInputTokens int  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int  `json:"cache_read_input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
}

// claudeRateLimit is the rate_limit_info payload of a rate_limit_event stream
// line. Observed shape (claude 2.x): {"type":"rate_limit_event",
// "rate_limit_info":{"status":"allowed","resetsAt":<epoch>,
// "rateLimitType":"five_hour","overageStatus":"rejected"}}. Status flips to
// "rejected" once the window is spent and overage is declined.
type claudeRateLimit struct {
	Status        string `json:"status"`
	RateLimitType string `json:"rateLimitType"`
	ResetsAt      int64  `json:"resetsAt"`
}

// claudeEvent is one line of Claude Code's stream-json (--verbose) output.
// Assistant events carry message.content blocks (text and tool_use); the
// terminal type:"result" event carries the final answer, usage, cost, and the
// error envelope (is_error/subtype/errors); a rate_limit_event carries the
// account's rate-limit state. Each event populates only its own fields, so the
// unused ones stay zero on the others.
type claudeEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []claudeContentBlock `json:"content"`
	} `json:"message"`
	Result        string           `json:"result"`
	IsError       bool             `json:"is_error"`
	Subtype       string           `json:"subtype"`
	Errors        []string         `json:"errors"`
	Usage         *claudeUsage     `json:"usage"`
	TotalCostUSD  *float64         `json:"total_cost_usd"`
	RateLimitInfo *claudeRateLimit `json:"rate_limit_info"`
	// StructuredOutput is the object a --json-schema run validated and returned,
	// verbatim, on its result event.
	StructuredOutput json.RawMessage `json:"structured_output"`
}

// scanEvents walks Claude Code's stream-json output once: it returns the
// terminal result event (found is false when the output carried none — e.g.
// plain text or a crash mid-stream), every tool_use invocation in observed
// order, and the last rate_limit_event seen (the status can transition
// mid-session, so last is authoritative; nil when none appeared).
// ParseEvalOutput, ParseToolCalls, and RuntimeError each project from it.
func scanEvents(stdout []byte) (result claudeEvent, found bool, tools []model.ToolCall, rateLimit *claudeRateLimit) {
	for line := range bytes.SplitSeq(stdout, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev claudeEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Type == "result" {
			result, found = ev, true
		}
		if ev.Type == "rate_limit_event" && ev.RateLimitInfo != nil {
			rateLimit = ev.RateLimitInfo
		}
		for _, block := range ev.Message.Content {
			if block.Type == "tool_use" {
				tools = append(tools, model.ToolCall{Name: block.Name, Input: block.Input})
			}
		}
	}
	return result, found, tools, rateLimit
}

// ScanLine reports a hit when a Skill or Read tool_use in the stream-json event
// targets the skill.
func (c *Claude) ScanLine(line []byte, skill, _ string) (bool, string) {
	var event claudeEvent
	if json.Unmarshal(line, &event) != nil {
		return false, ""
	}
	for _, block := range event.Message.Content {
		if block.Type != "tool_use" {
			continue
		}
		payload := string(block.Input)
		if block.Name == "Skill" && strings.Contains(payload, skill) {
			return true, ""
		}
		if block.Name == "Read" && strings.Contains(payload, "skills/"+skill+"/SKILL.md") {
			return true, ""
		}
	}
	return false, ""
}

// EvalSpec runs claude with permissions bypassed: evals grade what the agent
// builds, not what a tool allowlist happens to permit, and confinement comes
// from the layered sandboxes (evolve's outer one, and Claude Code's own Bash
// sandbox inside it, see claudeSandboxSettings) rather than from permission
// prompts.
func (c *Claude) EvalSpec(ws string, in model.EvalInput, cliModelID string) model.CommandSpec {
	maxTurns := in.MaxTurns
	if maxTurns == 0 {
		maxTurns = model.DefaultMaxTurns
	}
	argv := []string{
		"claude", "-p", in.Prompt,
		"--model", cliModelID,
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", strconv.Itoa(maxTurns),
		"--permission-mode", "bypassPermissions",
		"--settings", claudeSandboxSettings(in.InnerSandbox),
	}
	env, readPaths := claudeEnv(ws)
	return model.CommandSpec{Argv: argv, Dir: ws, Env: env, ReadPaths: readPaths}
}

// JudgeSpec builds the grading session. The judge reads the workspace it grades
// and nothing else can be trusted to stay put, so it runs with every path that
// content under test could use to act through it closed:
//
//   - its own directory (never the workspace) and its own fresh CLAUDE_CONFIG_DIR
//     under that directory, so an agent-planted user-level settings.json,
//     apiKeyHelper or env block is never loaded;
//   - --restricted: no code-running tools, user/project/local settings ignored,
//     file tools confined to the working directory plus --add-dir, and
//     bypassPermissions refused;
//   - --safe-mode: no CLAUDE.md, skills, plugins, hooks, MCP servers or custom
//     commands from the workspace;
//   - --tools Read,Grep,Glob and --permission-mode dontAsk: reads only, anything
//     else denied;
//   - --json-schema: the verdicts arrive as validated structured output.
//
// Confirmed live against claude 2.1.285 (2026-09-30): with --restricted and
// --tools Read,Grep,Glob the session's init event lists exactly Glob, Grep, Read
// and StructuredOutput, in permissionMode dontAsk. The judge workspace is
// exposed through --add-dir and, inside evolve's sandbox, bound read-only.
func (c *Claude) JudgeSpec(judgeDir string, in model.JudgeInput, cliModelID string) model.CommandSpec {
	argv := []string{
		"claude", "-p", in.Prompt,
		"--model", cliModelID,
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", strconv.Itoa(in.MaxTurns),
		"--restricted", "--safe-mode", "--strict-mcp-config",
		"--tools", "Read,Grep,Glob",
		"--permission-mode", "dontAsk",
		"--add-dir", in.Workspace,
		"--json-schema", in.Schema,
	}
	env, readPaths := claudeEnv(judgeDir)
	return model.CommandSpec{
		Argv: argv, Dir: judgeDir, Env: env,
		ReadPaths: append(readPaths, in.Workspace),
	}
}

// ParseJudgeOutput returns the verdicts object from the result event: the
// validated structured_output when present, otherwise the result text if it is
// itself the whole JSON object. There is no substring scan, so a verdict block
// quoted from the content under test can never stand in for the judge's answer.
func (c *Claude) ParseJudgeOutput(stdout []byte) ([]byte, error) {
	result, found, _, _ := scanEvents(stdout)
	if !found {
		return nil, errors.New("no result event in the judge's output")
	}
	if len(bytes.TrimSpace(result.StructuredOutput)) > 0 && !bytes.Equal(bytes.TrimSpace(result.StructuredOutput), []byte("null")) {
		return bytes.TrimSpace(result.StructuredOutput), nil
	}
	return wholeJSONObject(result.Result)
}

// wholeJSONObject accepts text only when it is, after trimming whitespace,
// exactly one JSON object — no prose, code fences, or second value around it.
func wholeJSONObject(text string) ([]byte, error) {
	trimmed := bytes.TrimSpace([]byte(text))
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, errors.New("the judge's final message is not a single JSON object")
	}
	return trimmed, nil
}

// ParseEvalOutput reads the final answer and usage from the terminal result
// event of claude's stream-json output. Cache writes and reads are reported on
// their own fields rather than folded into input: a multi-turn cached session
// re-reads the same base context every turn, so lumping cache reads into
// "input" inflates it many-fold over the (cheaply cached) reality.
// total_cost_usd still reflects everything the session consumed. Output with no
// result event (plain text, crash) falls back to the raw stdout and nil usage.
func (c *Claude) ParseEvalOutput(stdout []byte) (string, *model.Usage) {
	result, found, _, _ := scanEvents(stdout)
	if !found {
		return string(stdout), nil
	}
	if result.Usage == nil {
		return result.Result, nil
	}
	in := result.Usage.InputTokens
	cacheRead := result.Usage.CacheReadInputTokens
	cacheCreation := result.Usage.CacheCreationInputTokens
	return result.Result, &model.Usage{
		InputTokens:         &in,
		CacheReadTokens:     &cacheRead,
		CacheCreationTokens: &cacheCreation,
		OutputTokens:        result.Usage.OutputTokens,
		CostUSD:             result.TotalCostUSD,
	}
}

// ParseToolCalls returns every tool_use invocation in claude's stream-json
// output, in observed order. MCP tools surface as mcp__<server>__<tool>. The
// ToolCallReporter contract is satisfied: a run with no tool calls yields nil
// here, which the engine normalizes to a non-nil empty slice (assertion fails),
// reserving nil for harnesses that cannot report tool calls at all.
func (c *Claude) ParseToolCalls(stdout []byte) []model.ToolCall {
	_, _, tools, _ := scanEvents(stdout)
	return tools
}

// ReportsUsage reports that the claude CLI reports session usage and cost.
func (c *Claude) ReportsUsage() bool { return true }

// RuntimeError detects a claude CLI run that produced no usable answer (auth
// blocked, init crash, error envelope without output) so it can be reported
// distinctly from an eval that ran and failed its assertions. A run with any
// non-empty result is gradable — this deliberately includes max-turns/partial
// runs, which the CLI reports with is_error=true but a populated result — with
// one carve-out: a usage-limit rejection. The CLI reports that as a
// success-shaped result (exit 0, the limit banner as the result text, zero
// output tokens), so without the carve-out rate-limited runs would be silently
// graded into all-fail rows.
func (c *Claude) RuntimeError(stdout []byte, exitCode int, _ bool) string {
	if len(bytes.TrimSpace(stdout)) == 0 {
		return "empty CLI output"
	}
	result, found, _, rateLimit := scanEvents(stdout)
	if !found {
		if exitCode != 0 {
			return "unparseable CLI output"
		}
		return "" // a clean exit with plain-text output is degenerate but gradable
	}
	if reason := claudeUsageLimitReason(result, rateLimit); reason != "" {
		return reason
	}
	if result.Result != "" {
		return "" // there is an answer to grade (success, or a partial/max-turns run)
	}
	if result.IsError {
		return claudeErrorReason(result.Subtype, result.Errors)
	}
	return "" // empty-result success: grade it (assertions may inspect the workspace)
}

// claudeUsageLimitRE matches the usage-limit banner the claude CLI returns as
// its result text once the account's window is spent (exact phrasing varies by
// CLI version/path, e.g. "Claude AI usage limit reached|<epoch>").
var claudeUsageLimitRE = regexp.MustCompile(`(?i)usage limit reached|limit reached.*resets`)

// claudeUsageLimitReason classifies a run rejected by the account's usage
// limit: the last rate-limit event reports status "rejected" and the result
// carries zero output tokens, or the result text itself is a usage-limit
// banner (CLI versions/paths that emit the banner without a rejection event).
// A session that hit the limit mid-run but produced real output (non-zero
// output tokens, non-limit result text) stays gradable — only the observed
// 0-token rejection shape is reclassified.
func claudeUsageLimitReason(result claudeEvent, rateLimit *claudeRateLimit) string {
	limitText := claudeUsageLimitRE.MatchString(result.Result)
	zeroOutput := result.Usage == nil || result.Usage.OutputTokens == nil || *result.Usage.OutputTokens == 0
	rejected := rateLimit != nil && rateLimit.Status == "rejected"
	if !limitText && (!rejected || !zeroOutput) {
		return ""
	}
	if rateLimit != nil {
		return fmt.Sprintf("usage limit reached (%s), resets %s", rateLimit.RateLimitType,
			time.Unix(rateLimit.ResetsAt, 0).UTC().Format(time.RFC3339))
	}
	return "usage limit reached: " + strings.TrimSpace(result.Result)
}

// ListOfferedModels asks the installed claude CLI which models the operator's
// account is offered. `claude -p "/model"` is handled client-side (no API
// call) and prints the model-picker alias list — "Available: sonnet, opus,
// haiku, fable, …" — which tracks the account's plan and gated extras. Each
// bare alias is then resolved to its display name ("Sonnet 5") with a second
// client-side invocation, because aliases name families while the picker
// offers specific models: "sonnet" resolving to Sonnet 5 must not read as
// Sonnet 4.6 being offered. Bracketed variants (sonnet[1m]) duplicate a base
// alias's model and are skipped. Probes run against the operator's real
// config on purpose — an isolated CLAUDE_CONFIG_DIR would hide the account's
// gated model options.
func (c *Claude) ListOfferedModels(ctx context.Context, probe ProbeExec) ([]string, error) {
	out, err := probe(ctx, claudeProbeSpec(""), nil)
	if err != nil {
		return nil, err
	}
	aliases := parseClaudeModelAliases(out)
	if len(aliases) == 0 {
		return nil, nil
	}

	names := make([]string, len(aliases))
	var wg sync.WaitGroup
	for i, alias := range aliases {
		wg.Go(func() {
			out, err := probe(ctx, claudeProbeSpec(alias), nil)
			if err != nil {
				return
			}
			names[i] = parseClaudeCurrentModel(out)
		})
	}
	wg.Wait()

	var offered []string
	seen := map[string]bool{}
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			offered = append(offered, n)
		}
	}
	return offered, nil
}

// claudeProbeSpec builds the client-side "/model" probe invocation, optionally
// pinning an alias to resolve. DISABLE_AUTOUPDATER keeps the probe from
// kicking off an update check; it otherwise uses the operator's config.
// --setting-sources user and --strict-mcp-config keep any project settings
// (hooks) and MCP servers of the directory it runs in from loading, so a
// hostile repository cannot execute code through the probe.
func claudeProbeSpec(alias string) model.CommandSpec {
	argv := []string{"claude", "-p", "/model", "--setting-sources", "user", "--strict-mcp-config"}
	if alias != "" {
		argv = append(argv, "--model", alias)
	}
	return model.CommandSpec{Argv: argv, Env: []string{"DISABLE_AUTOUPDATER=1"}}
}

// claudeMetaAliases are the "/model" aliases that name a selection mode, not a
// model: they resolve to a model some concrete alias already names (best,
// default) or to a mode description rather than a model name (opusplan —
// "Opus in plan mode, else Sonnet"). Resolving them adds nothing but junk
// tokens, so they are skipped.
var claudeMetaAliases = map[string]bool{"best": true, "default": true, "opusplan": true}

// parseClaudeModelAliases extracts the bare aliases from the "/model" usage
// line: `Usage: /model <name>. Available: sonnet, opus, …, or a full model
// ID.` Tokens with brackets (context-window variants of a base alias), the
// meta aliases, and the trailing "or a full model ID" prose are dropped.
func parseClaudeModelAliases(out []byte) []string {
	_, rest, found := strings.Cut(string(out), "Available:")
	if !found {
		return nil
	}
	if line, _, ok := strings.Cut(rest, "\n"); ok {
		rest = line
	}
	var aliases []string
	for tok := range strings.SplitSeq(rest, ",") {
		tok = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(tok), "."))
		if tok == "" || strings.ContainsAny(tok, "[] ") || claudeMetaAliases[tok] {
			continue
		}
		aliases = append(aliases, tok)
	}
	return aliases
}

// parseClaudeCurrentModel extracts the display name from a `Current model:
// Sonnet 5 (effort: high)` line, stripping any trailing parenthetical.
func parseClaudeCurrentModel(out []byte) string {
	_, rest, found := strings.Cut(string(out), "Current model:")
	if !found {
		return ""
	}
	if line, _, ok := strings.Cut(rest, "\n"); ok {
		rest = line
	}
	if name, _, ok := strings.Cut(rest, "("); ok {
		rest = name
	}
	return strings.TrimSpace(rest)
}

// claudeErrorReason renders the claude error envelope into one diagnostic line.
// The claude CLI reports a failed run only on stdout: the subtype names the
// class (error_max_turns, error_during_execution) and the `errors` array carries
// the human-readable detail. Neither is ever written to stderr, so without
// lifting them here the run surfaces as a bare non-zero exit with no explanation.
func claudeErrorReason(subtype string, errs []string) string {
	reason := "claude run error"
	if subtype != "" {
		reason += " (" + subtype + ")"
	}
	cleaned := make([]string, 0, len(errs))
	for _, e := range errs {
		if e = strings.TrimSpace(e); e != "" {
			cleaned = append(cleaned, e)
		}
	}
	if len(cleaned) > 0 {
		reason += ": " + strings.Join(cleaned, "; ")
	}
	return reason
}
