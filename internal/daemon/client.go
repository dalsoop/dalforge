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

// NewClient creates a daemon client.
func NewClient() *Client {
	url := os.Getenv("DALCENTER_URL")
	if url == "" {
		url = "http://localhost:11190"
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
