package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleMessage_UsesAdminToken(t *testing.T) {
	var receivedAuth string
	mmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/posts":
			receivedAuth = r.Header.Get("Authorization")
			json.NewEncoder(w).Encode(map[string]string{"id": "post-123"})
		default:
			w.WriteHeader(200)
			w.Write([]byte("{}"))
		}
	}))
	defer mmServer.Close()

	d := &Daemon{
		mm: &MattermostConfig{
			URL:        mmServer.URL,
			AdminToken: "admin-secret-token",
		},
		channelID: "ch-123",
		containers: map[string]*Container{
			"leader": {DalName: "leader", BotToken: "leader-bot-token", Role: "leader", Status: "running"},
		},
	}

	body := `{"from":"leader","message":"test message"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Key assertion: should use admin token, NOT leader's bot token
	if receivedAuth != "Bearer admin-secret-token" {
		t.Errorf("expected admin token in Authorization header, got: %s", receivedAuth)
	}
	if strings.Contains(receivedAuth, "leader-bot-token") {
		t.Error("should NOT use leader's bot token for message posting")
	}
}

func TestHandleMessage_UsesAdminTokenNotMemberBot(t *testing.T) {
	var receivedAuth string
	mmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/posts":
			receivedAuth = r.Header.Get("Authorization")
			json.NewEncoder(w).Encode(map[string]string{"id": "post-456"})
		default:
			w.WriteHeader(200)
			w.Write([]byte("{}"))
		}
	}))
	defer mmServer.Close()

	d := &Daemon{
		mm: &MattermostConfig{
			URL:        mmServer.URL,
			AdminToken: "admin-token-xyz",
		},
		channelID: "ch-456",
		containers: map[string]*Container{
			"dev": {DalName: "dev", BotToken: "dev-bot-token", Role: "member", Status: "running"},
		},
	}

	body := `{"from":"dev","message":"reporting done"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if receivedAuth != "Bearer admin-token-xyz" {
		t.Errorf("expected admin token, got: %s", receivedAuth)
	}
	if strings.Contains(receivedAuth, "dev-bot-token") {
		t.Error("should NOT use dev's bot token for message posting")
	}
}

func TestHandleMessage_ThreadID(t *testing.T) {
	var receivedBody string
	mmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/posts" {
			bodyBytes, _ := io.ReadAll(r.Body)
			receivedBody = string(bodyBytes)
			json.NewEncoder(w).Encode(map[string]string{"id": "post-789"})
		} else {
			w.WriteHeader(200)
			w.Write([]byte("{}"))
		}
	}))
	defer mmServer.Close()

	d := &Daemon{
		mm: &MattermostConfig{
			URL:        mmServer.URL,
			AdminToken: "admin-token",
		},
		channelID: "ch-123",
		containers: map[string]*Container{
			"leader": {DalName: "leader", BotToken: "bot-token", Role: "leader", Status: "running"},
		},
	}

	body := `{"from":"leader","message":"reply here","thread_id":"root-post-id"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(receivedBody, "root-post-id") {
		t.Errorf("expected thread_id in post body, got: %s", receivedBody)
	}
}

func TestHandleMessage_ReturnsPostID(t *testing.T) {
	mmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/posts" {
			json.NewEncoder(w).Encode(map[string]string{"id": "returned-post-id"})
		} else {
			w.WriteHeader(200)
			w.Write([]byte("{}"))
		}
	}))
	defer mmServer.Close()

	d := &Daemon{
		mm: &MattermostConfig{
			URL:        mmServer.URL,
			AdminToken: "admin-token",
		},
		channelID: "ch-123",
		containers: map[string]*Container{
			"dev": {DalName: "dev", BotToken: "bot-token", Role: "member", Status: "running"},
		},
	}

	body := `{"from":"dev","message":"hello"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["status"] != "sent" {
		t.Errorf("expected status=sent, got %s", resp["status"])
	}
	if resp["post_id"] != "returned-post-id" {
		t.Errorf("expected post_id=returned-post-id, got %s", resp["post_id"])
	}
}

func TestHandleMessage_BadJSON(t *testing.T) {
	d := &Daemon{
		mm: &MattermostConfig{
			URL:        "http://unused",
			AdminToken: "token",
		},
		channelID:  "ch-123",
		containers: map[string]*Container{},
	}

	req := httptest.NewRequest("POST", "/api/message", strings.NewReader("not-json"))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for bad JSON, got %d", w.Code)
	}
}

func TestHandleMessage_MMPostError(t *testing.T) {
	mmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/posts" {
			w.WriteHeader(500)
			w.Write([]byte("internal error"))
		} else {
			w.WriteHeader(200)
			w.Write([]byte("{}"))
		}
	}))
	defer mmServer.Close()

	d := &Daemon{
		mm: &MattermostConfig{
			URL:        mmServer.URL,
			AdminToken: "admin-token",
		},
		channelID: "ch-123",
		containers: map[string]*Container{
			"dev": {DalName: "dev", BotToken: "bot-token", Role: "member", Status: "running"},
		},
	}

	body := `{"from":"dev","message":"hello"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500 when MM returns error, got %d", w.Code)
	}
}
