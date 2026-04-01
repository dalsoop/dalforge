package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"status":        "ok",
			"dals_running":  2,
			"repo_count":    3,
			"leader_status": "running",
		})
	}))
	defer srv.Close()

	client := srv.Client()
	h, err := fetchHealth(client, srv.URL)
	if err != nil {
		t.Fatalf("fetchHealth error: %v", err)
	}
	if h.DalsRunning != 2 {
		t.Fatalf("dals_running = %d, want 2", h.DalsRunning)
	}
	if h.LeaderStatus != "running" {
		t.Fatalf("leader_status = %q, want 'running'", h.LeaderStatus)
	}
}

func TestFetchHealth_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", 500)
	}))
	defer srv.Close()

	client := srv.Client()
	_, err := fetchHealth(client, srv.URL)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestFindRemoteLeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"Name": "dev", "Role": "member"},
			{"Name": "dal-leader", "Role": "leader"},
		})
	}))
	defer srv.Close()

	client := srv.Client()
	name, err := findRemoteLeader(client, srv.URL)
	if err != nil {
		t.Fatalf("findRemoteLeader error: %v", err)
	}
	if name != "dal-leader" {
		t.Fatalf("name = %q, want 'dal-leader'", name)
	}
}

func TestFindRemoteLeader_NoLeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"Name": "dev", "Role": "member"},
		})
	}))
	defer srv.Close()

	client := srv.Client()
	_, err := findRemoteLeader(client, srv.URL)
	if err == nil {
		t.Fatal("expected error when no leader in response")
	}
}

func TestOpsCheckTeam_HealthyTeam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status":        "ok",
			"dals_running":  1,
			"leader_status": "running",
		})
	}))
	defer srv.Close()

	d := &Daemon{containers: map[string]*Container{}}
	err := d.opsCheckTeam(srv.Client(), teamEndpoint{Name: "test", URL: srv.URL})
	if err != nil {
		t.Fatalf("expected nil error for healthy team, got: %v", err)
	}
}

func TestOpsCheckTeam_NoLeaderConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status":        "ok",
			"dals_running":  0,
			"leader_status": "not_configured",
		})
	}))
	defer srv.Close()

	d := &Daemon{containers: map[string]*Container{}}
	err := d.opsCheckTeam(srv.Client(), teamEndpoint{Name: "test", URL: srv.URL})
	if err != nil {
		t.Fatalf("expected nil error when leader not configured, got: %v", err)
	}
}

func TestOpsCheckTeam_WakeLeader(t *testing.T) {
	var wakeRequested bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			json.NewEncoder(w).Encode(map[string]any{
				"status":        "ok",
				"dals_running":  0,
				"leader_status": "sleeping",
			})
		case "/api/status":
			json.NewEncoder(w).Encode([]map[string]any{
				{"Name": "dal-leader", "Role": "leader"},
			})
		case "/api/wake/dal-leader":
			wakeRequested = true
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	d := &Daemon{containers: map[string]*Container{}}
	err := d.opsCheckTeam(srv.Client(), teamEndpoint{Name: "test", URL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wakeRequested {
		t.Fatal("expected wake request to be sent")
	}
}

func TestDiscoverTeams(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)

	// Write common.env
	os.WriteFile(filepath.Join(dir, "common.env"), []byte("DALCENTER_HOST_IP=10.0.0.1\n"), 0644)

	// Write team env files
	os.WriteFile(filepath.Join(dir, "team-a.env"), []byte("DALCENTER_PORT=11191\n"), 0644)
	os.WriteFile(filepath.Join(dir, "team-b.env"), []byte("DALCENTER_PORT=11192\n"), 0644)

	teams := discoverTeams()
	if len(teams) != 2 {
		t.Fatalf("len(teams) = %d, want 2", len(teams))
	}

	// Check that host IP was picked up
	for _, team := range teams {
		if team.URL == "" {
			t.Fatalf("team %s has empty URL", team.Name)
		}
		if team.URL != "http://10.0.0.1:11191" && team.URL != "http://10.0.0.1:11192" {
			t.Fatalf("unexpected URL: %s", team.URL)
		}
	}
}

func TestDiscoverTeams_NoConfigDir(t *testing.T) {
	t.Setenv("DALCENTER_CONFIG_DIR", "/nonexistent")
	teams := discoverTeams()
	if len(teams) != 0 {
		t.Fatalf("expected 0 teams, got %d", len(teams))
	}
}

func TestOpsWatcherEnabled(t *testing.T) {
	t.Setenv("DALCENTER_OPS_WATCHER", "1")
	if !opsWatcherEnabled() {
		t.Fatal("expected enabled")
	}

	t.Setenv("DALCENTER_OPS_WATCHER", "")
	if opsWatcherEnabled() {
		t.Fatal("expected disabled")
	}
}
