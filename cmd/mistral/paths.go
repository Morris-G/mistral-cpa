package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// legacyConfigPath is where the official CPA Docker image keeps its config.
const legacyConfigPath = "/CLIProxyAPI/config.yaml"

// configPathEnv overrides every other lookup when set.
const configPathEnv = "MISTRAL_CPA_CONFIG"

var (
	// Swappable in tests.
	processArgs    = func() []string { return os.Args }
	workingDir     = os.Getwd
	executablePath = os.Executable
	lookupEnv      = os.LookupEnv
)

// resolveConfigPath finds the CPA config file. A path set on the console
// (config-path in the plugin block) always wins, even if it is missing, so the
// error names the path the user chose. Otherwise, since the plugin is loaded
// into the CPA process, os.Args and the working directory are CPA's own:
//  1. $MISTRAL_CPA_CONFIG
//  2. CPA's -config / --config flag
//  3. config.yaml in the working directory (CPA's own default)
//  4. config.yaml next to the CPA executable
//  5. /CLIProxyAPI/config.yaml (Docker image)
//
// The first candidate that exists wins. When none exists, the highest-priority
// candidate is returned so the error names a meaningful path.
func resolveConfigPath(custom string) string {
	if custom = strings.TrimSpace(custom); custom != "" {
		return filepath.Clean(custom)
	}
	candidates := configCandidates()
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return candidates[0]
}

func configCandidates() []string {
	var out []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		// "/x" is not IsAbs on Windows, but it is a rooted path, not a
		// relative one, so leave it alone.
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, `\`) {
			if wd, err := workingDir(); err == nil {
				path = filepath.Join(wd, path)
			}
		}
		path = filepath.Clean(path)
		for _, existing := range out {
			if existing == path {
				return
			}
		}
		out = append(out, path)
	}
	if value, ok := lookupEnv(configPathEnv); ok {
		add(value)
	}
	add(configFlag(processArgs()))
	if wd, err := workingDir(); err == nil {
		add(filepath.Join(wd, "config.yaml"))
	}
	if exe, err := executablePath(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		add(filepath.Join(filepath.Dir(exe), "config.yaml"))
	}
	add(legacyConfigPath)
	return out
}

// configFlag extracts the value of -config / --config from the CPA command line.
func configFlag(args []string) string {
	if len(args) <= 1 {
		return ""
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		name := strings.TrimLeft(arg, "-")
		if name == arg || len(arg)-len(name) > 2 {
			continue
		}
		if value, ok := strings.CutPrefix(name, "config="); ok {
			return value
		}
		if name == "config" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// validateConfigPath checks a path entered on the console before it is saved:
// it must be absolute, exist, and already contain the mistral plugin block.
// An empty path means auto-detect and is always valid.
func validateConfigPath(path string) error {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) && !strings.HasPrefix(path, "/") {
		return fmt.Errorf("config path must be absolute: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}
	if _, err := replacePluginBlock(string(raw), renderPluginBlock(defaultSettings())); err != nil {
		return fmt.Errorf("%s has no plugins.configs.%s block", path, pluginConfigKey)
	}
	return nil
}

// requestLogPath keeps logs under <config dir>/plugin-data/mistral, which is
// /CLIProxyAPI/plugin-data/mistral in the Docker image as before. It follows
// the config path chosen on the console.
func requestLogPath() string {
	return filepath.Join(filepath.Dir(resolveConfigPath(loadSettings().ConfigPath)), "plugin-data", pluginConfigKey, "requests.jsonl")
}
