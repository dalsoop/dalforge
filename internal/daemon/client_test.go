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
