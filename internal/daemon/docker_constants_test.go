package daemon

import (
	"strings"
	"testing"
)

// ── Constants sanity checks ─────────────────────────────────────

func TestContainerPrefix(t *testing.T) {
	if containerPrefix == "" {
		t.Fatal("containerPrefix must not be empty")
	}
	if !strings.HasSuffix(containerPrefix, "-") {
		t.Fatalf("containerPrefix %q should end with '-'", containerPrefix)
	}
}

func TestImagePrefix(t *testing.T) {
	if imagePrefix == "" {
		t.Fatal("imagePrefix must not be empty")
	}
	if !strings.HasSuffix(imagePrefix, "/") {
		t.Fatalf("imagePrefix %q should end with '/'", imagePrefix)
	}
}

func TestContainerWorkDir(t *testing.T) {
	if containerWorkDir == "" {
		t.Fatal("containerWorkDir must not be empty")
	}
	if !strings.HasPrefix(containerWorkDir, "/") {
		t.Fatalf("containerWorkDir %q should be absolute path", containerWorkDir)
	}
}

func TestContainerDalDir(t *testing.T) {
	if containerDalDir == "" {
		t.Fatal("containerDalDir must not be empty")
	}
	if !strings.HasPrefix(containerDalDir, "/") {
		t.Fatalf("containerDalDir %q should be absolute path", containerDalDir)
	}
}

func TestDockerHostAlias(t *testing.T) {
	if dockerHostAlias == "" {
		t.Fatal("dockerHostAlias must not be empty")
	}
}

func TestDefaultLogTail(t *testing.T) {
	if defaultLogTail == "" {
		t.Fatal("defaultLogTail must not be empty")
	}
	// Should be a number
	for _, c := range defaultLogTail {
		if c < '0' || c > '9' {
			t.Fatalf("defaultLogTail %q should be numeric", defaultLogTail)
		}
	}
}

func TestDefaultGitEmailDomain(t *testing.T) {
	if defaultGitEmailDomain == "" {
		t.Fatal("defaultGitEmailDomain must not be empty")
	}
	if !strings.Contains(defaultGitEmailDomain, ".") {
		t.Fatalf("defaultGitEmailDomain %q should contain '.'", defaultGitEmailDomain)
	}
}

// ── Container naming ────────────────────────────────────────────

func TestContainerNameFormat(t *testing.T) {
	tests := []struct {
		instance string
		want     string
	}{
		{"dev", "dal-dev"},
		{"leader", "dal-leader"},
		{"story-checker", "dal-story-checker"},
		{"art-director", "dal-art-director"},
	}
	for _, tt := range tests {
		got := containerPrefix + tt.instance
		if got != tt.want {
			t.Errorf("containerPrefix + %q = %q, want %q", tt.instance, got, tt.want)
		}
	}
}

// ── Image naming ────────────────────────────────────────────────

func TestImageNameFormat(t *testing.T) {
	tests := []struct {
		player  string
		version string
		want    string
	}{
		{"claude", "latest", "dalcenter/claude:latest"},
		{"codex", "latest", "dalcenter/codex:latest"},
		{"gemini", "1.0", "dalcenter/gemini:1.0"},
	}
	for _, tt := range tests {
		got := imagePrefix + tt.player + ":" + tt.version
		if got != tt.want {
			t.Errorf("image for %q:%q = %q, want %q", tt.player, tt.version, got, tt.want)
		}
	}
}

// ── Git email format ────────────────────────────────────────────

func TestGitEmailFormat(t *testing.T) {
	tests := []struct {
		dalName string
		want    string
	}{
		{"dev", "dal-dev@dalcenter.local"},
		{"leader", "dal-leader@dalcenter.local"},
		{"story-checker", "dal-story-checker@dalcenter.local"},
	}
	for _, tt := range tests {
		got := containerPrefix + tt.dalName + "@" + defaultGitEmailDomain
		if got != tt.want {
			t.Errorf("email for %q = %q, want %q", tt.dalName, got, tt.want)
		}
	}
}
