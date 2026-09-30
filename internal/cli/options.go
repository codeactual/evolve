// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tailscale/hujson"

	"github.com/codeactual/evolve/internal/encfmt"
)

// ErrFailures signals that checks or evals ran to completion and at least one
// failed: exit 1, distinct from usage/config errors (exit 2). The run
// subcommands return it only under --strict; by default they warn and exit 0.
// `report --check` returns it unconditionally.
var ErrFailures = errors.New("failures reported")

// ConfigExtensions lists the accepted config-file extensions, in search
// order: the file is .evolve.<ext> at the repository root.
var ConfigExtensions = []string{"yaml", "yml", "json", "jsonc"}

// userConfigBase is the base name of the user-level config file:
// os.UserConfigDir()/evolve/config.<ext>, with the same extensions as the
// repository's .evolve.<ext>.
const userConfigBase = "config"

// operatorOnlyKeys are the top-level config keys only the operator may set: a
// repository under test is untrusted, so its .evolve.<ext> must not steer the
// sandbox or the host-side paths evolve itself writes to. Everything beneath
// each key is operator-only too. They come from flags, EVOLVE_* env vars, or
// the user-level config file.
var operatorOnlyKeys = []string{"sandbox", "cache_dir", "telemetry"}

// operatorKeyFlags names the flag that sets an operator-only key, where one
// exists, for the rejection message.
var operatorKeyFlags = map[string]string{
	"sandbox.enabled": "--no-sandbox (the inverse)",
	"telemetry.dir":   "--telemetry-dir",
}

// Options carries the resolved global state every subcommand consumes.
type Options struct {
	Log   *slog.Logger
	Viper *viper.Viper

	Root          string // --root: repository to operate on ("" = walk up from cwd)
	Layout        string // --layout: auto|marketplace|multi|single
	JSON          bool   // --json: machine-readable JSONL progress on stdout
	ResultsFormat string // --results-format: json|jsonc|yaml for results + EVALUATION rollup
	TelemetryDir  string // --telemetry-dir: directory for the OTEL JSON exporter ("" = telemetry disabled)
}

// LoadConfig reads the optional user-level config file and the optional
// .evolve.<ext> from the resolved repository root, layering env (EVOLVE_*) and
// flags above them: flags > env > repository config > user-level config.
// Flags that the user set explicitly keep precedence because they are bound
// after the files load.
//
// The repository under test is untrusted, so its config is loaded into its own
// viper first and rejected when it sets an operator-only key (see
// operatorOnlyKeys); only then is it merged over the user-level config. The
// removed sandbox.protected_roots key is an error in either file.
func (o *Options) LoadConfig(cmd *cobra.Command) error {
	v := o.Viper
	v.SetEnvPrefix("EVOLVE")
	// Dotted keys (telemetry.dir) bind to underscore env vars (EVOLVE_TELEMETRY_DIR);
	// flat keys are unaffected since they hold no dots.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	dir := o.Root
	if dir == "" {
		dir = "."
	}
	userDir := UserConfigDir()
	userV, _, err := loadConfigFile(userDir, userConfigBase)
	if err != nil {
		return err
	}
	repoV, repoPath, err := loadConfigFile(dir, ".evolve")
	if err != nil {
		return err
	}
	if repoV != nil {
		if err := rejectOperatorOnly(repoV, repoPath, userDir); err != nil {
			return err
		}
		v.SetConfigFile(repoPath) // provenance only; nothing is reloaded from it
	}
	for _, loaded := range []*viper.Viper{userV, repoV} {
		if loaded != nil {
			if err := v.MergeConfigMap(loaded.AllSettings()); err != nil {
				return fmt.Errorf("merge config: %w", err)
			}
		}
	}

	if !cmd.Flags().Changed("layout") {
		if l := v.GetString("layout"); l != "" {
			o.Layout = l
		}
	}
	if !cmd.Flags().Changed("results-format") {
		if f := v.GetString("results_format"); f != "" {
			o.ResultsFormat = f
		}
	}
	if !cmd.Flags().Changed("telemetry-dir") {
		if d := v.GetString("telemetry.dir"); d != "" {
			o.TelemetryDir = d
		}
	}
	o.ResultsFormat = encfmt.Canonical(o.ResultsFormat)
	if o.ResultsFormat == "" {
		o.ResultsFormat = "json"
	}
	if !slices.Contains(encfmt.Formats, o.ResultsFormat) {
		return fmt.Errorf("unknown results format %q (want json, jsonc, or yaml)", o.ResultsFormat)
	}
	return nil
}

