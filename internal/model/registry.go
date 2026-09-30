// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package model

import "slices"

// Provider ids.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// Harness ids, referenced by the Supported maps below. The harness package
// owns the Harness implementations; these constants are the shared vocabulary.
const (
	HarnessClaude = "claude"
	HarnessCodex  = "codex"
)

// Providers returns the model vendors in display order.
func Providers() []Provider {
	return []Provider{
		{ID: ProviderAnthropic, Name: "Anthropic"},
		{ID: ProviderOpenAI, Name: "OpenAI"},
	}
}

// builtins returns the canonical model registry: one entry per vendor model,
// each declaring which harnesses can drive it and the CLI-specific id each
// harness uses.
func builtins() []Model {
	return []Model{
		// Anthropic — driven by Claude Code.
		{
			ID: "anthropic/claude-haiku-4-5", ProviderID: ProviderAnthropic, Name: "Claude Haiku 4.5",
			InputUSD: usd(1.00), OutputUSD: usd(5.00),
			Supported: map[string]string{HarnessClaude: "claude-haiku-4-5"},
			Preferred: HarnessClaude,
		},
		{
			ID: "anthropic/claude-sonnet-4-6", ProviderID: ProviderAnthropic, Name: "Claude Sonnet 4.6",
			InputUSD: usd(3.00), OutputUSD: usd(15.00),
			Supported: map[string]string{HarnessClaude: "claude-sonnet-4-6"},
			Preferred: HarnessClaude,
		},
		{
			// Sticker rate; an introductory $2/$10 per MTok applies through 2026-08-31.
			ID: "anthropic/claude-sonnet-5", ProviderID: ProviderAnthropic, Name: "Claude Sonnet 5",
			InputUSD: usd(3.00), OutputUSD: usd(15.00),
			Supported: map[string]string{HarnessClaude: "claude-sonnet-5"},
			Preferred: HarnessClaude,
		},
		{
			ID: "anthropic/claude-opus-4-8", ProviderID: ProviderAnthropic, Name: "Claude Opus 4.8",
			InputUSD: usd(5.00), OutputUSD: usd(25.00),
			Supported: map[string]string{HarnessClaude: "claude-opus-4-8"},
			Preferred: HarnessClaude,
		},
		{
			ID: "anthropic/claude-opus-5", ProviderID: ProviderAnthropic, Name: "Claude Opus 5",
			InputUSD: usd(5.00), OutputUSD: usd(25.00),
			Supported: map[string]string{HarnessClaude: "claude-opus-5"},
			Preferred: HarnessClaude,
		},
		{
			ID: "anthropic/claude-fable-5", ProviderID: ProviderAnthropic, Name: "Claude Fable 5",
			InputUSD: usd(10.00), OutputUSD: usd(50.00),
			Supported: map[string]string{HarnessClaude: "claude-fable-5"},
			Preferred: HarnessClaude,
		},

		// OpenAI — driven by Codex. The Spark id carries no published per-token
		// pricing, so estimate/measured render n/a.
		{
			ID: "openai/gpt-5.6-sol", ProviderID: ProviderOpenAI, Name: "GPT-5.6 Sol",
			InputUSD: usd(5.00), OutputUSD: usd(30.00),
			Supported: map[string]string{HarnessCodex: "gpt-5.6-sol"},
			Preferred: HarnessCodex,
		},
		{
			ID: "openai/gpt-5.6-terra", ProviderID: ProviderOpenAI, Name: "GPT-5.6 Terra",
			InputUSD: usd(2.50), OutputUSD: usd(15.00),
			Supported: map[string]string{HarnessCodex: "gpt-5.6-terra"},
			Preferred: HarnessCodex,
		},
		{
			ID: "openai/gpt-5.6-luna", ProviderID: ProviderOpenAI, Name: "GPT-5.6 Luna",
			InputUSD: usd(1.00), OutputUSD: usd(6.00),
			Supported: map[string]string{HarnessCodex: "gpt-5.6-luna"},
			Preferred: HarnessCodex,
		},
		{
			ID: "openai/gpt-5.3-codex-spark", ProviderID: ProviderOpenAI, Name: "GPT-5.3 Codex Spark",
			Supported: map[string]string{HarnessCodex: "gpt-5.3-codex-spark"},
			Preferred: HarnessCodex,
		},
		{
			ID: "openai/gpt-5.4-mini", ProviderID: ProviderOpenAI, Name: "GPT-5.4 Mini",
			InputUSD: usd(0.75), OutputUSD: usd(4.50),
			Supported: map[string]string{HarnessCodex: "gpt-5.4-mini"},
			Preferred: HarnessCodex,
		},
		{
			ID: "openai/gpt-5.4", ProviderID: ProviderOpenAI, Name: "GPT-5.4",
			InputUSD: usd(2.50), OutputUSD: usd(15.00),
			Supported: map[string]string{HarnessCodex: "gpt-5.4"},
			Preferred: HarnessCodex,
		},
		{
			ID: "openai/gpt-5.5", ProviderID: ProviderOpenAI, Name: "GPT-5.5",
			InputUSD: usd(5.00), OutputUSD: usd(30.00),
			Supported: map[string]string{HarnessCodex: "gpt-5.5"},
			Preferred: HarnessCodex,
		},
	}
}

// AllModels returns the canonical model registry with any per-provider config
// override applied. overrides maps a provider id to a replacement model list
// (replace, not merge — partial merges create "which price won?" ambiguity);
// only models whose ProviderID is overridden are replaced.
func AllModels(overrides map[string][]Model) []Model {
	if len(overrides) == 0 {
		return builtins()
	}
	var out []Model
	for _, m := range builtins() {
		if _, ok := overrides[m.ProviderID]; ok {
			continue // replaced below
		}
		out = append(out, m)
	}
	for _, p := range Providers() {
		if models, ok := overrides[p.ID]; ok {
			out = append(out, models...)
		}
	}
	return out
}

// ModelByID returns the model with the given canonical id from models, if any.
func ModelByID(models []Model, id string) (Model, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// ProviderByID returns the vendor with the given id, if any.
func ProviderByID(id string) (Provider, bool) {
	for _, p := range Providers() {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// providerIDs is the set of known vendor ids, used to validate override keys.
func providerIDs() []string {
	ids := make([]string, 0, len(Providers()))
	for _, p := range Providers() {
		ids = append(ids, p.ID)
	}
	return ids
}

// IsProviderID reports whether id names a known vendor.
func IsProviderID(id string) bool { return slices.Contains(providerIDs(), id) }
