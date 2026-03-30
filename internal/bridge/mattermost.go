package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrAuthFailed is returned when the bot token is invalid or expired.
// This is non-retryable — the caller should stop, not retry.
var ErrAuthFailed = errors.New("auth failed (token invalid or expired)")

// MattermostBridge implements Bridge using Mattermost REST API polling.
type MattermostBridge struct {
	URL          string
	ChannelID    string
	BotUserID    string
	NoDM         bool             // disable DM channel polling
	dmLastAt     map[string]int64 // per-DM-channel lastAt
	PollInterval time.Duration

	tokenMu  sync.RWMutex
	Token    string
	messages chan Message
	errors   chan error
	done     chan struct{}
	once     sync.Once
	lastAt   int64
}

func NewMattermostBridge(url, token, channelID string, pollInterval time.Duration) *MattermostBridge {
	return &MattermostBridge{
		URL:          strings.TrimRight(url, "/"),
		Token:        token,
		ChannelID:    channelID,
		PollInterval: pollInterval,
		messages:     make(chan Message, 64),
		errors:       make(chan error, 8),
		done:         make(chan struct{}),
	}
}

func (m *MattermostBridge) Connect() error {
	user, err := m.apiGet("/api/v4/users/me")
	if err != nil {
		return fmt.Errorf("auth failed: %w", err)
	}
	m.BotUserID = jsonString(user, "id")
	if m.BotUserID == "" {
		return fmt.Errorf("could not determine bot user ID")
	}
	// Use channel's latest message timestamp to avoid fetching old messages
	m.lastAt = m.fetchChannelLatestAt(m.ChannelID)
	go m.poll()
	return nil
}

func (m *MattermostBridge) Listen() <-chan Message { return m.messages }
func (m *MattermostBridge) Errors() <-chan error   { return m.errors }
func (m *MattermostBridge) UpdateToken(token string) {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	m.Token = token
}

func (m *MattermostBridge) token() string {
	m.tokenMu.RLock()
	defer m.tokenMu.RUnlock()
	return m.Token
}

func (m *MattermostBridge) Send(msg Message) error {
	rootID := msg.RootID
	if rootID == "" {
		rootID = msg.ReplyTo // reply to a root post starts a thread
	}
	chID := msg.Channel
	if chID == "" {
		chID = m.ChannelID
	}
	body := fmt.Sprintf(`{"channel_id":%q,"root_id":%q,"message":%q}`,
		chID, rootID, msg.Content)
	_, err := m.apiPost("/api/v4/posts", body)
	return err
}

func (m *MattermostBridge) Close() error {
	m.once.Do(func() { close(m.done) })
	return nil
}

func (m *MattermostBridge) BotID() string {
	return m.BotUserID
}

func (m *MattermostBridge) SetNoDM(noDM bool) {
	m.NoDM = noDM
}

func (m *MattermostBridge) poll() {
	consecutiveErrors := 0
	for {
		select {
		case <-m.done:
			return
		default:
		}

		posts, err := m.fetchNewPosts()
		if len(posts) > 0 {
			log.Printf("[bridge] fetched %d posts", len(posts))
		}
		if err != nil {
			// Non-retryable: auth failure → stop polling
			if errors.Is(err, ErrAuthFailed) {
				log.Printf("[bridge] fatal: %v — stopping poll", err)
				select {
				case m.errors <- err:
				default:
				}
				return
			}

			consecutiveErrors++
			select {
			case m.errors <- err:
			default:
			}

			// Exponential backoff on consecutive errors (max 60s)
			backoff := time.Duration(1<<uint(min(consecutiveErrors, 6))) * time.Second
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			select {
			case <-time.After(backoff):
			case <-m.done:
				return
			}
			continue
		}

		consecutiveErrors = 0
		for _, p := range posts {
			select {
			case m.messages <- p:
			case <-m.done:
				return
			default:
				// Buffer full — drop oldest to prevent blocking
				select {
				case <-m.messages:
				default:
				}
				m.messages <- p
			}
		}

		select {
		case <-time.After(m.PollInterval):
		case <-m.done:
			return
		}
	}
}

