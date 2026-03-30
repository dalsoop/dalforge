package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// IssueWorkflow tracks the state of an issue-driven automated workflow.
// Flow: dalroot creates issue → tell → leader detects → wake member → assign → PR → notify dalroot.
type IssueWorkflow struct {
	ID        string          `json:"id"`
	IssueID   string          `json:"issue_id"`
	Member    string          `json:"member"`
	Task      string          `json:"task"`
	Status    string          `json:"status"` // "pending", "waking", "assigned", "working", "done", "failed"
	Phase     string          `json:"phase"`
	TaskID    string          `json:"task_id,omitempty"`
	PRUrl     string          `json:"pr_url,omitempty"`
	Error     string          `json:"error,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	DoneAt    *time.Time      `json:"done_at,omitempty"`
	Events    []workflowEvent `json:"events"`
}

type workflowEvent struct {
	At      time.Time `json:"at"`
	Phase   string    `json:"phase"`
	Message string    `json:"message"`
}

// issueWorkflowStore manages active and completed issue workflows.
type issueWorkflowStore struct {
	mu        sync.RWMutex
	workflows map[string]*IssueWorkflow
}

const maxWorkflows = 50

func newIssueWorkflowStore() *issueWorkflowStore {
	return &issueWorkflowStore{workflows: make(map[string]*IssueWorkflow)}
}

func (s *issueWorkflowStore) Create(issueID, member, task string) *IssueWorkflow {
	s.mu.Lock()
	defer s.mu.Unlock()
	wf := &IssueWorkflow{
		ID:        newPrefixedUUID("iwf"),
		IssueID:   issueID,
		Member:    member,
		Task:      task,
		Status:    "pending",
		Phase:     "created",
		StartedAt: time.Now().UTC(),
	}
	wf.addEvent("created", fmt.Sprintf("Issue workflow created for issue #%s, member=%s", issueID, member))
	s.workflows[wf.ID] = wf

	// Evict oldest completed/failed if over limit
	if len(s.workflows) > maxWorkflows {
		var oldest string
		var oldestTime time.Time
		for id, w := range s.workflows {
			if w.Status == "done" || w.Status == "failed" {
				if oldest == "" || w.StartedAt.Before(oldestTime) {
					oldest = id
					oldestTime = w.StartedAt
				}
			}
		}
		if oldest != "" {
			delete(s.workflows, oldest)
		}
	}
	return wf
}

func (s *issueWorkflowStore) Get(id string) *IssueWorkflow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workflows[id]
}

func (s *issueWorkflowStore) List() []*IssueWorkflow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*IssueWorkflow, 0, len(s.workflows))
	for _, wf := range s.workflows {
		result = append(result, wf)
	}
	return result
}

func (s *issueWorkflowStore) FindByIssue(issueID string) *IssueWorkflow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, wf := range s.workflows {
		if wf.IssueID == issueID && wf.Status != "done" && wf.Status != "failed" {
			return wf
		}
	}
	return nil
}

func (wf *IssueWorkflow) addEvent(phase, message string) {
	wf.Events = append(wf.Events, workflowEvent{
		At:      time.Now().UTC(),
		Phase:   phase,
		Message: message,
	})
	wf.Phase = phase
}

func (wf *IssueWorkflow) fail(err string) {
	wf.Status = "failed"
	wf.Error = err
	now := time.Now().UTC()
	wf.DoneAt = &now
	wf.addEvent("failed", err)
}

func (wf *IssueWorkflow) complete(prURL string) {
	wf.Status = "done"
	wf.PRUrl = prURL
	now := time.Now().UTC()
	wf.DoneAt = &now
	wf.addEvent("done", "Workflow completed")
}

// handleIssueWorkflow orchestrates the full issue workflow:
// 1. Wake member with issue branch
// 2. Assign task to member
// 3. Track task completion
// 4. Notify dalroot on completion
// POST /api/issue-workflow
func (d *Daemon) handleIssueWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IssueID      string `json:"issue_id"`
		Member       string `json:"member"`
		Task         string `json:"task"`
		CallbackPane string `json:"callback_pane"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.IssueID == "" || req.Member == "" || req.Task == "" {
		http.Error(w, "issue_id, member, and task are required", http.StatusBadRequest)
		return
	}

	// Check for duplicate active workflow
	if existing := d.issueWorkflows.FindByIssue(req.IssueID); existing != nil {
		respondJSON(w, http.StatusConflict, map[string]string{
			"error":       "workflow already active for this issue",
			"workflow_id": existing.ID,
			"status":      existing.Status,
		})
		return
	}

	wf := d.issueWorkflows.Create(req.IssueID, req.Member, req.Task)

	// Run orchestration asynchronously
	go d.runIssueWorkflow(wf, req.CallbackPane)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"workflow_id": wf.ID,
		"status":      wf.Status,
		"issue_id":    wf.IssueID,
	})
}

