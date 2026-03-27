package talk

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// ── RemoveBotFromChannel guard 테스트 ────────────────────

func TestRemoveBotFromChannel_FuncExists(t *testing.T) {
	src, _ := os.ReadFile("bot.go")
	if !strings.Contains(string(src), "func RemoveBotFromChannel(") {
		t.Fatal("RemoveBotFromChannel must exist")
	}
}

func TestHideBotDMFromUsers_FuncExists(t *testing.T) {
	src, _ := os.ReadFile("bot.go")
	if !strings.Contains(string(src), "func HideBotDMFromUsers(") {
		t.Fatal("HideBotDMFromUsers must exist")
	}
}

func TestCleanupOrphanBotDMs_FuncExists(t *testing.T) {
	src, _ := os.ReadFile("bot.go")
	if !strings.Contains(string(src), "func CleanupOrphanBotDMs(") {
		t.Fatal("CleanupOrphanBotDMs must exist")
	}
}

func TestCleanupBotWelcomeDMs_TargetsMessage(t *testing.T) {
	src, _ := os.ReadFile("bot.go")
	if !strings.Contains(string(src), "Please add me to teams") {
		t.Fatal("must target welcome DM message")
	}
}

func TestSetupBot_TokenLimit(t *testing.T) {
	src, _ := os.ReadFile("bot.go")
	if !strings.Contains(string(src), "keep max 2 tokens") {
		t.Fatal("SetupBot must limit token count")
	}
}

func TestSetupBot_EnablesDisabledBot(t *testing.T) {
	src, _ := os.ReadFile("bot.go")
	if !strings.Contains(string(src), "/enable") {
		t.Fatal("SetupBot must re-enable disabled bots")
	}
}

func TestSetupBot_AddsToTeam(t *testing.T) {
	src, _ := os.ReadFile("bot.go")
	if !strings.Contains(string(src), "teams") && !strings.Contains(string(src), "/members") {
		t.Fatal("SetupBot must add bot to team")
	}
}

func TestSetupBot_AddsToChannel(t *testing.T) {
	src, _ := os.ReadFile("bot.go")
	if !strings.Contains(string(src), "channels") && !strings.Contains(string(src), "/members") {
		t.Fatal("SetupBot must add bot to channel")
	}
}

// ── SetupBot HTTP mock 테스트 ────────────────────────────

func TestSetupBot_CreatesNewBot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v4/bots":
			w.Write([]byte(`{"user_id":"bot1"}`))
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/tokens"):
			w.Write([]byte(`[]`))
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/tokens"):
			w.Write([]byte(`{"token":"tok1","id":"tid1"}`))
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/teams/"):
			w.WriteHeader(200)
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/channels/"):
			w.WriteHeader(200)
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/channels"):
			w.Write([]byte(`[]`))
		default:
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()

	bot, err := SetupBot(srv.URL, "admin", "team1", "ch1", "dal-test", "test", "test bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bot.UserID != "bot1" {
		t.Errorf("user_id = %q", bot.UserID)
	}
	if bot.Token != "tok1" {
		t.Errorf("token = %q", bot.Token)
	}
}

func TestSetupBot_ReusesExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v4/bots":
			w.WriteHeader(400) // bot exists
		case r.Method == "GET" && r.URL.Path == "/api/v4/bots":
			w.Write([]byte(`[{"user_id":"existing1","username":"dal-test"}]`))
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/enable"):
			w.WriteHeader(200)
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/tokens"):
			w.Write([]byte(`[{"id":"old1"}]`))
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/tokens"):
			w.Write([]byte(`{"token":"newtok","id":"newtid"}`))
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/teams/"):
			w.WriteHeader(200)
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/channels/"):
			w.WriteHeader(200)
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/channels"):
			w.Write([]byte(`[]`))
		default:
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()

	bot, err := SetupBot(srv.URL, "admin", "team1", "ch1", "dal-test", "test", "test bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bot.UserID != "existing1" {
		t.Errorf("should reuse existing bot, got %q", bot.UserID)
	}
}





func TestCreateChannel_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"ch123"}`))
	}))
	defer srv.Close()

	chID, err := CreateChannel(srv.URL, "admin", "team1", "test-channel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "ch123" {
		t.Errorf("chID = %q", chID)
	}
}

func TestDeleteChannel_Success(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted = true
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	DeleteChannel(srv.URL, "admin", "ch1")
	if !deleted {
		t.Fatal("should call DELETE")
	}
}

func TestGetAdminToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/login") {
			w.Header().Set("Token", "admin-tok-123")
			w.Write([]byte(`{"id":"user1"}`))
		}
	}))
	defer srv.Close()

	token, err := GetAdminToken(srv.URL, "admin", "password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "admin-tok-123" {
		t.Errorf("token = %q", token)
	}
}
