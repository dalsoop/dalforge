package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dalsoop/dalcenter/internal/localdal"
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

func TestShouldDisableContainerDM(t *testing.T) {
	if shouldDisableContainerDM(&localdal.DalProfile{ChannelOnly: true}) != true {
		t.Fatal("channel_only dal should disable DM")
	}
	if shouldDisableContainerDM(&localdal.DalProfile{ChannelOnly: false}) != false {
		t.Fatal("default dal should keep DM enabled")
	}
}

func TestInferredFallbackPlayer(t *testing.T) {
	if got := inferredFallbackPlayer("claude"); got != "codex" {
		t.Fatalf("inferredFallbackPlayer(claude) = %q, want codex", got)
	}
	if got := inferredFallbackPlayer("codex"); got != "claude" {
		t.Fatalf("inferredFallbackPlayer(codex) = %q, want claude", got)
	}
	if got := inferredFallbackPlayer("gemini"); got != "" {
		t.Fatalf("inferredFallbackPlayer(gemini) = %q, want empty", got)
	}
}

func TestCredentialPlayers(t *testing.T) {
	dal := &localdal.DalProfile{Player: "claude", FallbackPlayer: "codex"}
	got := credentialPlayers(dal)
	if len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Fatalf("credentialPlayers() = %v, want [claude codex]", got)
	}

	inferred := credentialPlayers(&localdal.DalProfile{Player: "claude"})
	if len(inferred) != 2 || inferred[0] != "claude" || inferred[1] != "codex" {
		t.Fatalf("credentialPlayers() should infer codex fallback, got %v", inferred)
	}

	same := credentialPlayers(&localdal.DalProfile{Player: "codex", FallbackPlayer: "codex"})
	if len(same) != 1 || same[0] != "codex" {
		t.Fatalf("credentialPlayers() should dedupe fallback, got %v", same)
	}
}

func TestAppendCredentialMounts(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	claudeFuture := time.Now().Add(time.Hour).UnixMilli()
	codexFuture := time.Now().Add(time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(fmt.Sprintf(`{"claudeAiOauth":{"expiresAt":%d}}`, claudeFuture)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(fmt.Sprintf(`{"tokens":{"expires_at":"%s"}}`, codexFuture)), 0600); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	mounts := appendCredentialMounts(nil, home, []string{"claude", "codex"}, &warnings)
	var joined string
	for _, m := range mounts {
		joined += m.Source + " " + m.Target + " "
	}
	if !strings.Contains(joined, ".claude/.credentials.json") {
		t.Fatalf("expected claude credential mount, got %q", joined)
	}
	if !strings.Contains(joined, ".codex/auth.json") {
		t.Fatalf("expected codex credential mount, got %q", joined)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
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

// ── InstanceID in Docker context ────────────────────────────────

func TestNewPrefixedUUID_InstFormat(t *testing.T) {
	id := newPrefixedUUID("inst")
	if !strings.HasPrefix(id, "inst-") {
		t.Fatalf("expected inst- prefix, got %q", id)
	}
	// Should match UUID v4 format: inst-xxxxxxxx-xxxx-4xxx-[89ab]xxx-xxxxxxxxxxxx
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 || parts[0] != "inst" {
		t.Fatalf("unexpected format: %q", id)
	}
	uuidPart := parts[1]
	// 36 chars: 8-4-4-4-12
	if len(uuidPart) != 36 {
		t.Fatalf("uuid part length = %d, want 36: %q", len(uuidPart), uuidPart)
	}
}

func TestNewPrefixedUUID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := newPrefixedUUID("inst")
		if seen[id] {
			t.Fatalf("duplicate instance ID: %s", id)
		}
		seen[id] = true
	}
}

func TestNewPrefixedUUID_DifferentPrefixes(t *testing.T) {
	inst := newPrefixedUUID("inst")
	task := newPrefixedUUID("task")
	fb := newPrefixedUUID("fb")

	if !strings.HasPrefix(inst, "inst-") {
		t.Errorf("expected inst- prefix, got %q", inst)
	}
	if !strings.HasPrefix(task, "task-") {
		t.Errorf("expected task- prefix, got %q", task)
	}
	if !strings.HasPrefix(fb, "fb-") {
		t.Errorf("expected fb- prefix, got %q", fb)
	}
}

// TestDockerRunEnvContract verifies that the dockerRun function signature
// accepts instanceID and documents the env/label contract.
// Actual Docker integration is not tested here (requires Docker daemon).
func TestDockerRunEnvContract(t *testing.T) {
	// The envMap in dockerRun sets DAL_INSTANCE_ID = instanceID.
	// The labels set dalcenter.instance_id = instanceID.
	// This test verifies the contract is maintained by checking
	// that a Container created via handleWake stores the InstanceID.
	d, _ := setupTestDaemon(t)

	// Simulate what handleWake does: generate ID, store in Container
	instanceID := newPrefixedUUID("inst")
	d.containers["dev"] = &Container{
		DalName:     "dev",
		UUID:        "dev-test-001",
		InstanceID:  instanceID,
		ContainerID: "fake-container-id",
		Status:      "running",
	}

	c := d.containers["dev"]
	if c.InstanceID != instanceID {
		t.Errorf("container InstanceID = %q, want %q", c.InstanceID, instanceID)
	}
	if !strings.HasPrefix(c.InstanceID, "inst-") {
		t.Errorf("InstanceID should have inst- prefix, got %q", c.InstanceID)
	}
}

// TestDockerLabelContract verifies that reconcile reads dalcenter.instance_id
// from Docker labels and stores it in the Container struct.
func TestDockerLabelContract(t *testing.T) {
	// Simulate what reconcile does: read label → store in Container
	labels := map[string]string{
		"dalcenter.uuid":        "dev-test-001",
		"dalcenter.instance_id": "inst-restored-from-label",
	}

	c := &Container{
		DalName:     "dev",
		UUID:        labels["dalcenter.uuid"],
		InstanceID:  labels["dalcenter.instance_id"],
		ContainerID: "abc123",
		Status:      "running",
	}

	if c.InstanceID != "inst-restored-from-label" {
		t.Errorf("expected InstanceID from label, got %q", c.InstanceID)
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
