package bridge

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MattermostBridge implements Bridge using Mattermost REST API polling.
type MattermostBridge struct {
	URL          string
	Token        string
	ChannelID    string
	BotUserID    string
	PollInterval time.Duration

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
	m.lastAt = time.Now().UnixMilli()
	go m.poll()
	return nil
}

func (m *MattermostBridge) Listen() <-chan Message { return m.messages }
func (m *MattermostBridge) Errors() <-chan error   { return m.errors }

func (m *MattermostBridge) Send(msg Message) error {
	rootID := msg.RootID
	if rootID == "" {
		rootID = msg.ReplyTo // reply to a root post starts a thread
	}
	body := fmt.Sprintf(`{"channel_id":%q,"root_id":%q,"message":%q}`,
		m.ChannelID, rootID, msg.Content)
	_, err := m.apiPost("/api/v4/posts", body)
	return err
}

func (m *MattermostBridge) Close() error {
	m.once.Do(func() { close(m.done) })
	return nil
}

func (m *MattermostBridge) poll() {
	for {
		select {
		case <-m.done:
			return
		default:
		}

		posts, err := m.fetchNewPosts()
		if err != nil {
			select {
			case m.errors <- err:
			default:
			}
		} else {
			for _, p := range posts {
				select {
				case m.messages <- p:
				case <-m.done:
					return
				}
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
	path := fmt.Sprintf("/api/v4/channels/%s/posts?since=%d", m.ChannelID, m.lastAt)
	data, err := m.apiGet(path)
	if err != nil {
		return nil, err
	}

	var result struct {
		Order []string                   `json:"order"`
		Posts map[string]json.RawMessage `json:"posts"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse posts: %w", err)
	}

	var msgs []Message
	for i := len(result.Order) - 1; i >= 0; i-- {
		id := result.Order[i]
		raw := result.Posts[id]
		var post struct {
			ID       string `json:"id"`
			UserID   string `json:"user_id"`
			Message  string `json:"message"`
			RootID   string `json:"root_id"`
			CreateAt int64  `json:"create_at"`
		}
		if err := json.Unmarshal(raw, &post); err != nil {
			continue
		}
		if post.CreateAt <= m.lastAt {
			continue
		}
		if post.CreateAt > m.lastAt {
			m.lastAt = post.CreateAt
		}
		msgs = append(msgs, Message{
			ID:        post.ID,
			From:      post.UserID,
			Channel:   m.ChannelID,
			Content:   post.Message,
			RootID:    post.RootID,
			Timestamp: time.UnixMilli(post.CreateAt),
		})
	}
	return msgs, nil
}

func (m *MattermostBridge) apiGet(path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", m.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+m.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("MM API %s: %d %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

func (m *MattermostBridge) apiPost(path, jsonBody string) ([]byte, error) {
	req, _ := http.NewRequest("POST", m.URL+path, strings.NewReader(jsonBody))
	req.Header.Set("Authorization", "Bearer "+m.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("MM API %s: %d %s", path, resp.StatusCode, string(body))
	}
	return body, nil
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