// handleIssueWorkflowStatus returns the status of an issue workflow.
// GET /api/issue-workflow/{id}
func (d *Daemon) handleIssueWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf := d.issueWorkflows.Get(id)
	if wf == nil {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}
	respondJSON(w, http.StatusOK, wf)
}

// handleIssueWorkflowList returns all issue workflows.
// GET /api/issue-workflows
func (d *Daemon) handleIssueWorkflowList(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, d.issueWorkflows.List())
}

// runIssueWorkflow executes the full issue → member → PR → notify pipeline.
func (d *Daemon) runIssueWorkflow(wf *IssueWorkflow, callbackPane string) {
	log.Printf("[issue-workflow] starting %s: issue=#%s member=%s", wf.ID, wf.IssueID, wf.Member)

	// Phase 1: Wake member with issue branch
	wf.Status = "waking"
	wf.addEvent("wake", fmt.Sprintf("Waking member %s with issue #%s", wf.Member, wf.IssueID))

	wakeResult, err := d.wakeForIssue(wf.Member, wf.IssueID)
	if err != nil {
		wf.fail(fmt.Sprintf("wake failed: %v", err))
		log.Printf("[issue-workflow] %s wake failed: %v", wf.ID, err)
		d.notifyWorkflowResult(wf, callbackPane)
		return
	}
	containerID := wakeResult
	log.Printf("[issue-workflow] %s: member %s awake (container=%s)", wf.ID, wf.Member, truncateStr(containerID, 12))

	// Phase 2: Assign task to member via direct task execution
	wf.Status = "assigned"
	wf.addEvent("assign", fmt.Sprintf("Assigning task to %s: %s", wf.Member, truncateStr(wf.Task, 80)))

	d.mu.RLock()
	c, ok := d.containers[wf.Member]
	d.mu.RUnlock()
	if !ok {
		// Check for instance variants (dev-2, dev-3, etc.)
		d.mu.RLock()
		for name, container := range d.containers {
			if strings.HasPrefix(name, wf.Member) {
				c = container
				ok = true
				break
			}
		}
		d.mu.RUnlock()
	}
	if !ok {
		wf.fail(fmt.Sprintf("member %s not found after wake", wf.Member))
		log.Printf("[issue-workflow] %s member not found after wake", wf.ID)
		d.notifyWorkflowResult(wf, callbackPane)
		return
	}

	// Phase 3: Execute task in container
	wf.Status = "working"
	wf.addEvent("working", "Task execution started")

	tr := d.tasks.New(c.DalName, wf.Task)
	tr.CallbackPane = callbackPane
	wf.TaskID = tr.ID

	d.execTaskInContainer(c, tr)

	// Phase 4: Check result
	if tr.Status == "failed" {
		wf.fail(fmt.Sprintf("task failed: %s", tr.Error))
		log.Printf("[issue-workflow] %s task failed: %s", wf.ID, tr.Error)
		d.notifyWorkflowResult(wf, callbackPane)
		return
	}

	// Phase 5: Complete — extract PR URL if present in output
	prURL := extractPRUrl(tr.Output)
	wf.complete(prURL)
	log.Printf("[issue-workflow] %s completed (pr=%s)", wf.ID, prURL)

	d.notifyWorkflowResult(wf, callbackPane)
}

