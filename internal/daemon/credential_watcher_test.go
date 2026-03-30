package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsApproachingExpiry_ClaudeNear(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cred.json")
	// Expires in 30 minutes — within 1h threshold
	exp := time.Now().Add(30 * time.Minute).UnixMilli()
	os.WriteFile(f, []byte(fmt.Sprintf(`{"claudeAiOauth":{"expiresAt":%d}}`, exp)), 0600)

	approaching, err := isApproachingExpiry(f, time.Hour)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !approaching {
		t.Fatal("should be approaching expiry")
	}
}

func TestIsApproachingExpiry_ClaudeFar(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cred.json")
	// Expires in 5 hours — not within 1h threshold
	exp := time.Now().Add(5 * time.Hour).UnixMilli()
	os.WriteFile(f, []byte(fmt.Sprintf(`{"claudeAiOauth":{"expiresAt":%d}}`, exp)), 0600)

	approaching, err := isApproachingExpiry(f, time.Hour)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if approaching {
		t.Fatal("should not be approaching expiry")
	}
}

func TestIsApproachingExpiry_CodexNear(t *testing.T) {
	f := filepath.Join(t.TempDir(), "auth.json")
	exp := time.Now().Add(30 * time.Minute).Format(time.RFC3339)
	os.WriteFile(f, []byte(fmt.Sprintf(`{"tokens":{"expires_at":"%s"}}`, exp)), 0600)

	approaching, err := isApproachingExpiry(f, time.Hour)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !approaching {
		t.Fatal("should be approaching expiry")
	}
}

func TestIsApproachingExpiry_MissingFile(t *testing.T) {
	_, err := isApproachingExpiry("/nonexistent", time.Hour)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIsApproachingExpiry_UnknownFormat(t *testing.T) {
	f := filepath.Join(t.TempDir(), "unknown.json")
	os.WriteFile(f, []byte(`{"other":"data"}`), 0600)

	approaching, err := isApproachingExpiry(f, time.Hour)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if approaching {
		t.Fatal("unknown format should not be approaching")
	}
}

func TestRefreshCredential_UnknownPlayer(t *testing.T) {
	// Should not panic
	refreshCredential("unknown-player")
}

func TestCheckAndRefresh_EmptyPaths(t *testing.T) {
	// Should not panic
	checkAndRefresh(&Daemon{credSyncLast: newCredentialSyncMap()}, map[string]string{})
}

func TestCheckAndRefresh_MissingFiles(t *testing.T) {
	// Should not panic
	checkAndRefresh(&Daemon{credSyncLast: newCredentialSyncMap()}, map[string]string{
		"claude": "/nonexistent/cred.json",
		"codex":  "/nonexistent/auth.json",
	})
}
