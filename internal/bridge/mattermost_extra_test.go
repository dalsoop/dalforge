package bridge

import (
	"testing"
	"time"
)

func TestErrors_Channel(t *testing.T) {
	b := NewMattermostBridge("http://localhost", "tok", "ch", time.Second)
	ch := b.Errors()
	if ch == nil {
		t.Fatal("Errors() should return non-nil channel")
	}
}

func TestListen_Channel(t *testing.T) {
	b := NewMattermostBridge("http://localhost", "tok", "ch", time.Second)
	ch := b.Listen()
	if ch == nil {
		t.Fatal("Listen() should return non-nil channel")
	}
}

func TestMessage_Fields(t *testing.T) {
	msg := Message{
		ID:      "m1",
		From:    "user1",
		Channel: "ch1",
		Content: "hello",
		RootID:  "root1",
	}
	if msg.ID != "m1" || msg.From != "user1" || msg.Channel != "ch1" {
		t.Error("message fields mismatch")
	}
	if msg.RootID != "root1" {
		t.Error("rootID mismatch")
	}
}

func TestGetUserIDByUsername(t *testing.T) {
	// Needs mock — but already covered in bot_test.go pattern
	// This tests the function signature exists and is callable
	b := NewMattermostBridge("http://invalid:9999", "tok", "ch", time.Second)
	_, err := b.GetUserIDByUsername("test")
	if err == nil {
		t.Error("should fail with invalid URL")
	}
}
