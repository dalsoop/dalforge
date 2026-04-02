package daemon

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// maxBufferedMessages is the maximum number of messages kept in the store.
	maxBufferedMessages = 200

	// messageACKTimeout is how long to wait for an ACK before marking as timed out.
	messageACKTimeout = 5 * time.Minute

	// maxRetries is the number of times to retry a message before giving up.
	maxRetries = 2
)

// MessageStatus tracks the delivery state of a message.
type MessageStatus string

const (
	MessagePending  MessageStatus = "pending"  // queued, not yet sent
	MessageSent     MessageStatus = "sent"     // delivered to channel/container
	MessageAcked    MessageStatus = "acked"    // receiver confirmed processing
	MessageTimedOut MessageStatus = "timed_out" // no ACK within timeout
	MessageFailed   MessageStatus = "failed"   // delivery failed after retries
)

// BufferedMessage represents a tell message with delivery tracking.
type BufferedMessage struct {
	ID        string        `json:"id"`
	From      string        `json:"from"`
	To        string        `json:"to"`       // target team/dal
	Message   string        `json:"message"`
	Status    MessageStatus `json:"status"`
	Retries   int           `json:"retries"`
	CreatedAt time.Time     `json:"created_at"`
	SentAt    *time.Time    `json:"sent_at,omitempty"`
	AckedAt   *time.Time    `json:"acked_at,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// messageStore persists buffered messages to disk.
type messageStore struct {
	mu       sync.RWMutex
	wg       sync.WaitGroup
	messages []*BufferedMessage
	file     string
	seq      int
}

// newMessageStore creates a store, loading any existing messages from disk.
func newMessageStore(file string) *messageStore {
	s := &messageStore{file: file}
	if err := loadJSON(file, &s.messages); err == nil {
		// Restore sequence counter from existing messages
		for _, m := range s.messages {
			var n int
			if _, err := fmt.Sscanf(m.ID, "msg-%d", &n); err == nil && n > s.seq {
				s.seq = n
			}
		}
	}
	return s
}

// New creates a new buffered message and persists it.
func (s *messageStore) New(from, to, message string) *BufferedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	m := &BufferedMessage{
		ID:        fmt.Sprintf("msg-%04d", s.seq),
		From:      from,
		To:        to,
		Message:   message,
		Status:    MessagePending,
		CreatedAt: time.Now().UTC(),
	}
	s.messages = append(s.messages, m)
	s.evictLocked()
	s.persistAsync()
	return m
}

// MarkSent updates a message status to sent.
func (s *messageStore) MarkSent(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.findLocked(id); m != nil {
		now := time.Now().UTC()
		m.Status = MessageSent
		m.SentAt = &now
		s.persistAsync()
	}
}

// MarkAcked updates a message status to acked.
func (s *messageStore) MarkAcked(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.findLocked(id)
	if m == nil {
		return false
	}
	now := time.Now().UTC()
	m.Status = MessageAcked
	m.AckedAt = &now
	go persistJSON(s.file, s.messages, &s.mu)
	return true
}

// MarkFailed updates a message status to failed.
func (s *messageStore) MarkFailed(id, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.findLocked(id); m != nil {
		m.Status = MessageFailed
		m.Error = errMsg
		s.persistAsync()
	}
}

// IncrRetry increments retry count and returns the new value.
func (s *messageStore) IncrRetry(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.findLocked(id)
	if m == nil {
		return -1
	}
	m.Retries++
	m.Status = MessagePending
	go persistJSON(s.file, s.messages, &s.mu)
	return m.Retries
}

// Get returns a message by ID.
func (s *messageStore) Get(id string) *BufferedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.findLocked(id)
}

// Pending returns messages that need (re)delivery.
func (s *messageStore) Pending() []*BufferedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*BufferedMessage
	for _, m := range s.messages {
		if m.Status == MessagePending {
			result = append(result, m)
		}
	}
	return result
}

// TimedOut returns messages that were sent but not acked within the timeout.
func (s *messageStore) TimedOut() []*BufferedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	var result []*BufferedMessage
	for _, m := range s.messages {
		if m.Status == MessageSent && m.SentAt != nil && now.Sub(*m.SentAt) > messageACKTimeout {
			m.Status = MessageTimedOut
			result = append(result, m)
		}
	}
	if len(result) > 0 {
		s.persistAsync()
	}
	return result
}

// List returns all messages (most recent first).
func (s *messageStore) List() []*BufferedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*BufferedMessage, len(s.messages))
	for i, m := range s.messages {
		result[len(s.messages)-1-i] = m
	}
	return result
}

// persistAsync writes messages to disk in a background goroutine.
// Must be called with s.mu held.
func (s *messageStore) persistAsync() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		persistJSON(s.file, s.messages, &s.mu)
	}()
}

// Flush waits for all in-flight persistence operations to complete.
func (s *messageStore) Flush() {
	s.wg.Wait()
}

func (s *messageStore) findLocked(id string) *BufferedMessage {
	for _, m := range s.messages {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (s *messageStore) evictLocked() {
	if len(s.messages) > maxBufferedMessages {
		// Remove oldest completed messages first
		var keep []*BufferedMessage
		removed := 0
		target := len(s.messages) - maxBufferedMessages
		for _, m := range s.messages {
			if removed < target && (m.Status == MessageAcked || m.Status == MessageFailed) {
				removed++
				continue
			}
			keep = append(keep, m)
		}
		// If we still have too many, drop oldest regardless of status
		if len(keep) > maxBufferedMessages {
			keep = keep[len(keep)-maxBufferedMessages:]
		}
		s.messages = keep
	}
}

// messageBufferDir returns the directory for message buffer files.
func messageBufferDir(serviceRepo string) string {
	dir := filepath.Join(stateDir(serviceRepo), "message-buffer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[message-store] mkdir %s: %v", dir, err)
	}
	return dir
}
