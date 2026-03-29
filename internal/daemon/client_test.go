package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestNewClient_NoURL(t *testing.T) {
	os.Unsetenv("DALCENTER_URL")
	_, err := NewClient()
	if err == nil {
		t.Fatal("expected error when DALCENTER_URL not set")
	}
}

func TestNewClient_WithURL(t *testing.T) {
	os.Setenv("DALCENTER_URL", "http://localhost:11190")
	defer os.Unsetenv("DALCENTER_URL")

	c, err := NewClient()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("client should not be nil")
	}
}

func TestClient_Ps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		containers := []Container{
			{DalName: "dev", Player: "claude", Role: "member", Status: "running", ContainerID: "abc123def456"},
			{DalName: "leader", Player: "claude", Role: "leader", Status: "running", ContainerID: "def456abc789"},
		}
		json.NewEncoder(w).Encode(containers)
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	containers, err := c.Ps()
	if err != nil {
		t.Fatalf("Ps: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("got %d containers, want 2", len(containers))
	}
	if containers[0].DalName != "dev" {
		t.Errorf("first dal = %q, want dev", containers[0].DalName)
	}
	if containers[1].Role != "leader" {
		t.Errorf("second role = %q, want leader", containers[1].Role)
	}
}

func TestClient_Activity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/activity/dev" {
			t.Errorf("path = %s, want /api/activity/dev", r.URL.Path)
		}
		w.Write([]byte(`{"status":"ok","dal":"dev"}`))
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	if _, err := c.Activity("dev"); err != nil {
		t.Fatalf("Activity: %v", err)
	}
}

func TestClient_Ps_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	_, err := c.Ps()
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestClient_Wake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/wake/dev" {
			t.Errorf("path = %s, want /api/wake/dev", r.URL.Path)
		}
		w.Write([]byte(`{"status":"awake","container_id":"abc123"}`))
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	_, err := c.Wake("dev")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
}

func TestClient_Sleep(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sleep/dev" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"status":"sleeping"}`))
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	_, err := c.Sleep("dev")
	if err != nil {
		t.Fatalf("Sleep: %v", err)
	}
}

func TestClient_Message(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/message" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		received = body["message"]
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	_, err := c.Message("dev", "hello from test")
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if received != "hello from test" {
		t.Errorf("received = %q, want 'hello from test'", received)
	}
}

func TestClient_Claims(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/claims" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"claims": []map[string]any{
				{"id": "claim-0001", "dal": "verifier", "title": "credential 만료로 호스트 sync 필요", "status": "open"},
			},
		})
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	claims, err := c.Claims("")
	if err != nil {
		t.Fatalf("Claims: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("got %d claims, want 1", len(claims))
	}
	if claims[0].ID != "claim-0001" {
		t.Fatalf("claim id = %q, want claim-0001", claims[0].ID)
	}
}

func TestClient_Claims_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	if _, err := c.Claims(""); err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestClient_Logs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/logs/dev" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]string{"logs": "line1\nline2"})
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	_, err := c.Logs("dev")
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
}

func TestClient_StartAndFinishTaskRun(t *testing.T) {
	var sawStart, sawEvent, sawFinish bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/task/start":
			sawStart = true
			w.Write([]byte(`{"task_id":"task-1234","status":"running"}`))
		case "/api/task/task-1234/event":
			sawEvent = true
			w.Write([]byte(`{"status":"running","events":[{"kind":"self_repair","message":"retry"}]}`))
		case "/api/task/task-1234/metadata":
			w.Write([]byte(`{"status":"running","verified":"yes","git_changes":2,"completion":{"build_ok":true,"test_ok":true,"duration":"2s"}}`))
		case "/api/task/task-1234/finish":
			sawFinish = true
			w.Write([]byte(`{"status":"done","dal":"leader","task":"triage","output":"ok"}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	started, err := c.StartTaskRun("leader", "triage")
	if err != nil {
		t.Fatalf("StartTaskRun: %v", err)
	}
	if started.ID != "task-1234" {
		t.Fatalf("task id = %q, want task-1234", started.ID)
	}
	updated, err := c.TaskEvent("task-1234", "self_repair", "retry")
	if err != nil {
		t.Fatalf("TaskEvent: %v", err)
	}
	if len(updated.Events) != 1 || updated.Events[0].Kind != "self_repair" {
		t.Fatalf("unexpected task events: %+v", updated.Events)
	}
	meta, err := c.UpdateTaskRun("task-1234", TaskMetadataUpdate{
		GitDiff:    "M README.md",
		GitChanges: 2,
		Verified:   "yes",
		Completion: &CompletionResult{BuildOK: true, TestOK: true, Duration: "2s"},
	})
	if err != nil {
		t.Fatalf("UpdateTaskRun: %v", err)
	}
	if meta.Verified != "yes" || meta.GitChanges != 2 {
		t.Fatalf("unexpected metadata result: %+v", meta)
	}
	finished, err := c.FinishTaskRun("task-1234", "done", "ok", "")
	if err != nil {
		t.Fatalf("FinishTaskRun: %v", err)
	}
	if finished.Status != "done" {
		t.Fatalf("status = %q, want done", finished.Status)
	}
	if !sawStart || !sawEvent || !sawFinish {
		t.Fatalf("expected start, event, finish requests, got start=%v event=%v finish=%v", sawStart, sawEvent, sawFinish)
	}
}
