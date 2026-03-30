package talk

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFindExistingBotUserID_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"user_id":"u1","username":"dal-dev"},
			{"user_id":"u2","username":"dal-leader"},
			{"user_id":"u3","username":"dal-story-checker"}
		]`))
	}))
	defer srv.Close()

	id := findExistingBotUserID(srv.URL, "token", "dal-leader")
	if id != "u2" {
		t.Errorf("got %q, want u2", id)
	}
}

func TestFindExistingBotUserID_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"user_id":"u1","username":"dal-dev"}]`))
	}))
	defer srv.Close()

	id := findExistingBotUserID(srv.URL, "token", "dal-nonexistent")
	if id != "" {
		t.Errorf("got %q, want empty", id)
	}
}

func TestFindExistingBotUserID_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	id := findExistingBotUserID(srv.URL, "token", "dal-dev")
	if id != "" {
		t.Errorf("got %q, want empty", id)
	}
}

func TestFindExistingBotUserID_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`server error`))
	}))
	defer srv.Close()

	id := findExistingBotUserID(srv.URL, "token", "dal-dev")
	if id != "" {
		t.Errorf("got %q, want empty on error", id)
	}
}

func TestSetupBot_CreateNew(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/bots" && r.Method == "POST":
			w.Write([]byte(`{"user_id":"bot-u1","username":"dal-test"}`))
		case r.URL.Path == "/api/v4/users/bot-u1/tokens" && r.Method == "POST":
			w.Write([]byte(`{"id":"tok-id","token":"tok-secret"}`))
		case r.URL.Path == "/api/v4/teams/team1/members" && r.Method == "POST":
			step++
			w.Write([]byte(`{}`))
		case r.URL.Path == "/api/v4/channels/ch1/members" && r.Method == "POST":
			step++
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	bot, err := SetupBot(srv.URL, "admin-tok", "team1", "ch1", "dal-test", "Test", "test bot")
	if err != nil {
		t.Fatalf("SetupBot: %v", err)
	}
	if bot.UserID != "bot-u1" {
		t.Errorf("UserID = %q, want bot-u1", bot.UserID)
	}
	if bot.Token != "tok-secret" {
		t.Errorf("Token = %q, want tok-secret", bot.Token)
	}
	if step != 2 {
		t.Errorf("expected team+channel join, got %d steps", step)
	}
}

func TestSetupBot_ReuseExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/bots" && r.Method == "POST":
			// Create fails — already exists
			w.WriteHeader(400)
			w.Write([]byte(`{"id":"app.user.save.username_exists.app_error","message":"already exists"}`))
		case r.URL.Path == "/api/v4/bots" && r.Method == "GET":
			// List bots — find existing
			w.Write([]byte(`[{"user_id":"existing-u1","username":"dal-test"}]`))
		case r.URL.Path == "/api/v4/bots/existing-u1/enable" && r.Method == "POST":
			w.Write([]byte(`{}`))
		case r.URL.Path == "/api/v4/users/existing-u1/tokens" && r.Method == "POST":
			w.Write([]byte(`{"id":"tok-id","token":"reused-token"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	bot, err := SetupBot(srv.URL, "admin-tok", "team1", "ch1", "dal-test", "Test", "test")
	if err != nil {
		t.Fatalf("SetupBot reuse: %v", err)
	}
	if bot.UserID != "existing-u1" {
		t.Errorf("UserID = %q, want existing-u1", bot.UserID)
	}
	if bot.Token != "reused-token" {
		t.Errorf("Token = %q, want reused-token", bot.Token)
	}
}

func TestSetupBot_CreateFail_NoExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/bots" && r.Method == "POST":
			w.WriteHeader(403)
			w.Write([]byte(`{"message":"permission denied"}`))
		case r.URL.Path == "/api/v4/bots" && r.Method == "GET":
			w.Write([]byte(`[]`)) // no existing bot either
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	_, err := SetupBot(srv.URL, "admin-tok", "team1", "ch1", "dal-test", "Test", "test")
	if err == nil {
		t.Fatal("expected error when create fails and no existing bot")
	}
}

func TestGetTeamAndChannel_ExistingChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/teams/name/myteam":
			w.Write([]byte(`{"id":"t1","name":"myteam"}`))
		case r.URL.Path == "/api/v4/teams/t1/channels/name/mychannel":
			w.Write([]byte(`{"id":"ch1","name":"mychannel"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	teamID, chID, err := GetTeamAndChannel(srv.URL, "token", "myteam", "mychannel")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if teamID != "t1" {
		t.Errorf("teamID = %q, want t1", teamID)
	}
	if chID != "ch1" {
		t.Errorf("chID = %q, want ch1", chID)
	}
}

func TestJsonStr(t *testing.T) {
	tests := []struct {
		data []byte
		key  string
		want string
	}{
		{[]byte(`{"id":"abc"}`), "id", "abc"},
		{[]byte(`{"id":"abc"}`), "name", ""},
		{[]byte(`{"id":123}`), "id", ""},
		{[]byte(`invalid`), "id", ""},
		{[]byte(`{}`), "id", ""},
	}
	for _, tt := range tests {
		got := jsonStr(tt.data, tt.key)
		if got != tt.want {
			t.Errorf("jsonStr(%s, %q) = %q, want %q", tt.data, tt.key, got, tt.want)
		}
	}
}
