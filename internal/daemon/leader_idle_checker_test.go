package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchPs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"dal_name": "leader", "role": "leader", "idle_for": "5m0s"},
			{"dal_name": "dev", "role": "member", "idle_for": "1m0s"},
		})
	}))
	defer srv.Close()

	containers, err := fetchPs(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchPs error: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("len = %d, want 2", len(containers))
	}
	if containers[0].DalName != "leader" {
		t.Fatalf("dal_name = %q, want 'leader'", containers[0].DalName)
	}
	if containers[0].IdleFor != "5m0s" {
		t.Fatalf("idle_for = %q, want '5m0s'", containers[0].IdleFor)
	}
}

func TestFetchPs_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", 500)
	}))
	defer srv.Close()

	_, err := fetchPs(srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestCheckTeamLeaderIdle_ActiveLeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"dal_name": "leader", "role": "leader", "idle_for": "5m0s"},
		})
	}))
	defer srv.Close()

	d := &Daemon{containers: map[string]*Container{}}
	err := d.checkTeamLeaderIdle(srv.Client(), teamEndpoint{Name: "test", URL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error for active leader: %v", err)
	}
}

func TestCheckTeamLeaderIdle_IdleLeader(t *testing.T) {
	var sleepCalled, wakeCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/ps" && r.Method == "GET":
			json.NewEncoder(w).Encode([]map[string]any{
				{"dal_name": "dal-leader", "role": "leader", "idle_for": "3h35m0s"},
			})
		case r.URL.Path == "/api/sleep/dal-leader" && r.Method == "POST":
			sleepCalled = true
			json.NewEncoder(w).Encode(map[string]string{"status": "sleeping"})
		case r.URL.Path == "/api/status" && r.Method == "GET":
			json.NewEncoder(w).Encode([]map[string]any{
				{"Name": "dal-leader", "Role": "leader"},
			})
		case r.URL.Path == "/api/wake/dal-leader" && r.Method == "POST":
			wakeCalled = true
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	d := &Daemon{containers: map[string]*Container{}}
	err := d.checkTeamLeaderIdle(srv.Client(), teamEndpoint{Name: "test", URL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sleepCalled {
		t.Fatal("expected sleep to be called")
	}
	if !wakeCalled {
		t.Fatal("expected wake to be called")
	}
}

func TestCheckTeamLeaderIdle_NoLeader(t *testing.T) {
	var wakeCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/ps" && r.Method == "GET":
			json.NewEncoder(w).Encode([]map[string]any{
				{"dal_name": "dev", "role": "member", "idle_for": "10m0s"},
			})
		case r.URL.Path == "/api/status" && r.Method == "GET":
			json.NewEncoder(w).Encode([]map[string]any{
				{"Name": "dal-leader", "Role": "leader"},
			})
		case r.URL.Path == "/api/wake/dal-leader" && r.Method == "POST":
			wakeCalled = true
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	d := &Daemon{containers: map[string]*Container{}}
	err := d.checkTeamLeaderIdle(srv.Client(), teamEndpoint{Name: "test", URL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wakeCalled {
		t.Fatal("expected wake to be called for absent leader")
	}
}

func TestSleepLeaderRemote(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sleep/leader" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		called = true
		json.NewEncoder(w).Encode(map[string]string{"status": "sleeping"})
	}))
	defer srv.Close()

	err := sleepLeaderRemote(srv.Client(), srv.URL, "leader")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected sleep endpoint to be called")
	}
}

func TestSleepLeaderRemote_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	err := sleepLeaderRemote(srv.Client(), srv.URL, "leader")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{3*time.Hour + 35*time.Minute, "3h35m"},
		{45 * time.Minute, "45m"},
		{1 * time.Hour, "1h0m"},
		{2*time.Hour + 5*time.Minute + 30*time.Second, "2h5m"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestLeaderIdleCheckerEnabled(t *testing.T) {
	t.Setenv("DALCENTER_LEADER_IDLE_CHECK", "1")
	if !leaderIdleCheckerEnabled() {
		t.Fatal("expected enabled")
	}

	t.Setenv("DALCENTER_LEADER_IDLE_CHECK", "")
	if leaderIdleCheckerEnabled() {
		t.Fatal("expected disabled")
	}
}
