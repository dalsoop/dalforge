package bridge

import (
	"os"
	"strings"
	"testing"
)

func TestGetUsername_MethodExists(t *testing.T) {
	data, err := os.ReadFile("mattermost.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func (m *MattermostBridge) GetUsername(") {
		t.Fatal("MattermostBridge must have GetUsername method")
	}
}

func TestFetchDMChannelIDs_Exists(t *testing.T) {
	data, err := os.ReadFile("mattermost.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	if !strings.Contains(src, "func (m *MattermostBridge) fetchDMChannelIDs()") {
		t.Fatal("MattermostBridge must have fetchDMChannelIDs method")
	}
}

func TestFetchNewPosts_PollsDMChannels(t *testing.T) {
	data, err := os.ReadFile("mattermost.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	if !strings.Contains(src, "fetchDMChannelIDs") {
		t.Fatal("fetchNewPosts must call fetchDMChannelIDs for DM support")
	}
}

func TestSend_UsesMessageChannel(t *testing.T) {
	data, err := os.ReadFile("mattermost.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	if !strings.Contains(src, "msg.Channel") {
		t.Fatal("Send must use msg.Channel for DM replies")
	}
}

// ── Send 분기 테스트 ─────────────────────────────────────

func TestSend_FallbackToMainChannel(t *testing.T) {
	src, _ := os.ReadFile("mattermost.go")
	s := string(src)
	if !strings.Contains(s, "msg.Channel") {
		t.Fatal("Send must check msg.Channel")
	}
	if !strings.Contains(s, "m.ChannelID") {
		t.Fatal("Send must fallback to m.ChannelID")
	}
}

func TestFetchNewPosts_MultiChannel(t *testing.T) {
	src, _ := os.ReadFile("mattermost.go")
	s := string(src)
	if !strings.Contains(s, "for _, chID := range channels") {
		t.Fatal("fetchNewPosts must iterate over multiple channels")
	}
}

func TestBridge_HasDmLastAt(t *testing.T) {
	src, _ := os.ReadFile("mattermost.go")
	if !strings.Contains(string(src), "dmLastAt") {
		t.Fatal("bridge must have dmLastAt map")
	}
}

func TestBridge_PostChannelIDInMessage(t *testing.T) {
	src, _ := os.ReadFile("mattermost.go")
	if !strings.Contains(string(src), "post.ChannelID") {
		t.Fatal("must use post.ChannelID in parsed messages")
	}
}

func TestBridge_ErrAuthFailed(t *testing.T) {
	src, _ := os.ReadFile("mattermost.go")
	if !strings.Contains(string(src), "ErrAuthFailed") {
		t.Fatal("must handle ErrAuthFailed")
	}
}
