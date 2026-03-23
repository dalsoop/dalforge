package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client talks to the dalcenter daemon over HTTP.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a daemon client. Requires DALCENTER_URL.
func NewClient() *Client {
	url := os.Getenv("DALCENTER_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "error: DALCENTER_URL is not set")
		os.Exit(1)
	}
	return &Client{
		baseURL: strings.TrimRight(url, "/"),
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

// Wake sends a wake request.
func (c *Client) Wake(name string) (map[string]string, error) {
	return c.postJSON(fmt.Sprintf("/api/wake/%s", name))
}

// Sleep sends a sleep request.
func (c *Client) Sleep(name string) (map[string]string, error) {
	return c.postJSON(fmt.Sprintf("/api/sleep/%s", name))
}

// Sync sends a sync request.
func (c *Client) Sync() (map[string]any, error) {
	return c.postAny("/api/sync")
}

// Message posts a message to the project channel.
// MessageResult contains the response from posting a message.
type MessageResult struct {
	PostID string `json:"post_id"`
}

// Message posts a message to the project channel.
func (c *Client) Message(from, message string) (*MessageResult, error) {
	return c.MessageThread(from, message, "")
}

// MessageThread posts a message as a thread reply.
func (c *Client) MessageThread(from, message, threadID string) (*MessageResult, error) {
	body := fmt.Sprintf(`{"from":%q,"message":%q,"thread_id":%q}`, from, message, threadID)
	resp, err := c.http.Post(c.baseURL+"/api/message", "application/json", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("message failed: %s", strings.TrimSpace(string(b)))
	}
	var result MessageResult
	json.NewDecoder(resp.Body).Decode(&result)
	return &result, nil
}

// Ps returns running containers.
func (c *Client) Ps() ([]*Container, error) {
	resp, err := c.http.Get(c.baseURL + "/api/ps")
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("daemon error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var containers []*Container
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, err
	}
	return containers, nil
}

// Logs returns container logs.
func (c *Client) Logs(name string) (string, error) {
	resp, err := c.http.Get(c.baseURL + fmt.Sprintf("/api/logs/%s", name))
	if err != nil {
		return "", fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("daemon error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func (c *Client) postJSON(path string) (map[string]string, error) {
	resp, err := c.http.Post(c.baseURL+path, "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("daemon error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result map[string]string
	json.Unmarshal(body, &result)
	return result, nil
}

func (c *Client) postAny(path string) (map[string]any, error) {
	resp, err := c.http.Post(c.baseURL+path, "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("daemon error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result map[string]any
	json.Unmarshal(body, &result)
	return result, nil
}
