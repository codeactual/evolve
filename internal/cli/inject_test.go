// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/codeactual/evolve/internal/model"
)

// discovered builds the minimal model.Model the discover flow injects: bare
// vendor id and display name, no pricing, native-harness defaults.
func discovered(provider, id, name string) model.Model {
	return model.Model{
		ID: provider + "/" + id, ProviderID: provider, Name: name,
		Supported: map[string]string{nativeHarness[provider]: id},
		Preferred: nativeHarness[provider],
	}
}

// reload parses the config at dir the way the CLI would and returns the
// effective model set.
func reload(t *testing.T, dir string) []model.Model {
	t.Helper()
	o := &Options{Viper: viper.New(), Root: dir}
	path, err := FindConfigFile(dir)
	if err != nil || path == "" {
		t.Fatalf("FindConfigFile(%s) = %q, %v; want the injected config", dir, path, err)
	}
	if err := readConfigPath(o.Viper, path); err != nil {
		t.Fatalf("readConfigPath: %v", err)
	}
	models, err := o.AvailableModels()
	if err != nil {
		t.Fatalf("AvailableModels: %v", err)
	}
	return models
}

func TestInjectModelsCreatesConfigAndSeedsBuiltins(t *testing.T) {
	dir := t.TempDir()
	o := &Options{Viper: viper.New(), Root: dir}

	path, added, err := o.InjectModels([]model.Model{discovered("anthropic", "claude-sonnet-6", "Claude Sonnet 6")})
	if err != nil {
		t.Fatalf("InjectModels: %v", err)
	}
	if filepath.Base(path) != ".evolve.yaml" {
		t.Errorf("path = %q, want a fresh .evolve.yaml", path)
	}
	if len(added) != 1 || added[0] != "anthropic/claude-sonnet-6" {
		t.Errorf("added = %v", added)
	}

	// Overrides replace a provider's builtin list, so the injected file must
	// keep every builtin Anthropic model alongside the new one.
	models := reload(t, dir)
	for _, want := range []string{"anthropic/claude-sonnet-6", "anthropic/claude-sonnet-4-6", "anthropic/claude-opus-4-8", "anthropic/claude-fable-5"} {
		if _, ok := model.ByID(models, want); !ok {
			t.Errorf("effective registry missing %s after injection", want)
		}
	}
	// The seeded Sonnet 4.6 entry must keep its non-default display name and
	// pricing through the round trip.
	m, _ := model.ByID(models, "anthropic/claude-sonnet-4-6")
	if m.Name != "Claude Sonnet 4.6" {
		t.Errorf("seeded sonnet-4-6 lost its display name: %q", m.Name)
	}
	if m.InputUSD == nil || *m.InputUSD != 3.00 || m.OutputUSD == nil || *m.OutputUSD != 15.00 {
		t.Errorf("seeded sonnet-4-6 lost its pricing: %v/%v", m.InputUSD, m.OutputUSD)
	}
	// Other providers stay builtin: no stray override was written for them.
	if _, ok := model.ByID(models, "openai/gpt-5.5"); !ok {
		t.Error("non-injected providers must keep their builtin models")
	}
}

func TestInjectModelsAppendsWithoutSeedWhenListExists(t *testing.T) {
	dir := t.TempDir()
	seed := "# team config\nproviders:\n  anthropic:\n    models:\n      - id: claude-opus-4-8\n"
	if err := os.WriteFile(filepath.Join(dir, ".evolve.yaml"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &Options{Viper: viper.New(), Root: dir}

	_, added, err := o.InjectModels([]model.Model{discovered("anthropic", "claude-sonnet-6", "Claude Sonnet 6")})
	if err != nil {
		t.Fatalf("InjectModels: %v", err)
	}
	if len(added) != 1 {
		t.Fatalf("added = %v, want one", added)
	}

	out, err := os.ReadFile(filepath.Join(dir, ".evolve.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "# team config") {
		t.Errorf("comment lost:\n%s", text)
	}
	// An existing list is the user's chosen matrix — no builtin seeding.
	if strings.Contains(text, "claude-haiku-4-5") {
		t.Errorf("existing list must not be re-seeded with builtins:\n%s", text)
	}
	models := reload(t, dir)
	if _, ok := model.ByID(models, "anthropic/claude-sonnet-6"); !ok {
		t.Error("injected model missing from the effective registry")
	}
}

func TestInjectModelsDeduplicates(t *testing.T) {
	dir := t.TempDir()
	seed := "providers:\n  anthropic:\n    models:\n      - id: claude-sonnet-6\n"
	if err := os.WriteFile(filepath.Join(dir, ".evolve.yaml"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &Options{Viper: viper.New(), Root: dir}
	if _, _, err := o.InjectModels([]model.Model{discovered("anthropic", "claude-sonnet-6", "")}); err == nil {
		t.Error("want the nothing-to-add error when every selection is already listed")
	}
}

func TestInjectModelsMultipleProviders(t *testing.T) {
	dir := t.TempDir()
	o := &Options{Viper: viper.New(), Root: dir}
	_, added, err := o.InjectModels([]model.Model{
		discovered("anthropic", "claude-sonnet-6", "Claude Sonnet 6"),
		discovered("openai", "gpt-6", "GPT-6"),
	})
	if err != nil {
		t.Fatalf("InjectModels: %v", err)
	}
	if len(added) != 2 {
		t.Fatalf("added = %v, want two", added)
	}
	models := reload(t, dir)
	for _, want := range []string{"anthropic/claude-sonnet-6", "openai/gpt-6", "openai/gpt-5.5"} {
		if _, ok := model.ByID(models, want); !ok {
			t.Errorf("effective registry missing %s", want)
		}
	}
}

func TestEntryForDropsDefaults(t *testing.T) {
	e := entryFor(discovered("anthropic", "claude-sonnet-6", "claude-sonnet-6"))
	if e.Name != "" || e.Supported != nil || e.Preferred != "" {
		t.Errorf("defaults must be omitted, got %+v", e)
	}

	// A builtin Codex model: the native supported map and preferred harness are
	// defaults, but the display name and pricing are kept.
	gpt55, _ := model.ByID(model.AllModels(nil), "openai/gpt-5.5")
	e = entryFor(gpt55)
	if e.Supported != nil || e.Preferred != "" {
		t.Errorf("native supported/preferred must be omitted, got %+v", e)
	}
	if e.Name != "GPT-5.5" || e.InputUSD == nil || *e.InputUSD != 5.00 {
		t.Errorf("non-default name/pricing must be kept, got %+v", e)
	}

	// A CLI id that diverges from the vendor id is a non-default supported map.
	gpt55.Supported = map[string]string{model.HarnessCodex: "gpt-5.5-cli"}
	e = entryFor(gpt55)
	if e.Supported == nil || e.Supported[model.HarnessCodex] != "gpt-5.5-cli" {
		t.Errorf("non-default supported map must be kept, got %+v", e)
	}
}
