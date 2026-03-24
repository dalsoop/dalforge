package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── isCredentialExpired ─────────────────────────────────────────

func TestIsCredentialExpired_ClaudeExpired(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cred.json")
	past := time.Now().Add(-time.Hour).UnixMilli()
	data := fmt.Sprintf(`{"claudeAiOauth":{"expiresAt":%d}}`, past)
	os.WriteFile(f, []byte(data), 0600)

	expired, err := isCredentialExpired(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !expired {
		t.Fatal("expected expired for past timestamp")
	}
}

func TestIsCredentialExpired_ClaudeValid(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cred.json")
	future := time.Now().Add(time.Hour).UnixMilli()
	data := fmt.Sprintf(`{"claudeAiOauth":{"expiresAt":%d}}`, future)
	os.WriteFile(f, []byte(data), 0600)

	expired, err := isCredentialExpired(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expired {
		t.Fatal("expected not expired for future timestamp")
	}
}

func TestIsCredentialExpired_CodexExpired(t *testing.T) {
	f := filepath.Join(t.TempDir(), "auth.json")
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	data := fmt.Sprintf(`{"tokens":{"expires_at":"%s"}}`, past)
	os.WriteFile(f, []byte(data), 0600)

	expired, err := isCredentialExpired(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !expired {
		t.Fatal("expected expired for past RFC3339")
	}
}

func TestIsCredentialExpired_CodexValid(t *testing.T) {
	f := filepath.Join(t.TempDir(), "auth.json")
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	data := fmt.Sprintf(`{"tokens":{"expires_at":"%s"}}`, future)
	os.WriteFile(f, []byte(data), 0600)

	expired, err := isCredentialExpired(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expired {
		t.Fatal("expected not expired for future RFC3339")
	}
}

func TestIsCredentialExpired_InvalidJSON(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(f, []byte(`not json`), 0600)

	expired, err := isCredentialExpired(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expired {
		t.Fatal("expected false for invalid JSON")
	}
}

func TestIsCredentialExpired_MissingFile(t *testing.T) {
	_, err := isCredentialExpired("/nonexistent/file")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestIsCredentialExpired_EmptyFields(t *testing.T) {
	f := filepath.Join(t.TempDir(), "empty.json")
	os.WriteFile(f, []byte(`{"claudeAiOauth":{"expiresAt":0}}`), 0600)

	expired, err := isCredentialExpired(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expired {
		t.Fatal("expected false for zero expiresAt")
	}
}

func TestIsCredentialExpired_CodexBadDate(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad-date.json")
	os.WriteFile(f, []byte(`{"tokens":{"expires_at":"not-a-date"}}`), 0600)

	expired, err := isCredentialExpired(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expired {
		t.Fatal("expected false for invalid RFC3339")
	}
}

func TestIsCredentialExpired_UnknownFormat(t *testing.T) {
	f := filepath.Join(t.TempDir(), "unknown.json")
	os.WriteFile(f, []byte(`{"some_other_provider":{"key":"value"}}`), 0600)

	expired, err := isCredentialExpired(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expired {
		t.Fatal("expected false for unknown credential format")
	}
}

// ── instructionsFileName ────────────────────────────────────────

func TestInstructionsFileName(t *testing.T) {
	tests := []struct {
		player string
		want   string
	}{
		{"claude", "CLAUDE.md"},
		{"codex", "AGENTS.md"},
		{"gemini", "GEMINI.md"},
		{"unknown", "AGENTS.md"},
		{"", "AGENTS.md"},
	}
	for _, tt := range tests {
		if got := instructionsFileName(tt.player); got != tt.want {
			t.Errorf("instructionsFileName(%q) = %q, want %q", tt.player, got, tt.want)
		}
	}
}

// ── playerHome ──────────────────────────────────────────────────

func TestPlayerHome(t *testing.T) {
	tests := []struct {
		player string
		want   string
	}{
		{"claude", "/root/.claude"},
		{"codex", "/root/.codex"},
		{"gemini", "/root/.gemini"},
		{"unknown", "/root/.config"},
		{"", "/root/.config"},
	}
	for _, tt := range tests {
		if got := playerHome(tt.player); got != tt.want {
			t.Errorf("playerHome(%q) = %q, want %q", tt.player, got, tt.want)
		}
	}
}

// ── JSON round-trip for credential formats ──────────────────────

func TestCredentialFormats_ClaudeRoundTrip(t *testing.T) {
	// Verify the struct tags match the actual credential format
	type claudeCred struct {
		ClaudeAiOauth struct {
			ExpiresAt int64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	input := `{"claudeAiOauth":{"expiresAt":1711929600000}}`
	var c claudeCred
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.ClaudeAiOauth.ExpiresAt != 1711929600000 {
		t.Fatalf("got %d, want 1711929600000", c.ClaudeAiOauth.ExpiresAt)
	}
}

func TestCredentialFormats_CodexRoundTrip(t *testing.T) {
	type codexCred struct {
		Tokens struct {
			ExpiresAt string `json:"expires_at"`
		} `json:"tokens"`
	}
	input := `{"tokens":{"expires_at":"2026-03-20T12:00:00Z"},"last_refresh":"2026-03-19T12:00:00Z"}`
	var c codexCred
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Tokens.ExpiresAt != "2026-03-20T12:00:00Z" {
		t.Fatalf("got %q, want 2026-03-20T12:00:00Z", c.Tokens.ExpiresAt)
	}
}
