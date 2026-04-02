package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdInstanceName(t *testing.T) {
	tests := []struct {
		repoName string
		want     string
	}{
		{"dalcenter", "dalcenter@dalcenter"},
		{"my-project", "dalcenter@my-project"},
		{"", "dalcenter@"},
	}
	for _, tt := range tests {
		got := systemdInstanceName(tt.repoName)
		if got != tt.want {
			t.Errorf("systemdInstanceName(%q) = %q, want %q", tt.repoName, got, tt.want)
		}
	}
}

func TestEnvOrDefault(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envVal   string
		fallback string
		want     string
	}{
		{"env set", "TEST_ENV_OR_DEFAULT_SET", "from-env", "fallback", "from-env"},
		{"env empty", "TEST_ENV_OR_DEFAULT_EMPTY", "", "fallback", "fallback"},
		{"env unset", "TEST_ENV_OR_DEFAULT_UNSET_XYZ", "", "fallback", "fallback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal != "" {
				t.Setenv(tt.key, tt.envVal)
			}
			got := envOrDefault(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("envOrDefault(%q, %q) = %q, want %q", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestNextAvailablePort_Empty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)

	got := nextAvailablePort()
	if got != 11190 {
		t.Errorf("nextAvailablePort() = %d, want 11190 (base port)", got)
	}
}

func TestNextAvailablePort_SkipsUsed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)

	// Create env files with ports
	os.WriteFile(filepath.Join(dir, "team-a.env"), []byte("DALCENTER_PORT=11190\n"), 0644)
	os.WriteFile(filepath.Join(dir, "team-b.env"), []byte("DALCENTER_PORT=11191\n"), 0644)

	got := nextAvailablePort()
	if got != 11192 {
		t.Errorf("nextAvailablePort() = %d, want 11192", got)
	}
}

func TestNextAvailablePort_SkipsCommonEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)

	os.WriteFile(filepath.Join(dir, "common.env"), []byte("DALCENTER_PORT=11190\n"), 0644)

	got := nextAvailablePort()
	if got != 11190 {
		t.Errorf("nextAvailablePort() = %d, want 11190 (common.env should be skipped)", got)
	}
}

func TestNextAvailablePort_Gap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)

	os.WriteFile(filepath.Join(dir, "team-a.env"), []byte("DALCENTER_PORT=11190\n"), 0644)
	os.WriteFile(filepath.Join(dir, "team-c.env"), []byte("DALCENTER_PORT=11192\n"), 0644)

	got := nextAvailablePort()
	if got != 11191 {
		t.Errorf("nextAvailablePort() = %d, want 11191 (should fill gap)", got)
	}
}

func TestInjectTokensToService_AppendsTokens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)
	t.Setenv("GITHUB_TOKEN", "gh-test-token")
	t.Setenv("DALCENTER_TOKEN", "dc-test-token")
	t.Setenv("VEILKEY_LOCALVAULT_URL", "http://vault:8200")

	// Create initial env file
	envPath := filepath.Join(dir, "myrepo.env")
	os.WriteFile(envPath, []byte("DALCENTER_PORT=11190\n"), 0644)

	err := injectTokensToService("myrepo")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "GITHUB_TOKEN=gh-test-token") {
		t.Error("missing GITHUB_TOKEN")
	}
	if !strings.Contains(content, "DALCENTER_TOKEN=dc-test-token") {
		t.Error("missing DALCENTER_TOKEN")
	}
	if !strings.Contains(content, "VEILKEY_LOCALVAULT_URL=http://vault:8200") {
		t.Error("missing VEILKEY_LOCALVAULT_URL")
	}

	// Check permissions restricted to 0600
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("env file permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestInjectTokensToService_NoTokens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("DALCENTER_TOKEN", "")
	t.Setenv("VEILKEY_LOCALVAULT_URL", "")

	envPath := filepath.Join(dir, "myrepo.env")
	original := "DALCENTER_PORT=11190\n"
	os.WriteFile(envPath, []byte(original), 0644)

	err := injectTokensToService("myrepo")
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(envPath)
	if string(data) != original {
		t.Errorf("file should be unchanged when no tokens set, got %q", string(data))
	}
}

func TestInjectTokensToService_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)
	t.Setenv("GITHUB_TOKEN", "some-token")

	err := injectTokensToService("nonexistent")
	if err == nil {
		t.Error("expected error when env file does not exist")
	}
}
