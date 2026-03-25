package talk

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTeardownBot_Found(t *testing.T) {
	var disabled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/bots" && r.Method == "GET":
			w.Write([]byte(`[{"user_id":"u1","username":"dal-dev"}]`))
		case r.URL.Path == "/api/v4/users/u1/tokens" && r.Method == "GET":
			w.Write([]byte(`[{"id":"tok1"},{"id":"tok2"}]`))
		case r.URL.Path == "/api/v4/users/u1/tokens/revoke":
			w.Write([]byte(`{}`))
		case r.URL.Path == "/api/v4/bots/u1/disable":
			disabled = true
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	err := TeardownBot(srv.URL, "token", "dal-dev")
	if err != nil {
		t.Fatalf("TeardownBot: %v", err)
	}
	if !disabled {
		t.Fatal("bot should have been disabled")
	}
}

func TestTeardownBot_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	err := TeardownBot(srv.URL, "token", "dal-nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent bot")
	}
}

func TestGetAdminToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/users/login" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Token", "session-tok-abc")
		w.Write([]byte(`{"id":"user1"}`))
	}))
	defer srv.Close()

	tok, err := GetAdminToken(srv.URL, "admin", "password")
	if err != nil {
		t.Fatalf("GetAdminToken: %v", err)
	}
	if tok != "session-tok-abc" {
		t.Errorf("token = %q, want session-tok-abc", tok)
	}
}

func TestGetAdminToken_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"message":"invalid credentials"}`))
	}))
	defer srv.Close()

	_, err := GetAdminToken(srv.URL, "admin", "wrong")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestCreateChannel_New(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/channels" && r.Method == "POST" {
			w.Write([]byte(`{"id":"ch-new"}`))
		}
	}))
	defer srv.Close()

	id, err := CreateChannel(srv.URL, "token", "team1", "mychannel")
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if id != "ch-new" {
		t.Errorf("id = %q, want ch-new", id)
	}
}

func TestCreateChannel_RestoreArchived(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/channels" && r.Method == "POST":
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"exists"}`))
		case r.URL.Path == "/api/v4/channels/search":
			w.Write([]byte(`[{"id":"ch-archived","name":"mychannel","delete_at":12345}]`))
		case r.URL.Path == "/api/v4/channels/ch-archived/restore":
			w.Write([]byte(`{"id":"ch-archived"}`))
		}
	}))
	defer srv.Close()

	id, err := CreateChannel(srv.URL, "token", "team1", "mychannel")
	if err != nil {
		t.Fatalf("CreateChannel restore: %v", err)
	}
	if id != "ch-archived" {
		t.Errorf("id = %q, want ch-archived", id)
	}
}

func TestDeleteChannel(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == "/api/v4/channels/ch1" {
			deleted = true
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	err := DeleteChannel(srv.URL, "token", "ch1")
	if err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}
	if !deleted {
		t.Fatal("channel should have been deleted")
	}
}

func TestMmAPI_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`server error`))
	}))
	defer srv.Close()

	_, err := mmAPI("GET", srv.URL+"/api/v4/test", "token", "")
	if err == nil {
		t.Fatal("expected error for 500")
	}
}
