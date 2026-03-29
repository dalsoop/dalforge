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
	ID        string      `json:"id"`
	Dal       string      `json:"dal"`
	Task      string      `json:"task"`
	Output    string      `json:"output"`
	Error     string      `json:"error,omitempty"`
	Status    string      `json:"status"` // "running", "done", "failed", "blocked", "noop"
	StartedAt time.Time   `json:"started_at"`
	DoneAt    *time.Time  `json:"done_at,omitempty"`
	Events    []taskEvent `json:"events,omitempty"`
	// Post-task verification
	GitDiff    string `json:"git_diff,omitempty"`    // workspace git diff after task
	GitChanges int    `json:"git_changes,omitempty"` // number of files changed
	Verified   string `json:"verified,omitempty"`    // "yes", "no_changes", "skipped"
	// Post-task build/test verification
	Completion *CompletionResult `json:"completion,omitempty"`
}

type taskEvent struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}

type TaskMetadataUpdate struct {
	GitDiff    string            `json:"git_diff"`
	GitChanges int               `json:"git_changes"`
	Verified   string            `json:"verified"`
	Completion *CompletionResult `json:"completion"`
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
	t.appendEvent("accepted", "Task accepted and running")
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

func (s *taskStore) Complete(id, status, output, errMsg string) *taskResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr := s.tasks[id]
	if tr == nil {
		return nil
	}
	now := time.Now().UTC()
	tr.Status = status
	tr.Output = output
	tr.Error = errMsg
	tr.DoneAt = &now
	message := taskStatusMessage(status, errMsg)
	if message != "" {
		tr.appendEvent(status, message)
	}
	return tr
}

func (s *taskStore) AddEvent(id, kind, message string) *taskResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr := s.tasks[id]
	if tr == nil {
		return nil
	}
	tr.appendEvent(kind, message)
	return tr
}

func (s *taskStore) UpdateMetadata(id string, update TaskMetadataUpdate) *taskResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr := s.tasks[id]
	if tr == nil {
		return nil
	}
	tr.GitDiff = update.GitDiff
	tr.GitChanges = update.GitChanges
	tr.Verified = update.Verified
	tr.Completion = update.Completion
	return tr
}

func (tr *taskResult) appendEvent(kind, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	tr.Events = append(tr.Events, taskEvent{
		At:      time.Now().UTC(),
		Kind:    kind,
		Message: message,
	})
}

func taskStatusMessage(status, errMsg string) string {
	switch status {
	case "done":
		return "Task completed"
	case "blocked":
		if strings.TrimSpace(errMsg) != "" {
			return "Task blocked: " + errMsg
		}
		return "Task blocked"
	case "failed":
		if strings.TrimSpace(errMsg) != "" {
			return "Task failed: " + errMsg
		}
		return "Task failed"
	case "noop":
		return "Task completed with no changes"
	default:
		return ""
	}
}

// handleTaskStart registers a tracked task without executing it inside the daemon.
// POST /api/task/start
func (d *Daemon) handleTaskStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dal  string `json:"dal"`
		Task string `json:"task"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Dal == "" || req.Task == "" {
		http.Error(w, "dal and task are required", http.StatusBadRequest)
		return
	}
	tr := d.tasks.New(req.Dal, req.Task)
	respondJSON(w, http.StatusAccepted, map[string]string{
		"task_id": tr.ID,
		"status":  tr.Status,
	})
}

// handleTaskFinish marks a previously registered tracked task as complete.
// POST /api/task/{id}/finish
func (d *Daemon) handleTaskFinish(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Status string `json:"status"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Status == "" {
		http.Error(w, "status is required", http.StatusBadRequest)
		return
	}
	tr := d.tasks.Complete(id, req.Status, req.Output, req.Error)
	if tr == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	respondJSON(w, http.StatusOK, tr)
}

// handleTaskEvent appends an event to a previously registered tracked task.
// POST /api/task/{id}/event
func (d *Daemon) handleTaskEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	tr := d.tasks.AddEvent(id, strings.TrimSpace(req.Kind), req.Message)
	if tr == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	respondJSON(w, http.StatusOK, tr)
}

