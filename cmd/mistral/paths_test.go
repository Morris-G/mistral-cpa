package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigFlag(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"cpa"}, ""},
		{[]string{"cpa", "-config", "/opt/cpa/config.yaml"}, "/opt/cpa/config.yaml"},
		{[]string{"cpa", "--config", "a.yaml"}, "a.yaml"},
		{[]string{"cpa", "-config=b.yaml"}, "b.yaml"},
		{[]string{"cpa", "--config=c.yaml", "-other"}, "c.yaml"},
		{[]string{"cpa", "-login"}, ""},
		{[]string{"cpa", "---config", "x"}, ""},
		{[]string{"cpa", "--", "-config", "x"}, ""},
	}
	for _, tc := range cases {
		if got := configFlag(tc.args); got != tc.want {
			t.Errorf("configFlag(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func withPathEnv(t *testing.T, args []string, wd, exe string, env map[string]string) {
	t.Helper()
	oldArgs, oldWd, oldExe, oldEnv := processArgs, workingDir, executablePath, lookupEnv
	t.Cleanup(func() { processArgs, workingDir, executablePath, lookupEnv = oldArgs, oldWd, oldExe, oldEnv })
	processArgs = func() []string { return args }
	workingDir = func() (string, error) { return wd, nil }
	executablePath = func() (string, error) { return exe, nil }
	lookupEnv = func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("plugins:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveConfigPathPrefersFlag(t *testing.T) {
	root := t.TempDir()
	flagged := filepath.Join(root, "custom", "cpa.yaml")
	writeFile(t, flagged)
	writeFile(t, filepath.Join(root, "config.yaml"))
	withPathEnv(t, []string{"cpa", "-config", "custom/cpa.yaml"}, root, filepath.Join(root, "bin", "cpa"), nil)
	if got := resolveConfigPath(""); got != flagged {
		t.Fatalf("got %q, want %q", got, flagged)
	}
}

func TestResolveConfigPathEnvWins(t *testing.T) {
	root := t.TempDir()
	envPath := filepath.Join(root, "env.yaml")
	writeFile(t, envPath)
	writeFile(t, filepath.Join(root, "config.yaml"))
	withPathEnv(t, []string{"cpa"}, root, filepath.Join(root, "cpa"), map[string]string{configPathEnv: envPath})
	if got := resolveConfigPath(""); got != envPath {
		t.Fatalf("got %q, want %q", got, envPath)
	}
}

func TestResolveConfigPathFallsBackToExecutableDir(t *testing.T) {
	root := t.TempDir()
	exeDir := filepath.Join(root, "app")
	want := filepath.Join(exeDir, "config.yaml")
	writeFile(t, want)
	withPathEnv(t, []string{"cpa"}, filepath.Join(root, "elsewhere"), filepath.Join(exeDir, "cpa"), nil)
	if got := resolveConfigPath(""); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveConfigPathMissingReturnsFirstCandidate(t *testing.T) {
	root := t.TempDir()
	withPathEnv(t, []string{"cpa"}, root, filepath.Join(root, "bin", "cpa"), nil)
	want := filepath.Join(root, "config.yaml")
	if got := resolveConfigPath(""); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveConfigPathCustomWins(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config.yaml"))
	custom := filepath.Join(root, "mine", "cpa.yaml")
	withPathEnv(t, []string{"cpa"}, root, filepath.Join(root, "cpa"), nil)
	if got := resolveConfigPath(custom); got != custom {
		t.Fatalf("got %q, want %q", got, custom)
	}
}

func TestConfigPathRoundTripsThroughPluginBlock(t *testing.T) {
	for _, path := range []string{`C:\Users\me\CLIProxyAPI\config.yaml`, "/opt/cpa/config.yaml", `D:\a "b"\c.yaml`} {
		s := defaultSettings()
		s.ConfigPath = path
		block := renderPluginBlock(s)
		// CPA hands the plugin its own block with the key line stripped.
		var body []string
		for _, line := range strings.Split(block, "\n")[1:] {
			body = append(body, strings.TrimPrefix(line, "      "))
		}
		if got := parseSettings([]byte(strings.Join(body, "\n"))).ConfigPath; got != path {
			t.Errorf("round trip %q -> %q\n%s", path, got, block)
		}
	}
}

func TestValidateConfigPath(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good.yaml")
	if err := os.WriteFile(good, []byte("plugins:\n  configs:\n    mistral:\n      enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "bare.yaml")
	writeFile(t, bare)
	if err := validateConfigPath(""); err != nil {
		t.Errorf("empty path: %v", err)
	}
	if err := validateConfigPath(good); err != nil {
		t.Errorf("good path: %v", err)
	}
	for _, bad := range []string{"relative/config.yaml", filepath.Join(root, "missing.yaml"), bare} {
		if err := validateConfigPath(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

// TestMain points config lookup at a temp dir so request logs written by
// executor tests do not land in the source tree.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mistral-test-")
	if err != nil {
		panic(err)
	}
	config := filepath.Join(dir, "config.yaml")
	lookupEnv = func(key string) (string, bool) {
		if key == configPathEnv {
			return config, true
		}
		return os.LookupEnv(key)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
