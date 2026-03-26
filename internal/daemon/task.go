package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// taskResult holds the result of a direct task execution.
type taskResult struct {
	ID        string    `json:"id"`
	Dal       string    `json:"dal"`
	Task      string    `json:"task"`
	Output    string    `json:"output"`
	Error     string    `json:"error,omitempty"`
	Status    string    `json:"status"` // "running", "done", "failed"
	StartedAt time.Time `json:"started_at"`
	DoneAt    *time.Time `json:"done_at,omitempty"`
}

// taskStore manages running and completed direct tasks.
type taskStore struct {
	mu    sync.RWMutex
	tasks map[string]*taskResult
	seq   int
}

func newTaskStore() *taskStore {
	return &taskStore{tasks: make(map[string]*taskResult)}
}

func (s *taskStore) New(dal, task string) *taskResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	t := &taskResult{
		ID:        fmt.Sprintf("task-%04d", s.seq),
		Dal:       dal,
		Task:      task,
		Status:    "running",
		StartedAt: time.Now().UTC(),
	}
	s.tasks[t.ID] = t

	// Evict old tasks (keep last 50)
	if len(s.tasks) > 50 {
		var oldest string
		var oldestTime time.Time
		for id, t := range s.tasks {
			if t.Status != "running" && (oldest == "" || t.StartedAt.Before(oldestTime)) {
				oldest = id
				oldestTime = t.StartedAt
			}
		}
		if oldest != "" {
			delete(s.tasks, oldest)
		}
	}

	return t
}

func (s *taskStore) Get(id string) *taskResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tasks[id]
}

func (s *taskStore) List() []*taskResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*taskResult
	for _, t := range s.tasks {
		result = append(result, t)
	}
	return result
}

// handleTask executes a task directly inside a dal container via docker exec.
// This works without Mattermost — direct command execution.
// POST /api/task
// Body: {"dal": "leader", "task": "...", "async": false}
func (d *Daemon) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Dal   string `json:"dal"`
		Task  string `json:"task"`
		Async bool   `json:"async"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Dal == "" || req.Task == "" {
		http.Error(w, "dal and task are required", http.StatusBadRequest)
		return
	}

	d.mu.RLock()
	c, ok := d.containers[req.Dal]
	d.mu.RUnlock()
	if !ok {
		http.Error(w, fmt.Sprintf("dal %q is not awake", req.Dal), http.StatusNotFound)
		return
	}

	tr := d.tasks.New(req.Dal, req.Task)

	if req.Async {
		// Run in background, return task ID for polling
		go d.execTaskInContainer(c, tr)
		respondJSON(w, http.StatusAccepted, map[string]string{
			"task_id": tr.ID,
			"status":  "running",
		})
		return
	}

	// Synchronous: execute and return result
	d.execTaskInContainer(c, tr)
	status := http.StatusOK
	if tr.Status == "failed" {
		status = http.StatusInternalServerError
	}
	respondJSON(w, status, tr)
}

// handleTaskStatus returns the status of a previously submitted task.
// GET /api/task/{id}
func (d *Daemon) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tr := d.tasks.Get(id)
	if tr == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	respondJSON(w, http.StatusOK, tr)
}

// handleTaskList returns all tracked tasks.
// GET /api/tasks
func (d *Daemon) handleTaskList(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, d.tasks.List())
}

// execTaskInContainer runs a Claude/Codex command inside the container.
func (d *Daemon) execTaskInContainer(c *Container, tr *taskResult) {
	log.Printf("[task] executing %s on %s: %s", tr.ID, c.DalName, truncateStr(tr.Task, 80))

	// Build docker exec command that pipes task via stdin.
	// Claude's -p flag requires stdin input (not positional args).
	var cmdArgs []string
	switch c.Player {
	case "codex":
		cmdArgs = []string{
			"docker", "exec", c.ContainerID,
			"codex", "exec",
			"--dangerously-bypass-approvals-and-sandbox",
			"-C", containerWorkDir,
			tr.Task,
		}
	default: // claude
		allowedTools := "Bash Read Write Glob Grep Edit"
		if c.Role != "leader" {
			allowedTools = "Bash(git:*,gh:*) Read Write Glob Grep Edit"
		}
		// Use bash -c to pipe task via stdin to claude -p
		claudeCmd := fmt.Sprintf("cd %s && claude -p --allowedTools %q",
			containerWorkDir, allowedTools)
		cmdArgs = []string{
			"docker", "exec",
			"-i",
			"-e", "CLAUDE_CODE_ENTRYPOINT=dalcli",
			c.ContainerID,
			"bash", "-c", claudeCmd,
		}
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	// Pipe task content via stdin for claude -p
	if c.Player != "codex" {
		cmd.Stdin = strings.NewReader(tr.Task)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	now := time.Now().UTC()
	tr.DoneAt = &now

	if err != nil {
		tr.Status = "failed"
		tr.Output = stdout.String()
		tr.Error = fmt.Sprintf("%v: %s", err, stderr.String())
		log.Printf("[task] %s failed: %v", tr.ID, err)

		// Dispatch webhook
		dispatchWebhook(WebhookEvent{
			Event:     "task_failed",
			Dal:       c.DalName,
			Task:      truncateStr(tr.Task, 200),
			Error:     tr.Error,
			Timestamp: now.Format(time.RFC3339),
		})
	} else {
		tr.Status = "done"
		tr.Output = stdout.String()
		log.Printf("[task] %s done (%d bytes)", tr.ID, len(tr.Output))

		dispatchWebhook(WebhookEvent{
			Event:      "task_complete",
			Dal:        c.DalName,
			Task:       truncateStr(tr.Task, 200),
			OutputSize: len(tr.Output),
			Timestamp:  now.Format(time.RFC3339),
		})
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