// handleTaskMetadata updates verification metadata for a tracked task.
// POST /api/task/{id}/metadata
func (d *Daemon) handleTaskMetadata(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req TaskMetadataUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	tr := d.tasks.UpdateMetadata(id, req)
	if tr == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	respondJSON(w, http.StatusOK, tr)
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

	// Role-aware warning: member dals should receive tasks via leader routing
	if c.Role == "member" {
		log.Printf("[scope] ⚠️ direct task to member %q — prefer leader routing via dalcli-leader assign", req.Dal)
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
		// Determine allowed tools based on role and DAL_EXTRA_BASH.
		// Use a shell snippet that reads the container's env at runtime.
		claudeCmd := fmt.Sprintf(
			`cd %s && if [ "$DAL_ROLE" = "leader" ]; then `+
				`TOOLS="Bash Read Glob Grep"; `+
				`elif [ "$DAL_EXTRA_BASH" = "*" ]; then `+
				`TOOLS="Bash Read Write Glob Grep Edit"; `+
				`else TOOLS="Bash(git:*,gh:*) Read Write Glob Grep Edit"; fi && `+
				`claude -p --allowedTools "$TOOLS"`,
			containerWorkDir)
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

	durationMs := now.Sub(tr.StartedAt).Milliseconds()

	if err != nil {
		tr.Status = "failed"
		tr.Output = stdout.String()
		tr.Error = fmt.Sprintf("%v: %s", err, stderr.String())
		log.Printf("[task] %s failed: %v", tr.ID, err)

		// Record feedback
		d.feedback.Add(c.DalName, tr.ID, tr.Task, "failure", tr.Error, 0, durationMs)

		// Parse and record token usage even on failure
		usage := ParseTokenUsage(tr.Output + "\n" + tr.Error)
		if usage.InputTokens > 0 || usage.OutputTokens > 0 {
			model := c.Player
			d.costs.Add(c.DalName, d.serviceRepo, tr.ID, model, usage.InputTokens, usage.OutputTokens)
			log.Printf("[cost-tracker] %s (failed): input=%d output=%d model=%s", tr.ID, usage.InputTokens, usage.OutputTokens, model)
		}

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

		// Post-task verification: check git diff in workspace
		verifyTaskChanges(c.ContainerID, tr)

		// Post-task completion: run go build + go test
		runCompletion(c.ContainerID, tr)

		// Record feedback
		d.feedback.Add(c.DalName, tr.ID, tr.Task, "success", "", tr.GitChanges, durationMs)

		// Parse and record token usage
		usage := ParseTokenUsage(tr.Output)
		if usage.InputTokens > 0 || usage.OutputTokens > 0 {
			model := c.Player // default to player name
			d.costs.Add(c.DalName, d.serviceRepo, tr.ID, model, usage.InputTokens, usage.OutputTokens)
			log.Printf("[cost-tracker] %s: input=%d output=%d model=%s", tr.ID, usage.InputTokens, usage.OutputTokens, model)
		}

		log.Printf("[task] %s done (%d bytes, verified=%s, changes=%d)", tr.ID, len(tr.Output), tr.Verified, tr.GitChanges)

		dispatchWebhook(WebhookEvent{
			Event:      "task_complete",
			Dal:        c.DalName,
			Task:       truncateStr(tr.Task, 200),
			OutputSize: len(tr.Output),
			Timestamp:  now.Format(time.RFC3339),
		})
	}
}

// verifyTaskChanges runs git diff inside the container to verify actual file changes.
func verifyTaskChanges(containerID string, tr *taskResult) {
	diffCmd := exec.Command("docker", "exec", containerID,
		"bash", "-c", fmt.Sprintf("cd %s && git diff --stat HEAD 2>/dev/null; git diff --stat --cached HEAD 2>/dev/null; git status --porcelain 2>/dev/null", containerWorkDir))
	diffOut, err := diffCmd.Output()
	if err != nil {
		tr.Verified = "skipped"
		return
	}

	diff := strings.TrimSpace(string(diffOut))
	if diff == "" {
		tr.Verified = "no_changes"
		tr.GitDiff = ""
		tr.GitChanges = 0
	} else {
		tr.Verified = "yes"
		tr.GitDiff = truncateStr(diff, 2000)
		// Count changed files from porcelain output
		for _, line := range strings.Split(diff, "\n") {
			line = strings.TrimSpace(line)
			if len(line) >= 2 && (line[0] == 'M' || line[0] == 'A' || line[0] == 'D' || line[0] == '?' || line[0] == 'R') {
				tr.GitChanges++
			}
		}
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
