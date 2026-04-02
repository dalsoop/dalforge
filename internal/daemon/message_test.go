package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func testMessageStore(t *testing.T) *messageStore {
	t.Helper()
	return newMessageStore(filepath.Join(t.TempDir(), "messages.json"))
}

func TestHandleMessage_PostsViaBridge(t *testing.T) {
	var receivedBody string
	bridgeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer bridgeSrv.Close()

	d := &Daemon{
		bridgeURL: bridgeSrv.URL,
		containers: map[string]*Container{
			"leader": {DalName: "leader", Role: "leader", Status: "running"},
		},
		messages: testMessageStore(t),
	}

	body := `{"from":"leader","message":"test message"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(receivedBody, "test message") {
		t.Errorf("expected message in bridge post body, got: %s", receivedBody)
	}
	if !strings.Contains(receivedBody, "leader") {
		t.Errorf("expected username in bridge post body, got: %s", receivedBody)
	}
}

func TestHandleMessage_DefaultUsername(t *testing.T) {
	var receivedBody string
	bridgeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer bridgeSrv.Close()

	d := &Daemon{
		bridgeURL:  bridgeSrv.URL,
		containers: map[string]*Container{},
		messages:   testMessageStore(t),
	}

	body := `{"from":"","message":"hello"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(receivedBody, "dalcenter") {
		t.Errorf("expected default username 'dalcenter', got: %s", receivedBody)
	}
}

func TestHandleMessage_ReturnsStatusSent(t *testing.T) {
	bridgeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer bridgeSrv.Close()

	d := &Daemon{
		bridgeURL: bridgeSrv.URL,
		containers: map[string]*Container{
			"dev": {DalName: "dev", Role: "member", Status: "running"},
		},
		messages: testMessageStore(t),
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
}

func TestHandleMessage_BadJSON(t *testing.T) {
	d := &Daemon{
		bridgeURL:  "http://unused",
		containers: map[string]*Container{},
		messages:   testMessageStore(t),
	}

	req := httptest.NewRequest("POST", "/api/message", strings.NewReader("not-json"))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for bad JSON, got %d", w.Code)
	}
}

func TestHandleMessage_BridgeError(t *testing.T) {
	bridgeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer bridgeSrv.Close()

	d := &Daemon{
		bridgeURL: bridgeSrv.URL,
		containers: map[string]*Container{
			"dev": {DalName: "dev", Role: "member", Status: "running"},
		},
		messages: testMessageStore(t),
	}

	body := `{"from":"dev","message":"hello"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500 when bridge returns error, got %d", w.Code)
	}
}

func TestHandleMessage_NoBridgeFallbackToTask(t *testing.T) {
	d := &Daemon{
		bridgeURL: "", // no bridge
		containers: map[string]*Container{
			"dev": {DalName: "dev", Role: "member", Status: "running"},
		},
		tasks:    newTaskStore(),
		feedback: newFeedbackStore(),
		messages: testMessageStore(t),
	}

	body := `{"from":"dev","message":"do something"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	// Should fallback to task dispatch
	if w.Code != 202 {
		t.Fatalf("expected 202 (task dispatched), got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "task_dispatched" {
		t.Errorf("expected status=task_dispatched, got %s", resp["status"])
	}
}

func TestHandleMessage_NoBridgeNoContainers(t *testing.T) {
	d := &Daemon{
		bridgeURL:  "",
		containers: map[string]*Container{},
		messages:   testMessageStore(t),
	}

	body := `{"from":"dev","message":"hello"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	if w.Code != 503 {
		t.Fatalf("expected 503 when no bridge and no containers, got %d", w.Code)
	}
}
