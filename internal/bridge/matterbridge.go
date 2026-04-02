package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MatterbridgeBridge implements Bridge using the matterbridge REST API.
// See https://github.com/42wim/matterbridge/wiki/API
type MatterbridgeBridge struct {
	URL         string // matterbridge API base URL (e.g. http://localhost:4242)
	Gateway     string // target gateway name
	BotUsername string // bot's own username (for self-message filtering)

	tokenMu  sync.RWMutex
	token    string
	messages chan Message
	errors   chan error
	done     chan struct{}
	once     sync.Once
}

// NewMatterbridgeBridge creates a new matterbridge API bridge.
// gateway is used as the Channel equivalent.
func NewMatterbridgeBridge(url, token, gateway, botUsername string) *MatterbridgeBridge {
	return &MatterbridgeBridge{
		URL:         strings.TrimRight(url, "/"),
		token:       token,
		Gateway:     gateway,
		BotUsername: botUsername,
		messages:    make(chan Message, 64),
		errors:      make(chan error, 8),
		done:        make(chan struct{}),
	}
}

func (mb *MatterbridgeBridge) Connect() error {
	// Verify connectivity by fetching the message buffer.
	_, err := mb.apiGet("/api/messages")
	if err != nil {
		return fmt.Errorf("matterbridge connect: %w", err)
	}
	go mb.stream()
	return nil
}

func (mb *MatterbridgeBridge) Listen() <-chan Message { return mb.messages }
func (mb *MatterbridgeBridge) Errors() <-chan error   { return mb.errors }

func (mb *MatterbridgeBridge) Send(msg Message) error {
	gateway := msg.Channel
	if gateway == "" {
		gateway = mb.Gateway
	}
	payload := struct {
		Text     string `json:"text"`
		Username string `json:"username"`
		Gateway  string `json:"gateway"`
		ParentID string `json:"parent_id,omitempty"`
	}{
		Text:     msg.Content,
		Username: mb.BotUsername,
		Gateway:  gateway,
		ParentID: msg.RootID,
	}
	body, _ := json.Marshal(payload)
	_, err := mb.apiPost("/api/message", string(body))
	return err
}

func (mb *MatterbridgeBridge) UpdateToken(token string) {
	mb.tokenMu.Lock()
	defer mb.tokenMu.Unlock()
	mb.token = token
}

func (mb *MatterbridgeBridge) Close() error {
	mb.once.Do(func() { close(mb.done) })
	return nil
}

func (mb *MatterbridgeBridge) BotID() string {
	return mb.BotUsername
}

func (mb *MatterbridgeBridge) GetUsername(userID string) string {
	// In matterbridge, From is already a username.
	return userID
}

func (mb *MatterbridgeBridge) GetUserIDByUsername(username string) (string, error) {
	// In matterbridge, username is the identity.
	return username, nil
}

func (mb *MatterbridgeBridge) SetNoDM(_ bool) {
	// matterbridge has no DM concept; no-op.
}

func (mb *MatterbridgeBridge) getToken() string {
	mb.tokenMu.RLock()
	defer mb.tokenMu.RUnlock()
	return mb.token
}

// stream connects to GET /api/stream for real-time messages.
// It auto-reconnects on failure with exponential backoff.
func (mb *MatterbridgeBridge) stream() {
	consecutiveErrors := 0
	for {
		select {
		case <-mb.done:
			return
		default:
		}

		err := mb.streamOnce()
		if err != nil {
			consecutiveErrors++
			select {
			case mb.errors <- err:
			default:
			}

			backoff := time.Duration(1<<uint(min(consecutiveErrors, 6))) * time.Second
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			log.Printf("[matterbridge] stream error (%d): %v, retry in %v", consecutiveErrors, err, backoff)
			select {
			case <-time.After(backoff):
			case <-mb.done:
				return
			}
			continue
		}
		consecutiveErrors = 0
	}
}

// streamOnce opens a single streaming connection and reads messages until
// the connection drops or done is closed.
func (mb *MatterbridgeBridge) streamOnce() error {
	req, err := http.NewRequest("GET", mb.URL+"/stream?gateway="+mb.Gateway, nil)
	if err != nil {
		return err
	}
	if t := mb.getToken(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return fmt.Errorf("%w: %s", ErrAuthFailed, string(body))
		}
		return fmt.Errorf("matterbridge stream: %d %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		select {
		case <-mb.done:
			return nil
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}
		// SSE data: 접두사 제거
		line = strings.TrimPrefix(line, "data: ")

		var raw struct {
			Text      string `json:"text"`
			Username  string `json:"username"`
			Gateway   string `json:"gateway"`
			ParentID  string `json:"parent_id"`
			Timestamp string `json:"timestamp"`
			ID        string `json:"id"`
			Event     string `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		// Skip connection events and own messages.
		if raw.Event == "api_connected" || raw.Event == "connected" {
			log.Printf("[matterbridge] stream connected")
			continue
		}
		if raw.Username == mb.BotUsername {
			continue
		}
		// Client-side gateway filter — defense against server not filtering.
		if raw.Gateway != "" && raw.Gateway != mb.Gateway {
			continue
		}

		ts, _ := time.Parse(time.RFC3339, raw.Timestamp)
		msg := Message{
			ID:        raw.ID,
			From:      raw.Username,
			Channel:   raw.Gateway,
			Content:   raw.Text,
			RootID:    raw.ParentID,
			Timestamp: ts,
		}

		select {
		case mb.messages <- msg:
		case <-mb.done:
			return nil
		default:
			// Buffer full — drop oldest.
			select {
			case <-mb.messages:
			default:
			}
			mb.messages <- msg
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream read: %w", err)
	}
	return fmt.Errorf("stream closed by server")
}

func (mb *MatterbridgeBridge) apiGet(path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", mb.URL+path, nil)
	if t := mb.getToken(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("%w: %s", ErrAuthFailed, string(body))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("matterbridge API %s: %d %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

func (mb *MatterbridgeBridge) apiPost(path, jsonBody string) ([]byte, error) {
	req, _ := http.NewRequest("POST", mb.URL+path, strings.NewReader(jsonBody))
	if t := mb.getToken(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("%w: %s", ErrAuthFailed, string(body))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("matterbridge API %s: %d %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}
