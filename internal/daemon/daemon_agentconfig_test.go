package daemon

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestHandleAgentConfig_Found(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"story-checker": {
				DalName:  "story-checker",
				BotToken: "bot-tok-123",
			},
		},
		channelID: "ch-abc",
		mm:        &MattermostConfig{URL: "http://mm:8065"},
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
	if resp["bot_token"] != "bot-tok-123" {
		t.Errorf("bot_token = %q", resp["bot_token"])
	}
	if resp["channel_id"] != "ch-abc" {
		t.Errorf("channel_id = %q", resp["channel_id"])
	}
	if resp["mm_url"] != "http://mm:8065" {
		t.Errorf("mm_url = %q", resp["mm_url"])
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

func TestHandleAgentConfig_NoMM(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"dev": {DalName: "dev", BotToken: "tok"},
		},
		channelID: "ch-1",
		mm:        nil,
	}

	req := httptest.NewRequest("GET", "/api/agent-config/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()

	d.handleAgentConfig(w, req)

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["mm_url"] != "" {
		t.Errorf("mm_url should be empty when mm is nil, got %q", resp["mm_url"])
	}
}
