// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// newTestCmd mirrors the root command's layout flag, which LoadConfig
// consults for explicit-flag precedence.
func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("layout", "auto", "")
	cmd.Flags().String("results-format", "", "")
	return cmd
}

// TestMain points the user-level config directory at an empty temp dir for the
// whole package, so a developer's real ~/.config/evolve can never leak into a
// test. Individual tests override XDG_CONFIG_HOME with t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "evolve-cli-test-xdg-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, "cleanup:", err)
	}
	os.Exit(code)
}

// userConfigDir points XDG_CONFIG_HOME at a fresh temp dir and returns the
// evolve directory inside it, where the user-level config file lives.
func userConfigDir(t *testing.T) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "evolve")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigFormats(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{".evolve.yaml", "layout: marketplace\nchecks:\n  max_skill_lines: 200\n"},
		{".evolve.yml", "layout: marketplace\nchecks:\n  max_skill_lines: 200\n"},
		{".evolve.json", `{"layout": "marketplace", "checks": {"max_skill_lines": 200}}`},
		{".evolve.jsonc", `{
			// comments survive standardization
			"layout": "marketplace",
			"checks": {
				"max_skill_lines": 200, // trailing commas too
			},
		}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, tc.name, tc.content)
			o := &Options{Viper: viper.New(), Root: dir, Layout: "auto"}
			if err := o.LoadConfig(newTestCmd()); err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if o.Layout != "marketplace" {
				t.Errorf("Layout = %q, want marketplace", o.Layout)
			}
			if got := o.Viper.GetInt("checks.max_skill_lines"); got != 200 {
				t.Errorf("checks.max_skill_lines = %d, want 200", got)
			}
			if got := o.ConfigFileName(); got != tc.name {
				t.Errorf("ConfigFileName = %q, want %q", got, tc.name)
			}
		})
	}
}

func TestLoadConfigMissingIsOptional(t *testing.T) {
	o := &Options{Viper: viper.New(), Root: t.TempDir(), Layout: "auto"}
	if err := o.LoadConfig(newTestCmd()); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if o.Layout != "auto" {
		t.Errorf("Layout = %q, want auto", o.Layout)
	}
	if got := o.ConfigFileName(); got != "" {
		t.Errorf("ConfigFileName = %q, want empty", got)
	}
}

func TestLoadConfigAmbiguous(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".evolve.yaml", "layout: multi\n")
	writeFile(t, dir, ".evolve.json", `{"layout": "single"}`)
	o := &Options{Viper: viper.New(), Root: dir, Layout: "auto"}
	err := o.LoadConfig(newTestCmd())
	if err == nil || !strings.Contains(err.Error(), "ambiguous config") {
		t.Fatalf("LoadConfig error = %v, want ambiguous config", err)
	}
}

// TestLoadConfigIgnoresTOML pins the clean break: .evolve.toml is no longer
// a recognized config file.
func TestLoadConfigIgnoresTOML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".evolve.toml", "layout = \"marketplace\"\n")
	o := &Options{Viper: viper.New(), Root: dir, Layout: "auto"}
	if err := o.LoadConfig(newTestCmd()); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if o.Layout != "auto" || o.ConfigFileName() != "" {
		t.Errorf("toml config was loaded: layout=%q file=%q", o.Layout, o.ConfigFileName())
	}
}

func TestLoadConfigResultsFormat(t *testing.T) {
	dir := t.TempDir()
	o := &Options{Viper: viper.New(), Root: dir, Layout: "auto"}
	if err := o.LoadConfig(newTestCmd()); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if o.ResultsFormat != "json" {
		t.Errorf("default ResultsFormat = %q, want json", o.ResultsFormat)
	}

	writeFile(t, dir, ".evolve.yaml", "results_format: yml\n")
	o = &Options{Viper: viper.New(), Root: dir, Layout: "auto"}
	if err := o.LoadConfig(newTestCmd()); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if o.ResultsFormat != "yaml" {
		t.Errorf("ResultsFormat = %q, want yaml (yml canonicalized)", o.ResultsFormat)
	}

	// An explicit flag beats the config file.
	cmd := newTestCmd()
	if err := cmd.Flags().Set("results-format", "jsonc"); err != nil {
		t.Fatal(err)
	}
	o = &Options{Viper: viper.New(), Root: dir, Layout: "auto", ResultsFormat: "jsonc"}
	if err := o.LoadConfig(cmd); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if o.ResultsFormat != "jsonc" {
		t.Errorf("ResultsFormat = %q, want jsonc (explicit flag)", o.ResultsFormat)
	}

	o = &Options{Viper: viper.New(), Root: t.TempDir(), Layout: "auto", ResultsFormat: "toml"}
	if err := o.LoadConfig(newTestCmd()); err == nil {
		t.Error("LoadConfig: want error for unknown results format")
	}
}

func TestLoadConfigExplicitFlagWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".evolve.yaml", "layout: marketplace\n")
	cmd := newTestCmd()
	if err := cmd.Flags().Set("layout", "single"); err != nil {
		t.Fatal(err)
	}
	o := &Options{Viper: viper.New(), Root: dir, Layout: "single"}
	if err := o.LoadConfig(cmd); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if o.Layout != "single" {
		t.Errorf("Layout = %q, want single (explicit flag)", o.Layout)
	}
}

func TestLoadConfigInvalidJSONC(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".evolve.jsonc", "{ not valid")
	o := &Options{Viper: viper.New(), Root: dir, Layout: "auto"}
	if err := o.LoadConfig(newTestCmd()); err == nil {
		t.Fatal("LoadConfig: want error for invalid jsonc")
	}
}

// loadWithRepoConfig loads a repository .evolve.yaml with the given body and
// returns LoadConfig's error.
func loadWithRepoConfig(t *testing.T, body string) error {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, ".evolve.yaml", body)
	o := &Options{Viper: viper.New(), Root: dir, Layout: "auto"}
	return o.LoadConfig(newTestCmd())
}

func TestLoadConfigRejectsOperatorOnlyKeyInRepoConfig(t *testing.T) {
	xdg := userConfigDir(t)
	cases := []struct {
		name string
		body string
		want []string // substrings the error must carry
	}{
		{
			"sandbox.enabled", "sandbox:\n  enabled: false\n",
			[]string{"sandbox.enabled", "EVOLVE_SANDBOX_ENABLED", "--no-sandbox"},
		},
		{
			"sandbox.read_paths", "sandbox:\n  read_paths: [/etc/ssl]\n",
			[]string{"sandbox.read_paths", "EVOLVE_SANDBOX_READ_PATHS"},
		},
		{
			"cache_dir", "cache_dir: /tmp/somewhere\n",
			[]string{"cache_dir", "EVOLVE_CACHE_DIR"},
		},
		{
			"telemetry.dir", "telemetry:\n  dir: /tmp/otel\n",
			[]string{"telemetry.dir", "EVOLVE_TELEMETRY_DIR", "--telemetry-dir"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadWithRepoConfig(t, tc.body)
			if err == nil {
				t.Fatal("LoadConfig: want an error for an operator-only key in a repository config")
			}
			for _, want := range append(tc.want, filepath.Join(xdg, "config.")) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

func TestLoadConfigRejectsRemovedProtectedRoots(t *testing.T) {
	xdg := userConfigDir(t)
	writeFile(t, xdg, "config.yaml", "sandbox:\n  protected_roots: [/home/u/Repos]\n")
	o := &Options{Viper: viper.New(), Root: t.TempDir(), Layout: "auto"}
	err := o.LoadConfig(newTestCmd())
	if err == nil {
		t.Fatal("LoadConfig: want an error for the removed sandbox.protected_roots key")
	}
	for _, want := range []string{"sandbox.protected_roots", "sandbox.read_paths", "sandbox.write_paths"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestLoadConfigHonorsUserLevelOperatorKeys(t *testing.T) {
	xdg := userConfigDir(t)
	writeFile(t, xdg, "config.yaml", "sandbox:\n  enabled: false\ncache_dir: /var/cache/evolve\n")
	o := &Options{Viper: viper.New(), Root: t.TempDir(), Layout: "auto"}
	if err := o.LoadConfig(newTestCmd()); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if o.Viper.GetBool("sandbox.enabled") || !o.Viper.IsSet("sandbox.enabled") {
		t.Error("sandbox.enabled=false in the user-level config was not honored")
	}
	if got := o.Viper.GetString("cache_dir"); got != "/var/cache/evolve" {
		t.Errorf("cache_dir = %q, want the user-level value", got)
	}
}

func TestLoadConfigRepoOverridesUserLevel(t *testing.T) {
	xdg := userConfigDir(t)
	writeFile(t, xdg, "config.yaml", "max_turns: 7\nlayout: multi\njudge_model: user-judge\n")
	dir := t.TempDir()
	writeFile(t, dir, ".evolve.yaml", "max_turns: 9\nlayout: marketplace\n")

	o := &Options{Viper: viper.New(), Root: dir, Layout: "auto"}
	if err := o.LoadConfig(newTestCmd()); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := o.Viper.GetInt("max_turns"); got != 9 {
		t.Errorf("max_turns = %d, want the repository's 9 over the user-level 7", got)
	}
	if got := o.Viper.GetString("judge_model"); got != "user-judge" {
		t.Errorf("judge_model = %q, want the user-level value (repository leaves it unset)", got)
	}
	if o.Layout != "marketplace" {
		t.Errorf("Layout = %q, want marketplace (repository over user-level)", o.Layout)
	}

	// An explicit flag beats both files.
	cmd := newTestCmd()
	if err := cmd.Flags().Set("layout", "single"); err != nil {
		t.Fatal(err)
	}
	o = &Options{Viper: viper.New(), Root: dir, Layout: "single"}
	if err := o.LoadConfig(cmd); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if o.Layout != "single" {
		t.Errorf("Layout = %q, want single (explicit flag)", o.Layout)
	}
}

func TestLoadConfigUserLevelAmbiguous(t *testing.T) {
	xdg := userConfigDir(t)
	writeFile(t, xdg, "config.yaml", "layout: multi\n")
	writeFile(t, xdg, "config.json", `{"layout": "single"}`)
	o := &Options{Viper: viper.New(), Root: t.TempDir(), Layout: "auto"}
	err := o.LoadConfig(newTestCmd())
	if err == nil || !strings.Contains(err.Error(), "ambiguous config") {
		t.Fatalf("LoadConfig error = %v, want ambiguous config", err)
	}
}

func TestLoadConfigEnvBeatsConfigFiles(t *testing.T) {
	xdg := userConfigDir(t)
	writeFile(t, xdg, "config.yaml", "sandbox:\n  enabled: false\n")
	t.Setenv("EVOLVE_SANDBOX_ENABLED", "true")
	o := &Options{Viper: viper.New(), Root: t.TempDir(), Layout: "auto"}
	if err := o.LoadConfig(newTestCmd()); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !o.Viper.GetBool("sandbox.enabled") {
		t.Error("EVOLVE_SANDBOX_ENABLED should beat the user-level config file")
	}
}