// wakeForIssue wakes a member dal with an issue branch.
// Returns the container ID on success.
func (d *Daemon) wakeForIssue(member, issueID string) (string, error) {
	path := fmt.Sprintf("/api/wake/%s?issue=%s", member, issueID)
	wakeReq, _ := http.NewRequest("POST", path, nil)
	wakeReq.SetPathValue("name", member)
	q := wakeReq.URL.Query()
	q.Set("issue", issueID)
	wakeReq.URL.RawQuery = q.Encode()

	rec := &captureResponseWriter{}
	d.handleWake(rec, wakeReq)

	if rec.code >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", rec.code, rec.body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(rec.body.Bytes(), &result); err != nil {
		return "", fmt.Errorf("decode wake response: %v", err)
	}
	cid, _ := result["container_id"].(string)
	if cid == "" {
		return "", fmt.Errorf("no container_id in wake response")
	}
	return cid, nil
}

// notifyWorkflowResult sends a notification about the workflow result.
func (d *Daemon) notifyWorkflowResult(wf *IssueWorkflow, callbackPane string) {
	// Post to bridge if available
	if d.bridgeURL != "" {
		var msg string
		switch wf.Status {
		case "done":
			msg = fmt.Sprintf("[issue-workflow] Issue #%s completed by %s", wf.IssueID, wf.Member)
			if wf.PRUrl != "" {
				msg += fmt.Sprintf(" — PR: %s", wf.PRUrl)
			}
		case "failed":
			msg = fmt.Sprintf("[issue-workflow] Issue #%s failed: %s", wf.IssueID, wf.Error)
		}
		if msg != "" {
			if err := d.bridgePost(msg, "dalcenter"); err != nil {
				log.Printf("[issue-workflow] bridge notification failed: %v", err)
			}
		}
	}

	// Dispatch webhook
	event := "issue_workflow_complete"
	if wf.Status == "failed" {
		event = "issue_workflow_failed"
	}
	dispatchWebhook(WebhookEvent{
		Event:     event,
		Dal:       wf.Member,
		Task:      fmt.Sprintf("issue #%s: %s", wf.IssueID, truncateStr(wf.Task, 100)),
		PRUrl:     wf.PRUrl,
		Error:     wf.Error,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	// Notify dalroot via callback pane
	if callbackPane != "" {
		var msg string
		if wf.Status == "done" {
			msg = fmt.Sprintf("[%s] issue #%s done", wf.Member, wf.IssueID)
			if wf.PRUrl != "" {
				msg += fmt.Sprintf(" — PR: %s", wf.PRUrl)
			}
		} else {
			msg = fmt.Sprintf("[%s] issue #%s failed: %s", wf.Member, wf.IssueID, wf.Error)
		}
		notifyDalrootDirect(msg, callbackPane, d.serviceRepo)
	}
}

// notifyDalrootDirect sends a notification to dalroot without a taskResult.
func notifyDalrootDirect(msg, callbackPane, repo string) {
	cmd := exec.Command("notify-dalroot", repo, msg, callbackPane)
	if err := cmd.Run(); err != nil {
		log.Printf("[notify] dalroot notification failed: %v", err)
	}
}

// extractPRUrl attempts to find a GitHub PR URL in task output.
func extractPRUrl(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "github.com") && strings.Contains(line, "/pull/") {
			for _, word := range strings.Fields(line) {
				if strings.Contains(word, "github.com") && strings.Contains(word, "/pull/") {
					return strings.Trim(word, "()[]<>\"'")
				}
			}
		}
	}
	return ""
}

// captureResponseWriter captures HTTP response for internal handler calls.
type captureResponseWriter struct {
	code   int
	body   bytes.Buffer
	header http.Header
}

func (w *captureResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *captureResponseWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func (w *captureResponseWriter) WriteHeader(code int) {
	w.code = code
}
