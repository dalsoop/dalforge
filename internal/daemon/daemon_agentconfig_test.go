package daemon

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestHandleAgentConfig_Found(t *testing.T) {
	d := &Daemon{
		bridgeURL: "http://bridge:4242",
		serviceRepo: "/root/test-repo",
		containers: map[string]*Container{
			"story-checker": {
				DalName: "story-checker",
			},
		},
	}

	req := httptest.NewRequest("GET", "/api/agent-config/story-checker", nil)
	req.SetPathValue("name", "story-checker")
	w := httptest.NewRecorder()

	d.handleAgentConfig(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["dal_name"] != "story-checker" {
		t.Errorf("dal_name = %q", resp["dal_name"])
	}
	if resp["bridge_url"] != "http://bridge:4242" {
		t.Errorf("bridge_url = %q", resp["bridge_url"])
	}
	if resp["gateway"] != "dal-test-repo" {
		t.Errorf("gateway = %q", resp["gateway"])
	}
}

func TestHandleAgentConfig_NotFound(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{},
	}

	req := httptest.NewRequest("GET", "/api/agent-config/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()

	d.handleAgentConfig(w, req)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleAgentConfig_NoBridge(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"dev": {DalName: "dev"},
		},
	}

	req := httptest.NewRequest("GET", "/api/agent-config/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()

	d.handleAgentConfig(w, req)

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["bridge_url"] != "" {
		t.Errorf("bridge_url should be empty when not configured, got %q", resp["bridge_url"])
	}
}

