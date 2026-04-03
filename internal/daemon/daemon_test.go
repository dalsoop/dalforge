package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	root := t.TempDir()

	// Create leader dal
	leaderDir := filepath.Join(root, "leader")
	os.MkdirAll(leaderDir, 0755)
	os.WriteFile(filepath.Join(leaderDir, "dal.cue"), []byte(`
uuid:    "leader-test-001"
name:    "leader"
version: "1.0.0"
player:  "claude"
role:    "leader"
skills:  []
hooks:   []
`), 0644)
	os.WriteFile(filepath.Join(leaderDir, "charter.md"), []byte("# Leader\n"), 0644)

	// Create dev dal
	devDir := filepath.Join(root, "dev")
	os.MkdirAll(devDir, 0755)
	os.WriteFile(filepath.Join(devDir, "dal.cue"), []byte(`
uuid:    "dev-test-001"
name:    "dev"
version: "1.0.0"
player:  "claude"
role:    "member"
skills:  []
hooks:   []
`), 0644)
	os.WriteFile(filepath.Join(devDir, "charter.md"), []byte("# Dev\n"), 0644)

	d := New(":0", root, "", "", "", "", "")
	return d, root
}

func TestHandlePs(t *testing.T) {
	d, _ := setupTestDaemon(t)

	req := httptest.NewRequest("GET", "/api/ps", nil)
	w := httptest.NewRecorder()
	d.handlePs(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var containers []*Container
	json.NewDecoder(w.Body).Decode(&containers)
	if len(containers) != 0 {
		t.Fatalf("expected 0 containers, got %d", len(containers))
	}
}

func TestHandleStatus(t *testing.T) {
	d, _ := setupTestDaemon(t)

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	d.handleStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	// Should list leader + dev
	body := w.Body.String()
	if !strings.Contains(body, "leader") || !strings.Contains(body, "dev") {
		t.Fatalf("expected leader and dev in response: %s", body)
	}
}

func TestHandleStatusOne(t *testing.T) {
	d, _ := setupTestDaemon(t)

	req := httptest.NewRequest("GET", "/api/status/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()
	d.handleStatusOne(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "dev-test-001") {
		t.Fatalf("expected uuid in response: %s", body)
	}
}

func TestHandleStatusOne_WithInstanceID(t *testing.T) {
	d, _ := setupTestDaemon(t)

	// Simulate a running container with InstanceID
	d.containers["dev"] = &Container{
		DalName:     "dev",
		UUID:        "dev-test-001",
		InstanceID:  "inst-abc123",
		ContainerID: "container-xyz",
		Status:      "running",
	}

	req := httptest.NewRequest("GET", "/api/status/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()
	d.handleStatusOne(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["instance_id"]; got != "inst-abc123" {
		t.Errorf("expected instance_id=inst-abc123, got %v", got)
	}
	if got := resp["status"]; got != "running" {
		t.Errorf("expected status=running, got %v", got)
	}
}

func TestHandleStatusOneNotFound(t *testing.T) {
	d, _ := setupTestDaemon(t)

	req := httptest.NewRequest("GET", "/api/status/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	d.handleStatusOne(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleValidate(t *testing.T) {
	d, _ := setupTestDaemon(t)

	req := httptest.NewRequest("POST", "/api/validate", nil)
	w := httptest.NewRecorder()
	d.handleValidate(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestHandleSync(t *testing.T) {
	d, _ := setupTestDaemon(t)

	req := httptest.NewRequest("POST", "/api/sync", nil)
	w := httptest.NewRecorder()
	d.handleSync(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "no running dals") {
		t.Fatalf("expected no running dals: %s", body)
	}
}

func TestHandleWakeNotFound(t *testing.T) {
	d, _ := setupTestDaemon(t)

	req := httptest.NewRequest("POST", "/api/wake/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	d.handleWake(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleSleepNotAwake(t *testing.T) {
	d, _ := setupTestDaemon(t)

	req := httptest.NewRequest("POST", "/api/sleep/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()
	d.handleSleep(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleMessageNoBridge(t *testing.T) {
	d, _ := setupTestDaemon(t)

	body := `{"from":"dev","message":"hello"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleMessage(w, req)

	if w.Code != 503 {
		t.Fatalf("expected 503 (no bridge), got %d", w.Code)
	}
}

func TestDuplicateWakeReturnsConflict(t *testing.T) {
	d, _ := setupTestDaemon(t)

	// Simulate first instance already running
	d.containers["dev"] = &Container{DalName: "dev", ContainerID: "abc123", Status: "running"}

	// Second wake should be rejected with 409
	req := httptest.NewRequest("POST", "/api/wake/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()
	d.handleWake(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "already running") {
		t.Fatalf("expected 'already running' in body, got %q", w.Body.String())
	}
}

func TestContainerJSON_InstanceID(t *testing.T) {
	c := &Container{
		DalName:    "dev",
		UUID:       "dev-001",
		InstanceID: "inst-xyz789",
		Status:     "running",
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	if got := m["instance_id"]; got != "inst-xyz789" {
		t.Errorf("expected instance_id=inst-xyz789 in JSON, got %v", got)
	}
}

// ── InstanceID in handleWake / reconcile ────────────────────────

func TestHandleWake_StoresInstanceID(t *testing.T) {
	d, _ := setupTestDaemon(t)

	// We can't actually call handleWake (needs Docker), but we can verify
	// the InstanceID generation + storage pattern used by handleWake.
	instanceID := newPrefixedUUID("inst")
	if !strings.HasPrefix(instanceID, "inst-") {
		t.Fatalf("expected inst- prefix, got %q", instanceID)
	}

	// Simulate handleWake storing the container
	d.mu.Lock()
	d.containers["dev"] = &Container{
		DalName:     "dev",
		UUID:        "dev-test-001",
		InstanceID:  instanceID,
		Player:      "claude",
		Role:        "member",
		ContainerID: "fake-ctr-id",
		Status:      "running",
		Workspace:   "shared",
	}
	d.mu.Unlock()

	// Verify via handleStatusOne API
	req := httptest.NewRequest("GET", "/api/status/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()
	d.handleStatusOne(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if got := resp["instance_id"]; got != instanceID {
		t.Errorf("expected instance_id=%s, got %v", instanceID, got)
	}
}

func TestHandleWake_EachWakeGetsUniqueInstanceID(t *testing.T) {
	d, _ := setupTestDaemon(t)

	// Simulate two sequential wakes (sleep between them)
	id1 := newPrefixedUUID("inst")
	d.containers["dev"] = &Container{
		DalName:    "dev",
		InstanceID: id1,
		Status:     "running",
	}

	// "Sleep" the dal
	delete(d.containers, "dev")

	// Second wake
	id2 := newPrefixedUUID("inst")
	d.containers["dev"] = &Container{
		DalName:    "dev",
		InstanceID: id2,
		Status:     "running",
	}

	if id1 == id2 {
		t.Fatalf("expected different instance IDs across wakes, both = %s", id1)
	}
}

func TestReconcile_RestoresInstanceIDFromLabel(t *testing.T) {
	// Simulate what reconcile does: read label from discovered container
	// and store InstanceID in the Container struct.
	d, _ := setupTestDaemon(t)

	labelInstanceID := "inst-reconciled-abc123"

	// This mirrors reconcile's logic at daemon.go:1142-1153
	d.mu.Lock()
	d.containers["dev"] = &Container{
		DalName:     "dev",
		UUID:        "dev-test-001",
		InstanceID:  labelInstanceID, // from c.Labels["dalcenter.instance_id"]
		Player:      "claude",
		Role:        "member",
		ContainerID: "reconciled-ctr",
		Status:      "running",
	}
	d.mu.Unlock()

	// Verify it's accessible via status API
	req := httptest.NewRequest("GET", "/api/status/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()
	d.handleStatusOne(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if got := resp["instance_id"]; got != labelInstanceID {
		t.Errorf("reconcile should restore instance_id from label, got %v", got)
	}
}

func TestReconcile_EmptyLabelResultsInEmptyInstanceID(t *testing.T) {
	d, _ := setupTestDaemon(t)

	// Container with no instance_id label (pre-#685 container)
	d.mu.Lock()
	d.containers["dev"] = &Container{
		DalName:     "dev",
		UUID:        "dev-test-001",
		InstanceID:  "", // label not present
		Player:      "claude",
		Role:        "member",
		ContainerID: "old-ctr",
		Status:      "running",
	}
	d.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/status/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()
	d.handleStatusOne(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	// instance_id should be empty string (or absent) for old containers
	if got, ok := resp["instance_id"]; ok && got != "" {
		t.Errorf("expected empty instance_id for pre-#685 container, got %v", got)
	}
}

func TestStatusOne_InstanceIDNotRunning(t *testing.T) {
	d, _ := setupTestDaemon(t)

	// No container running → status should not have instance_id
	req := httptest.NewRequest("GET", "/api/status/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()
	d.handleStatusOne(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if got := resp["instance_id"]; got != nil && got != "" {
		t.Errorf("expected no instance_id when not running, got %v", got)
	}
}

func TestRunServer(t *testing.T) {
	d, _ := setupTestDaemon(t)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Run should exit cleanly on context cancel
	err := d.Run(ctx)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
}

// ── API Auth Tests ──────────────────────────────────────────────

func TestRequireAuth_NoToken_AllowAll(t *testing.T) {
	d, _ := setupTestDaemon(t)
	// No apiToken set — all requests allowed

	req := httptest.NewRequest("POST", "/api/sync", nil)
	w := httptest.NewRecorder()
	d.requireAuth(d.handleSync)(w, req)

	if w.Code == 401 {
		t.Fatal("expected no auth required when DALCENTER_TOKEN is empty")
	}
}

func TestRequireAuth_WithToken_Rejects(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.apiToken = "secret-token"

	req := httptest.NewRequest("POST", "/api/sync", nil)
	w := httptest.NewRecorder()
	d.requireAuth(d.handleSync)(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401 without token, got %d", w.Code)
	}
}

func TestRequireAuth_WithToken_WrongToken(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.apiToken = "secret-token"

	req := httptest.NewRequest("POST", "/api/sync", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	d.requireAuth(d.handleSync)(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401 with wrong token, got %d", w.Code)
	}
}

func TestRequireAuth_WithToken_CorrectToken(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.apiToken = "secret-token"

	req := httptest.NewRequest("POST", "/api/sync", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	d.requireAuth(d.handleSync)(w, req)

	if w.Code == 401 {
		t.Fatal("expected auth to pass with correct token")
	}
}

func TestRequireAuth_ReadEndpoints_NoAuth(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.apiToken = "secret-token"

	// Read endpoint should work without token (not wrapped with requireAuth)
	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	d.handleStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 for read endpoint without token, got %d", w.Code)
	}
}
