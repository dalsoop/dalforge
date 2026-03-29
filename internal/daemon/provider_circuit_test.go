package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderCircuit_StatusRefreshesCooldown(t *testing.T) {
	pc := &ProviderCircuit{
		primary:      "claude",
		fallback:     "codex",
		activePlayer: "codex",
		trippedAt:    time.Now().Add(-2 * time.Minute),
		cooldown:     time.Minute,
		trippedBy:    "dev",
		reason:       "rate limit",
	}

	status := pc.Status()

	if got := status["active_provider"]; got != "claude" {
		t.Fatalf("active_provider = %v, want claude", got)
	}
	if pc.activePlayer != "claude" {
		t.Fatalf("provider should reset to primary, got %s", pc.activePlayer)
	}
	if _, ok := status["resets_in"]; ok {
		t.Fatal("status should not include resets_in after cooldown reset")
	}
}

func TestHandleProviderTrip_NotifiesOnlyOnStateChange(t *testing.T) {
	prev := globalCircuit
	globalCircuit = &ProviderCircuit{
		primary:      "claude",
		fallback:     "codex",
		activePlayer: "claude",
		cooldown:     4 * time.Hour,
	}
	defer func() { globalCircuit = prev }()

	var posts int
	mmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/posts" {
			posts++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mmServer.Close()

	d := &Daemon{
		mm: &MattermostConfig{
			URL:        mmServer.URL,
			AdminToken: "admin-token",
		},
		channelID: "channel-1",
	}

	body := `{"dal_name":"dev","reason":"You've hit your limit"}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/provider-trip", strings.NewReader(body))
		w := httptest.NewRecorder()
		d.handleProviderTrip(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, w.Code)
		}
	}

	if posts != 1 {
		t.Fatalf("Mattermost posts = %d, want 1", posts)
	}
}

func TestParseProviderPeers_SkipsSelfAndDedupes(t *testing.T) {
	got := parseProviderPeers("11190, :11191, http://127.0.0.1:11191, http://127.0.0.1:11192", ":11191")
	if len(got) != 2 {
		t.Fatalf("got %d peers, want 2: %v", len(got), got)
	}
	if got[0] != "http://127.0.0.1:11190" || got[1] != "http://127.0.0.1:11192" {
		t.Fatalf("unexpected peers: %v", got)
	}
}

func TestHandleProviderTrip_PropagatesToPeersOnce(t *testing.T) {
	prev := globalCircuit
	globalCircuit = &ProviderCircuit{
		primary:      "claude",
		fallback:     "codex",
		activePlayer: "claude",
		cooldown:     4 * time.Hour,
	}
	defer func() { globalCircuit = prev }()

	var hits int
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/provider-trip" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get(providerPeerHeader) != "1" {
			t.Fatalf("missing propagation header")
		}
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()

	t.Setenv("DALCENTER_PROVIDER_PEERS", peer.URL)
	d := &Daemon{addr: ":11193"}

	req := httptest.NewRequest(http.MethodPost, "/api/provider-trip", strings.NewReader(`{"dal_name":"leader","reason":"You've hit your limit"}`))
	w := httptest.NewRecorder()
	d.handleProviderTrip(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if hits != 1 {
		t.Fatalf("peer hits = %d, want 1", hits)
	}
}

func TestHandleProviderTrip_ForwardedRequestDoesNotRepropagate(t *testing.T) {
	prev := globalCircuit
	globalCircuit = &ProviderCircuit{
		primary:      "claude",
		fallback:     "codex",
		activePlayer: "claude",
		cooldown:     4 * time.Hour,
	}
	defer func() { globalCircuit = prev }()

	var hits int
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()

	t.Setenv("DALCENTER_PROVIDER_PEERS", peer.URL)
	d := &Daemon{addr: ":11193"}

	req := httptest.NewRequest(http.MethodPost, "/api/provider-trip", strings.NewReader(`{"dal_name":"leader","reason":"You've hit your limit"}`))
	req.Header.Set(providerPeerHeader, "1")
	w := httptest.NewRecorder()
	d.handleProviderTrip(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if hits != 0 {
		t.Fatalf("peer hits = %d, want 0", hits)
	}
}
