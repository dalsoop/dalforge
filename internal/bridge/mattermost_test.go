package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewMattermostBridge(t *testing.T) {
	b := NewMattermostBridge("http://localhost:8065", "token123", "ch-id", 5*time.Second)
	if b.URL != "http://localhost:8065" {
		t.Errorf("URL = %q, want http://localhost:8065", b.URL)
	}
	if b.Token != "token123" {
		t.Errorf("Token = %q, want token123", b.Token)
	}
	if b.ChannelID != "ch-id" {
		t.Errorf("ChannelID = %q, want ch-id", b.ChannelID)
	}
	if b.PollInterval != 5*time.Second {
		t.Errorf("PollInterval = %v, want 5s", b.PollInterval)
	}
}

func TestNewMattermostBridge_TrimsTrailingSlash(t *testing.T) {
	b := NewMattermostBridge("http://localhost:8065/", "tok", "ch", time.Second)
	if b.URL != "http://localhost:8065" {
		t.Errorf("URL = %q, should trim trailing slash", b.URL)
	}
}

func TestConnect_SetsUserID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/me":
			w.Write([]byte(`{"id":"bot-user-123","username":"testbot"}`))
		default:
			w.Write([]byte(`{"order":[],"posts":{}}`))
		}
	}))
	defer srv.Close()

	b := NewMattermostBridge(srv.URL, "token", "ch-id", time.Second)
	if err := b.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer b.Close()

	if b.BotUserID != "bot-user-123" {
		t.Errorf("BotUserID = %q, want bot-user-123", b.BotUserID)
	}
}

func TestConnect_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"message":"invalid token"}`))
	}))
	defer srv.Close()

	b := NewMattermostBridge(srv.URL, "bad-token", "ch-id", time.Second)
	err := b.Connect()
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestSend(t *testing.T) {
	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/me":
			w.Write([]byte(`{"id":"bot-123"}`))
		case "/api/v4/posts":
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			receivedBody = string(body)
			w.Write([]byte(`{"id":"post-1"}`))
		default:
			w.Write([]byte(`{"order":[],"posts":{}}`))
		}
	}))
	defer srv.Close()

	b := NewMattermostBridge(srv.URL, "token", "ch-id", time.Second)
	b.Connect()
	defer b.Close()

	err := b.Send(Message{Content: "hello", ReplyTo: "root-1"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var parsed map[string]string
	json.Unmarshal([]byte(receivedBody), &parsed)
	if parsed["message"] != "hello" {
		t.Errorf("sent message = %q, want hello", parsed["message"])
	}
	if parsed["root_id"] != "root-1" {
		t.Errorf("root_id = %q, want root-1", parsed["root_id"])
	}
}

func TestFetchNewPosts(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/users/me":
			w.Write([]byte(`{"id":"bot-123"}`))
		case r.URL.Path == fmt.Sprintf("/api/v4/channels/ch-id/posts"):
			callCount++
			now := time.Now().UnixMilli()
			resp := fmt.Sprintf(`{
				"order":["p1"],
				"posts":{
					"p1":{
						"id":"p1",
						"user_id":"user-1",
						"message":"@dal-checker 작업 지시: do something",
						"root_id":"",
						"create_at":%d
					}
				}
			}`, now)
			w.Write([]byte(resp))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	b := NewMattermostBridge(srv.URL, "token", "ch-id", 100*time.Millisecond)
	b.Connect()
	defer b.Close()

	select {
	case msg := <-b.Listen():
		if msg.Content != "@dal-checker 작업 지시: do something" {
			t.Errorf("message = %q", msg.Content)
		}
		if msg.From != "user-1" {
			t.Errorf("from = %q, want user-1", msg.From)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"bot-123"}`))
	}))
	defer srv.Close()

	b := NewMattermostBridge(srv.URL, "token", "ch-id", time.Second)
	b.Connect()

	// Should not panic on multiple closes
	b.Close()
	b.Close()
}
