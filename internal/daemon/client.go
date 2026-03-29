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

var errNoURL = fmt.Errorf("DALCENTER_URL is not set")

// Client talks to the dalcenter daemon over HTTP.
type Client struct {
	baseURL  string
	apiToken string
	http     *http.Client
}

// NewClient creates a daemon client. Requires DALCENTER_URL.
// Reads DALCENTER_TOKEN for authenticated write requests.
func NewClient() (*Client, error) {
	url := os.Getenv("DALCENTER_URL")
	if url == "" {
		return nil, errNoURL
	}
	return &Client{
		baseURL:  strings.TrimRight(url, "/"),
		apiToken: os.Getenv("DALCENTER_TOKEN"),
		http:     &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// Wake sends a wake request.
func (c *Client) Wake(name string) (map[string]string, error) {
	return c.postJSON(fmt.Sprintf("/api/wake/%s", name))
}

// Sleep sends a sleep request.
func (c *Client) Sleep(name string) (map[string]string, error) {
	return c.postJSON(fmt.Sprintf("/api/sleep/%s", name))
}

// AgentConfig fetches the agent config for a dal.
func (c *Client) AgentConfig(name string) (map[string]string, error) {
	resp, err := http.Get(c.baseURL + "/api/agent-config/" + name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

// Restart sends a restart request (sleep + remove + fresh wake).
func (c *Client) Restart(name string) (map[string]string, error) {
	return c.postJSON(fmt.Sprintf("/api/restart/%s", name))
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
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/message", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("message failed: %s", strings.TrimSpace(string(b)))
	}
	var result MessageResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
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

func (c *Client) doPost(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("daemon error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *Client) postJSON(path string) (map[string]string, error) {
	body, err := c.doPost(path)
	if err != nil {
		return nil, err
	}
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return result, nil
}

func (c *Client) postAny(path string) (map[string]any, error) {
	body, err := c.doPost(path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return result, nil
}

// ClaimResult holds the response from submitting a claim.
type ClaimResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Claim submits feedback from a dal to the host.
func (c *Client) Claim(dal, claimType, title, detail, ctx string) (*ClaimResult, error) {
	body := fmt.Sprintf(`{"dal":%q,"type":%q,"title":%q,"detail":%q,"context":%q}`,
		dal, claimType, title, detail, ctx)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/claim", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("claim failed: %s", strings.TrimSpace(string(b)))
	}
	var result ClaimResult
	json.Unmarshal(b, &result)
	return &result, nil
}

// Claims lists claims with optional status filter.
func (c *Client) Claims(status string) ([]Claim, error) {
	url := c.baseURL + "/api/claims"
	if status != "" {
		url += "?status=" + status
	}
	resp, err := c.http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("daemon error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Claims []Claim `json:"claims"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return result.Claims, nil
}

// ClaimRespond responds to a claim (host action).
func (c *Client) ClaimRespond(id, status, response string) error {
	body := fmt.Sprintf(`{"status":%q,"response":%q}`, status, response)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/claims/"+id+"/respond", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("respond failed: %s", strings.TrimSpace(string(b)))
	}
	return nil
}

// Escalate sends an escalation to the dalcenter daemon.
func (c *Client) Escalate(dal, task, errorClass, output string) error {
	body := fmt.Sprintf(`{"dal":%q,"task":%q,"error_class":%q,"output":%q}`,
		dal, task, errorClass, output)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/escalate", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("escalate failed: %s", strings.TrimSpace(string(b)))
	}
	return nil
}

// TaskResult holds the response from a direct task execution.
type TaskResult struct {
	ID     string `json:"task_id,omitempty"`
	Status string `json:"status"`
	// Full result fields (when polling)
	Dal        string            `json:"dal,omitempty"`
	Task       string            `json:"task,omitempty"`
	Output     string            `json:"output,omitempty"`
	Error      string            `json:"error,omitempty"`
	StartedAt  string            `json:"started_at,omitempty"`
	DoneAt     *string           `json:"done_at,omitempty"`
	Events     []TaskEvent       `json:"events,omitempty"`
	GitDiff    string            `json:"git_diff,omitempty"`
	GitChanges int               `json:"git_changes,omitempty"`
	Verified   string            `json:"verified,omitempty"`
	Completion *CompletionResult `json:"completion,omitempty"`
}

type TaskEvent struct {
	At      string `json:"at,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Message string `json:"message,omitempty"`
}

// Task submits a direct task to a dal container. If async=true, returns immediately with a task ID.
func (c *Client) Task(dal, task string, async bool) (*TaskResult, error) {
	body := fmt.Sprintf(`{"dal":%q,"task":%q,"async":%t}`, dal, task, async)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/task", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("task failed: %s", strings.TrimSpace(string(b)))
	}
	var result TaskResult
	json.Unmarshal(b, &result)
	return &result, nil
}

// TaskStatus polls a task by ID.
func (c *Client) TaskStatus(id string) (*TaskResult, error) {
	resp, err := c.http.Get(c.baseURL + "/api/task/" + id)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("task not found: %s", strings.TrimSpace(string(b)))
	}
	var result TaskResult
	json.Unmarshal(b, &result)
	return &result, nil
}

// StartTaskRun registers a tracked task for an external execution path such as Mattermost.
func (c *Client) StartTaskRun(dal, task string) (*TaskResult, error) {
	body := fmt.Sprintf(`{"dal":%q,"task":%q}`, dal, task)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/task/start", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("task start failed: %s", strings.TrimSpace(string(b)))
	}
	var result TaskResult
	json.Unmarshal(b, &result)
	return &result, nil
}

// FinishTaskRun finalizes a tracked task created by StartTaskRun.
func (c *Client) FinishTaskRun(id, status, output, errMsg string) (*TaskResult, error) {
	body := fmt.Sprintf(`{"status":%q,"output":%q,"error":%q}`, status, output, errMsg)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/task/"+id+"/finish", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("task finish failed: %s", strings.TrimSpace(string(b)))
	}
	var result TaskResult
	json.Unmarshal(b, &result)
	return &result, nil
}

// TaskEvent appends an event to a tracked task created by StartTaskRun.
func (c *Client) TaskEvent(id, kind, message string) (*TaskResult, error) {
	body := fmt.Sprintf(`{"kind":%q,"message":%q}`, kind, message)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/task/"+id+"/event", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("task event failed: %s", strings.TrimSpace(string(b)))
	}
	var result TaskResult
	json.Unmarshal(b, &result)
	return &result, nil
}

// UpdateTaskRun updates verification metadata for a tracked task.
func (c *Client) UpdateTaskRun(id string, update TaskMetadataUpdate) (*TaskResult, error) {
	bodyBytes, err := json.Marshal(update)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/task/"+id+"/metadata", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("task metadata update failed: %s", strings.TrimSpace(string(b)))
	}
	var result TaskResult
	json.Unmarshal(b, &result)
	return &result, nil
}

// Feedback submits a task result for persistent tracking.
func (c *Client) Feedback(dal, taskID, task, result, errMsg string, gitChanges int, durationMs int64) (*Feedback, error) {
	body := fmt.Sprintf(`{"dal":%q,"task_id":%q,"task":%q,"result":%q,"error":%q,"git_changes":%d,"duration_ms":%d}`,
		dal, taskID, task, result, errMsg, gitChanges, durationMs)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/feedback", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("feedback failed: %s", strings.TrimSpace(string(b)))
	}
	var fb Feedback
	json.Unmarshal(b, &fb)
	return &fb, nil
}

// FeedbackList returns feedback entries, optionally filtered by dal name.
func (c *Client) FeedbackList(dal string) ([]Feedback, error) {
	url := c.baseURL + "/api/feedback"
	if dal != "" {
		url += "?dal=" + dal
	}
	resp, err := c.http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Feedback []Feedback `json:"feedback"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Feedback, nil
}

// FeedbackStats returns per-dal success rate aggregation.
func (c *Client) FeedbackStats() ([]DalStats, error) {
	resp, err := c.http.Get(c.baseURL + "/api/feedback/stats")
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Stats []DalStats `json:"stats"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Stats, nil
}

// TaskList returns all tracked tasks.
func (c *Client) TaskList() ([]TaskResult, error) {
	resp, err := c.http.Get(c.baseURL + "/api/tasks")
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	var results []TaskResult
	json.NewDecoder(resp.Body).Decode(&results)
	return results, nil
}
