package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestRunInitCreatesFiles(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := runInit(); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	env, err := os.ReadFile(initEnvFile)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	key := extractKey(t, string(env))
	if len(key) < 40 {
		t.Errorf("generated key looks too short: %q", key)
	}
	if key == "%s" || strings.Contains(key, "%") {
		t.Errorf("key placeholder not substituted: %q", key)
	}

	// .env holds a secret and should not be world-readable.
	info, err := os.Stat(initEnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf(".env perms = %o, want 600", info.Mode().Perm())
	}

	if _, err := os.Stat(initConfigFile); err != nil {
		t.Errorf("config not created: %v", err)
	}

	// Two runs must produce different keys (crypto/rand, not a fixed seed).
	t.Chdir(t.TempDir())
	if err := runInit(); err != nil {
		t.Fatal(err)
	}
	env2, _ := os.ReadFile(initEnvFile)
	if extractKey(t, string(env2)) == key {
		t.Errorf("two inits produced the same key")
	}
}

func TestRunInitRefusesToOverwrite(t *testing.T) {
	t.Chdir(t.TempDir())

	// Pre-existing config.yaml should block init entirely, leaving .env absent.
	if err := os.WriteFile(initConfigFile, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runInit()
	if err == nil {
		t.Fatal("expected init to fail when a target file exists")
	}
	if !strings.Contains(err.Error(), initConfigFile) {
		t.Errorf("error should name the existing file, got: %v", err)
	}
	if _, statErr := os.Stat(initEnvFile); !os.IsNotExist(statErr) {
		t.Errorf(".env should not have been created when init refused")
	}
	// The existing file must be untouched.
	if data, _ := os.ReadFile(initConfigFile); string(data) != "existing" {
		t.Errorf("existing config was modified: %q", data)
	}
}

func extractKey(t *testing.T, env string) string {
	t.Helper()
	for _, line := range strings.Split(env, "\n") {
		if v, ok := strings.CutPrefix(line, "EXPORT_HASH_KEY="); ok {
			return v
		}
	}
	t.Fatalf("EXPORT_HASH_KEY not found in .env:\n%s", env)
	return ""
}
