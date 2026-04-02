package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestHandleACK(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.messages = newMessageStore(filepath.Join(t.TempDir(), "messages.json"))

	// Create and send a message
	m := d.messages.New("dalroot", "local", "test message")
	d.messages.MarkSent(m.ID)

	// ACK it
	req := httptest.NewRequest(http.MethodPost, "/api/ack/"+m.ID, nil)
	req.SetPathValue("id", m.ID)
	w := httptest.NewRecorder()

	d.handleACK(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "acked" {
		t.Errorf("status = %q, want %q", resp["status"], "acked")
	}

	// Verify message is acked in store
	got := d.messages.Get(m.ID)
	if got.Status != MessageAcked {
		t.Errorf("message status = %q, want %q", got.Status, MessageAcked)
	}
}

func TestHandleACK_NotFound(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.messages = newMessageStore(filepath.Join(t.TempDir(), "messages.json"))

	req := httptest.NewRequest(http.MethodPost, "/api/ack/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	d.handleACK(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleACK_EmptyID(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.messages = newMessageStore(filepath.Join(t.TempDir(), "messages.json"))

	req := httptest.NewRequest(http.MethodPost, "/api/ack/", nil)
	req.SetPathValue("id", "")
	w := httptest.NewRecorder()

	d.handleACK(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleMessageList(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.messages = newMessageStore(filepath.Join(t.TempDir(), "messages.json"))

	d.messages.New("a", "b", "msg1")
	m2 := d.messages.New("a", "b", "msg2")
	d.messages.MarkSent(m2.ID)
	d.messages.MarkAcked(m2.ID)

	// List all
	req := httptest.NewRequest(http.MethodGet, "/api/messages", nil)
	w := httptest.NewRecorder()
	d.handleMessageList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Messages []BufferedMessage `json:"messages"`
		Count    int               `json:"count"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2", resp.Count)
	}

	// Filter by status
	req = httptest.NewRequest(http.MethodGet, "/api/messages?status=pending", nil)
	w = httptest.NewRecorder()
	d.handleMessageList(w, req)

	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Count != 1 {
		t.Errorf("pending count = %d, want 1", resp.Count)
	}
}

func TestHandleMessageStatus(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.messages = newMessageStore(filepath.Join(t.TempDir(), "messages.json"))

	m := d.messages.New("a", "b", "hello")

	req := httptest.NewRequest(http.MethodGet, "/api/messages/"+m.ID, nil)
	req.SetPathValue("id", m.ID)
	w := httptest.NewRecorder()
	d.handleMessageStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var got BufferedMessage
	json.NewDecoder(w.Body).Decode(&got)
	if got.Message != "hello" {
		t.Errorf("message = %q, want %q", got.Message, "hello")
	}
}

func TestHandleMessageStatus_NotFound(t *testing.T) {
	d, _ := setupTestDaemon(t)
	d.messages = newMessageStore(filepath.Join(t.TempDir(), "messages.json"))

	req := httptest.NewRequest(http.MethodGet, "/api/messages/nope", nil)
	req.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	d.handleMessageStatus(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