// ConfigFileName names the loaded config file for provenance output, or ""
// when no config file was found.
func (o *Options) ConfigFileName() string {
	if path := o.Viper.ConfigFileUsed(); path != "" {
		return filepath.Base(path)
	}
	return ""
}

// UserConfigDir is the directory holding the user-level config file
// (os.UserConfigDir()/evolve), or "" when the user config directory is unknown
// (neither XDG_CONFIG_HOME nor HOME is set).
func UserConfigDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "evolve")
}

// FindConfigFile locates the single .evolve.<ext> in dir: "" when none
// exists, an error listing every candidate when several do.
func FindConfigFile(dir string) (string, error) {
	return findConfigFile(dir, ".evolve")
}

// findConfigFile locates the single <base>.<ext> in dir.
func findConfigFile(dir, base string) (string, error) {
	var found []string
	for _, ext := range ConfigExtensions {
		path := filepath.Join(dir, base+"."+ext)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			found = append(found, path)
		}
	}
	switch len(found) {
	case 0:
		return "", nil // config is optional
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("ambiguous config: found %s; keep exactly one", strings.Join(found, ", "))
}

// loadConfigFile finds and loads the single <base>.<ext> in dir into its own
// viper. It returns a nil viper (and no error) when dir is empty or holds no
// such file. More than one file is ambiguous and rejected rather than silently
// prioritized; the removed sandbox.protected_roots key is rejected in any file.
func loadConfigFile(dir, base string) (*viper.Viper, string, error) {
	if dir == "" {
		return nil, "", nil
	}
	path, err := findConfigFile(dir, base)
	if err != nil || path == "" {
		return nil, "", err
	}
	v := viper.New()
	if err := readConfigPath(v, path); err != nil {
		return nil, "", err
	}
	if v.IsSet("sandbox.protected_roots") {
		return nil, "", fmt.Errorf("config: %s sets sandbox.protected_roots, which was removed: the sandbox is now "+
			"deny-by-default; grant paths with sandbox.read_paths and sandbox.write_paths in the user-level config instead", path)
	}
	return v, path, nil
}

// rejectOperatorOnly fails when the repository config sets an operator-only key,
// naming each offending key with the operator-side ways to set it.
func rejectOperatorOnly(v *viper.Viper, path, userDir string) error {
	var offenders []string
	seen := map[string]bool{}
	for _, key := range v.AllKeys() {
		if top, _, _ := strings.Cut(key, "."); slices.Contains(operatorOnlyKeys, top) {
			offenders = append(offenders, key)
			seen[top] = true
		}
	}
	for _, top := range operatorOnlyKeys {
		if !seen[top] && v.InConfig(top) { // e.g. an empty `sandbox: {}` block
			offenders = append(offenders, top)
		}
	}
	if len(offenders) == 0 {
		return nil
	}
	sort.Strings(offenders)
	userFile := "the user-level config file (neither XDG_CONFIG_HOME nor HOME is set)"
	if userDir != "" {
		userFile = "the user-level config " + filepath.Join(userDir, "config.<ext>")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "config: %s is a repository config (untrusted) and may not set operator-only keys:", path)
	for _, key := range offenders {
		fmt.Fprintf(&b, "\n  %s: set it in %s, or with %s", key, userFile,
			"EVOLVE_"+strings.ToUpper(strings.ReplaceAll(key, ".", "_")))
		if flag := operatorKeyFlags[key]; flag != "" {
			fmt.Fprintf(&b, ", or with the %s flag", flag)
		}
	}
	return errors.New(b.String())
}

// readConfigPath loads the config file at path into v. JSONC is standardized to
// plain JSON before it reaches viper, which parses the other formats natively.
func readConfigPath(v *viper.Viper, path string) error {
	if strings.HasSuffix(path, ".jsonc") {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read config %s: %w", path, err)
		}
		std, err := hujson.Standardize(raw)
		if err != nil {
			return fmt.Errorf("parse config %s: %w", path, err)
		}
		v.SetConfigFile(path) // provenance only; the type below wins
		v.SetConfigType("json")
		if err := v.ReadConfig(bytes.NewReader(std)); err != nil {
			return fmt.Errorf("parse config %s: %w", path, err)
		}
		return nil
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	return nil
}