func (m *MattermostBridge) fetchNewPosts() ([]Message, error) {
	channels := []string{m.ChannelID}
	if m.BotUserID != "" && !m.NoDM {
		if dms, err := m.fetchDMChannelIDs(); err == nil {
			channels = append(channels, dms...)
		}
	}

	if m.dmLastAt == nil {
		m.dmLastAt = make(map[string]int64)
	}

	var allMsgs []Message
	for _, chID := range channels {
		sinceAt := m.lastAt
		if chID != m.ChannelID {
			if t, ok := m.dmLastAt[chID]; ok {
				sinceAt = t
			} else {
				// First poll: get the latest message timestamp from this DM channel
				// so we only receive messages sent after bridge start
				sinceAt = m.fetchChannelLatestAt(chID)
				m.dmLastAt[chID] = sinceAt
			}
		}
		path := fmt.Sprintf("/api/v4/channels/%s/posts?since=%d&per_page=50", chID, sinceAt)
		data, err := m.apiGet(path)
		if err != nil {
			continue
		}

		var result struct {
			Order []string                   `json:"order"`
			Posts map[string]json.RawMessage `json:"posts"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			continue
		}

		for i := len(result.Order) - 1; i >= 0; i-- {
			id := result.Order[i]
			raw := result.Posts[id]
			var post struct {
				ID        string `json:"id"`
				UserID    string `json:"user_id"`
				ChannelID string `json:"channel_id"`
				Message   string `json:"message"`
				RootID    string `json:"root_id"`
				CreateAt  int64  `json:"create_at"`
			}
			if err := json.Unmarshal(raw, &post); err != nil {
				continue
			}
			if post.CreateAt <= sinceAt {
				continue
			}
			if chID != m.ChannelID {
				m.dmLastAt[chID] = post.CreateAt
			}
			if post.CreateAt > m.lastAt {
				m.lastAt = post.CreateAt
			}
			// Skip own messages at bridge level to prevent self-response loops
			if post.UserID == m.BotUserID {
				continue
			}
			allMsgs = append(allMsgs, Message{
				ID:        post.ID,
				From:      post.UserID,
				Channel:   post.ChannelID,
				Content:   post.Message,
				RootID:    post.RootID,
				Timestamp: time.UnixMilli(post.CreateAt),
			})
		}
	}
	return allMsgs, nil
}

// fetchChannelLatestAt gets the latest message timestamp from a channel.
func (m *MattermostBridge) fetchChannelLatestAt(chID string) int64 {
	data, err := m.apiGet(fmt.Sprintf("/api/v4/channels/%s/posts?per_page=1", chID))
	if err != nil {
		return time.Now().UnixMilli()
	}
	var result struct {
		Order []string                   `json:"order"`
		Posts map[string]json.RawMessage `json:"posts"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Order) == 0 {
		return time.Now().UnixMilli()
	}
	var post struct {
		CreateAt int64 `json:"create_at"`
	}
	if json.Unmarshal(result.Posts[result.Order[0]], &post) == nil {
		return post.CreateAt
	}
	return time.Now().UnixMilli()
}

func (m *MattermostBridge) fetchDMChannelIDs() ([]string, error) {
	data, err := m.apiGet(fmt.Sprintf("/api/v4/users/%s/channels", m.BotUserID))
	if err != nil {
		return nil, err
	}
	var channels []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &channels); err != nil {
		return nil, err
	}
	var dms []string
	for _, ch := range channels {
		if ch.ID == m.ChannelID {
			continue // skip main channel (already polled)
		}
		// Only include true direct-message channels. Polling O/P project channels
		// here makes every bot consume unrelated channel traffic and can create
		// cross-bot reply storms.
		if ch.Type == "D" {
			dms = append(dms, ch.ID)
		}
	}
	return dms, nil
}

func (m *MattermostBridge) apiGet(path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", m.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+m.token())
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
		return nil, fmt.Errorf("MM API %s: %d %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

func (m *MattermostBridge) apiPost(path, jsonBody string) ([]byte, error) {
	req, _ := http.NewRequest("POST", m.URL+path, strings.NewReader(jsonBody))
	req.Header.Set("Authorization", "Bearer "+m.token())
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
		return nil, fmt.Errorf("MM API %s: %d %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

// GetUserIDByUsername resolves a Mattermost username to a user ID.
func (m *MattermostBridge) GetUserIDByUsername(username string) (string, error) {
	data, err := m.apiGet("/api/v4/users/username/" + username)
	if err != nil {
		return "", fmt.Errorf("get user %q: %w", username, err)
	}
	id := jsonString(data, "id")
	if id == "" {
		return "", fmt.Errorf("get user %q: no id in response", username)
	}
	return id, nil
}

// GetUsername returns the Mattermost username for a given user ID.
func (m *MattermostBridge) GetUsername(userID string) string {
	data, err := m.apiGet("/api/v4/users/" + userID)
	if err != nil {
		return ""
	}
	return jsonString(data, "username")
}

func jsonString(data []byte, key string) string {
	var m map[string]interface{}
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
