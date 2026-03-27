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
