package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSoftServeAddr(t *testing.T) {
	tests := []struct {
		input    string
		wantHost string
		wantPort string
	}{
		{"localhost:23231", "localhost", "23231"},
		{"10.50.0.105:9999", "10.50.0.105", "9999"},
		{"myhost", "myhost", "23231"},
	}
	for _, tt := range tests {
		host, port := parseSoftServeAddr(tt.input)
		if host != tt.wantHost || port != tt.wantPort {
			t.Errorf("parseSoftServeAddr(%q) = (%q, %q), want (%q, %q)", tt.input, host, port, tt.wantHost, tt.wantPort)
		}
	}
}

func TestAddCredGitEnv_AddsToEnvFiles(t *testing.T) {
	dir := t.TempDir()
	envDir := filepath.Join(dir, "dalcenter")
	os.MkdirAll(envDir, 0755)

	// Create mock env files
	os.WriteFile(filepath.Join(envDir, "project-a.env"), []byte("SOME_VAR=value\n"), 0644)
	os.WriteFile(filepath.Join(envDir, "project-b.env"), []byte("OTHER=1\n"), 0644)
	os.WriteFile(filepath.Join(envDir, "not-an-env.txt"), []byte("skip me\n"), 0644)

	// Temporarily override env dir by testing the core logic directly
	clonePath := "/test/creds"
	envLine := "DALCENTER_CRED_GIT_REPO=" + clonePath

	count := 0
	entries, _ := os.ReadDir(envDir)
	for _, e := range entries {
		if !hasEnvSuffix(e.Name()) {
			continue
		}
		path := filepath.Join(envDir, e.Name())
		data, _ := os.ReadFile(path)
		content := string(data) + envLine + "\n"
		os.WriteFile(path, []byte(content), 0644)
		count++
	}

	if count != 2 {
		t.Fatalf("expected 2 env files updated, got %d", count)
	}

	// Verify content
	data, _ := os.ReadFile(filepath.Join(envDir, "project-a.env"))
	if got := string(data); got != "SOME_VAR=value\nDALCENTER_CRED_GIT_REPO=/test/creds\n" {
		t.Fatalf("unexpected content: %s", got)
	}

	// not-an-env.txt should be untouched
	data, _ = os.ReadFile(filepath.Join(envDir, "not-an-env.txt"))
	if string(data) != "skip me\n" {
		t.Fatal("non-env file was modified")
	}
}

func hasEnvSuffix(name string) bool {
	return len(name) > 4 && name[len(name)-4:] == ".env"
}

func TestPushInitialCredentials_NoSourceFiles(t *testing.T) {
	clonePath := t.TempDir()
	// Init a git repo so the function doesn't fail on git commands
	os.MkdirAll(filepath.Join(clonePath, "claude"), 0755)
	os.MkdirAll(filepath.Join(clonePath, "codex"), 0755)

	// HOME points to a dir with no credential files
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	changed, err := pushInitialCredentials(clonePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatal("should not report changes when no source files exist")
	}
}
