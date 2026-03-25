package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestClient_Sync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sync" || r.Method != "POST" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "synced"})
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	_, err := c.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

func TestClient_MessageThread(t *testing.T) {
	var received map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		json.NewEncoder(w).Encode(map[string]string{"post_id": "p1", "thread_id": "t1"})
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	_, err := c.MessageThread("dev", "hello", "thread-123")
	if err != nil {
		t.Fatalf("MessageThread: %v", err)
	}
	if received["thread_id"] != "thread-123" {
		t.Errorf("thread_id = %q, want thread-123", received["thread_id"])
	}
}

func TestClient_Sync_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	_, err := c.Sync()
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestClient_Wake_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("dal not found"))
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	_, err := c.Wake("nonexistent")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestClient_Logs_Content(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"logs": "line1\nline2\nline3"})
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	c, _ := NewClient()
	logs, err := c.Logs("dev")
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if logs == "" {
		t.Error("logs should not be empty")
	}
}
