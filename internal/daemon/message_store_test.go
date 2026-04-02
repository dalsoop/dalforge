package daemon

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMessageStore_NewAndGet(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	m := s.New("dalroot", "dalcenter", "hello world")
	if m.ID != "msg-0001" {
		t.Errorf("id = %q, want %q", m.ID, "msg-0001")
	}
	if m.Status != MessagePending {
		t.Errorf("status = %q, want %q", m.Status, MessagePending)
	}
	if m.From != "dalroot" {
		t.Errorf("from = %q, want %q", m.From, "dalroot")
	}

	got := s.Get(m.ID)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Message != "hello world" {
		t.Errorf("message = %q, want %q", got.Message, "hello world")
	}
}

func TestMessageStore_MarkSent(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	m := s.New("a", "b", "msg")
	s.MarkSent(m.ID)

	got := s.Get(m.ID)
	if got.Status != MessageSent {
		t.Errorf("status = %q, want %q", got.Status, MessageSent)
	}
	if got.SentAt == nil {
		t.Error("SentAt should be set")
	}
}

func TestMessageStore_MarkAcked(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	m := s.New("a", "b", "msg")
	s.MarkSent(m.ID)

	ok := s.MarkAcked(m.ID)
	if !ok {
		t.Error("MarkAcked returned false")
	}

	got := s.Get(m.ID)
	if got.Status != MessageAcked {
		t.Errorf("status = %q, want %q", got.Status, MessageAcked)
	}
	if got.AckedAt == nil {
		t.Error("AckedAt should be set")
	}
}

func TestMessageStore_MarkAcked_NotFound(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	ok := s.MarkAcked("nonexistent")
	if ok {
		t.Error("MarkAcked should return false for unknown ID")
	}
}

func TestMessageStore_MarkFailed(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	m := s.New("a", "b", "msg")
	s.MarkFailed(m.ID, "network error")

	got := s.Get(m.ID)
	if got.Status != MessageFailed {
		t.Errorf("status = %q, want %q", got.Status, MessageFailed)
	}
	if got.Error != "network error" {
		t.Errorf("error = %q, want %q", got.Error, "network error")
	}
}

func TestMessageStore_IncrRetry(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	m := s.New("a", "b", "msg")
	s.MarkSent(m.ID)

	r1 := s.IncrRetry(m.ID)
	if r1 != 1 {
		t.Errorf("retry = %d, want 1", r1)
	}

	got := s.Get(m.ID)
	if got.Status != MessagePending {
		t.Errorf("status after retry = %q, want %q", got.Status, MessagePending)
	}

	r2 := s.IncrRetry(m.ID)
	if r2 != 2 {
		t.Errorf("retry = %d, want 2", r2)
	}
}

func TestMessageStore_Pending(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	m1 := s.New("a", "b", "msg1")
	s.New("a", "b", "msg2")
	s.MarkSent(m1.ID)

	pending := s.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending count = %d, want 1", len(pending))
	}
	if pending[0].Message != "msg2" {
		t.Errorf("pending message = %q, want %q", pending[0].Message, "msg2")
	}
}

func TestMessageStore_TimedOut(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	m := s.New("a", "b", "msg")
	// Manually set SentAt to past
	s.mu.Lock()
	m.Status = MessageSent
	past := time.Now().UTC().Add(-messageACKTimeout - time.Minute)
	m.SentAt = &past
	s.mu.Unlock()

	timedOut := s.TimedOut()
	if len(timedOut) != 1 {
		t.Fatalf("timed out count = %d, want 1", len(timedOut))
	}
	if timedOut[0].ID != m.ID {
		t.Errorf("timed out id = %q, want %q", timedOut[0].ID, m.ID)
	}
}

func TestMessageStore_List(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	s.New("a", "b", "first")
	s.New("a", "b", "second")

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("list count = %d, want 2", len(list))
	}
	// Most recent first
	if list[0].Message != "second" {
		t.Errorf("first in list = %q, want %q", list[0].Message, "second")
	}
}

func TestMessageStore_Eviction(t *testing.T) {
	dir := t.TempDir()
	s := newMessageStore(filepath.Join(dir, "messages.json"))

	// Fill beyond max
	for i := 0; i < maxBufferedMessages+10; i++ {
		s.New("a", "b", "msg")
	}

	list := s.List()
	if len(list) > maxBufferedMessages {
		t.Errorf("list count = %d, want <= %d", len(list), maxBufferedMessages)
	}
}

func TestMessageStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "messages.json")

	s1 := newMessageStore(file)
	m := s1.New("a", "b", "persistent msg")
	s1.MarkSent(m.ID)
	s1.MarkAcked(m.ID)

	// Wait for async persist
	time.Sleep(100 * time.Millisecond)

	// Load from disk
	s2 := newMessageStore(file)
	got := s2.Get(m.ID)
	if got == nil {
		t.Fatal("message not found after reload")
	}
	if got.Status != MessageAcked {
		t.Errorf("status after reload = %q, want %q", got.Status, MessageAcked)
	}
	if got.Message != "persistent msg" {
		t.Errorf("message after reload = %q, want %q", got.Message, "persistent msg")
	}
}

func TestMessageStore_SeqRestore(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "messages.json")

	s1 := newMessageStore(file)
	s1.New("a", "b", "m1")
	s1.New("a", "b", "m2")
	time.Sleep(100 * time.Millisecond)

	s2 := newMessageStore(file)
	m := s2.New("a", "b", "m3")
	if m.ID != "msg-0003" {
		t.Errorf("id after reload = %q, want %q", m.ID, "msg-0003")
	}
}
