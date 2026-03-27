package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dalsoop/dalcenter/internal/daemon"
)

// mockDaemon creates an httptest server mimicking dalcenter API.
func mockDaemon(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/ps" && r.Method == "GET":
			json.NewEncoder(w).Encode([]daemon.Container{
				{DalName: "dev", UUID: "test-dev-uuid-1234", Player: "claude", Role: "member", Status: "running", ContainerID: "abc123def456ghi"},
				{DalName: "leader", UUID: "test-leader-uuid-5678", Player: "claude", Role: "leader", Status: "running", ContainerID: "def456abc789jkl"},
			})
		case r.URL.Path == "/api/wake/dev" && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]string{"status": "awake", "container_id": "new-container-abc"})
		case r.URL.Path == "/api/sleep/dev" && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]string{"status": "sleeping"})
		case r.URL.Path == "/api/sync" && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]string{"status": "synced"})
		case r.URL.Path == "/api/logs/dev" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]string{"logs": "line1\nline2\nline3"})
		case r.URL.Path == "/api/message" && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]string{"post_id": "post-abc-123", "thread_id": "thread-xyz"})
		default:
			w.WriteHeader(404)
			w.Write([]byte("not found: " + r.URL.Path))
		}
	}))
}

func withMock(t *testing.T, fn func()) {
	srv := mockDaemon(t)
	defer srv.Close()
	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")
	fn()
}

// ── Command creation tests ──

func TestWakeCmd_Exists(t *testing.T) {
	cmd := wakeCmd()
	if cmd.Use != "wake <dal>" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestSleepCmd_Exists(t *testing.T) {
	cmd := sleepCmd()
	if cmd.Use != "sleep <dal>" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestPsCmd_Exists(t *testing.T) {
	cmd := psCmd()
	if cmd.Use != "ps" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestStatusCmd_Exists(t *testing.T) {
	cmd := statusCmd()
	if cmd.Use != "status <dal>" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestLogsCmd_Exists(t *testing.T) {
	cmd := logsCmd()
	if cmd.Use != "logs <dal>" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestSyncCmd_Exists(t *testing.T) {
	cmd := syncCmd()
	if cmd.Use != "sync" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

func TestAssignCmd_Exists(t *testing.T) {
	cmd := assignCmd("leader")
	if cmd.Use != "assign <dal> <task>" {
		t.Errorf("Use = %q", cmd.Use)
	}
}

// ── Execution tests with mock ──

func TestWakeCmd_Execute(t *testing.T) {
	withMock(t, func() {
		cmd := wakeCmd()
		cmd.SetArgs([]string{"dev"})
		err := cmd.Execute()
		if err != nil {
			t.Fatalf("wake: %v", err)
		}
	})
}

func TestSleepCmd_Execute(t *testing.T) {
	withMock(t, func() {
		cmd := sleepCmd()
		cmd.SetArgs([]string{"dev"})
		err := cmd.Execute()
		if err != nil {
			t.Fatalf("sleep: %v", err)
		}
	})
}

func TestPsCmd_Execute(t *testing.T) {
	withMock(t, func() {
		cmd := psCmd()
		err := cmd.Execute()
		if err != nil {
			t.Fatalf("ps: %v", err)
		}
	})
}

func TestStatusCmd_Execute(t *testing.T) {
	withMock(t, func() {
		cmd := statusCmd()
		cmd.SetArgs([]string{"dev"})
		err := cmd.Execute()
		if err != nil {
			t.Fatalf("status: %v", err)
		}
	})
}

func TestStatusCmd_NotFound(t *testing.T) {
	withMock(t, func() {
		cmd := statusCmd()
		cmd.SetArgs([]string{"nonexistent"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for nonexistent dal")
		}
	})
}

func TestLogsCmd_Execute(t *testing.T) {
	withMock(t, func() {
		cmd := logsCmd()
		cmd.SetArgs([]string{"dev"})
		err := cmd.Execute()
		if err != nil {
			t.Fatalf("logs: %v", err)
		}
	})
}

func TestSyncCmd_Execute(t *testing.T) {
	withMock(t, func() {
		cmd := syncCmd()
		err := cmd.Execute()
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
	})
}

func TestAssignCmd_Execute(t *testing.T) {
	withMock(t, func() {
		cmd := assignCmd("leader")
		cmd.SetArgs([]string{"dev", "do the thing"})
		err := cmd.Execute()
		if err != nil {
			t.Fatalf("assign: %v", err)
		}
	})
}

// ── Argument validation ──

func TestWakeCmd_NoArgs(t *testing.T) {
	cmd := wakeCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestAssignCmd_MissingTask(t *testing.T) {
	cmd := assignCmd("leader")
	cmd.SetArgs([]string{"dev"}) // missing task
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing task arg")
	}
}

// ── No daemon ──

func TestPsCmd_NoDaemon(t *testing.T) {
	os.Setenv("DALCENTER_URL", "http://localhost:19999")
	defer os.Unsetenv("DALCENTER_URL")

	cmd := psCmd()
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when daemon unreachable")
	}
}

func TestPostCmd_Exists(t *testing.T) {
	src := readSrc(t, "main.go")
	if !strings.Contains(src, "postCmd()") {
		t.Fatal("must have postCmd registered")
	}
}

func TestPostCmd_RequiresChannel(t *testing.T) {
	src := readSrc(t, "main.go")
	if !strings.Contains(src, `"channel"`) {
		t.Fatal("post cmd must have --channel flag")
	}
}

func TestPostCmd_UsesBotToken(t *testing.T) {
	src := readSrc(t, "main.go")
	if !strings.Contains(src, "bot_token") {
		t.Fatal("post cmd must get bot_token from agent config")
	}
}

func TestPostCmd_PostsToMM(t *testing.T) {
	src := readSrc(t, "main.go")
	if !strings.Contains(src, "/api/v4/posts") {
		t.Fatal("post cmd must call Mattermost /api/v4/posts")
	}
}

func TestAgentConfigMethod(t *testing.T) {
	src := readSrc(t, "../../internal/daemon/client.go")
	if !strings.Contains(src, "func (c *Client) AgentConfig(") {
		t.Fatal("Client must have AgentConfig method")
	}
}

func readSrc(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("cannot read %s: %v", file, err)
	}
	return string(data)
}
