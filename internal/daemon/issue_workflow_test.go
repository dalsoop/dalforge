package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── Store Tests ──────────────────────────────────────────────

func TestIssueWorkflowStore_Create(t *testing.T) {
	s := newIssueWorkflowStore()
	wf := s.Create("529", "dev", "implement feature X")

	if wf.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if !strings.HasPrefix(wf.ID, "iwf-") {
		t.Fatalf("expected ID to start with iwf-, got %s", wf.ID)
	}
	if wf.IssueID != "529" {
		t.Fatalf("expected issue_id=529, got %s", wf.IssueID)
	}
	if wf.Member != "dev" {
		t.Fatalf("expected member=dev, got %s", wf.Member)
	}
	if wf.Status != "pending" {
		t.Fatalf("expected status=pending, got %s", wf.Status)
	}
	if len(wf.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(wf.Events))
	}
}

func TestIssueWorkflowStore_Get(t *testing.T) {
	s := newIssueWorkflowStore()
	wf := s.Create("100", "dev", "task")

	got := s.Get(wf.ID)
	if got == nil {
		t.Fatal("expected to find workflow")
	}
	if got.ID != wf.ID {
		t.Fatalf("expected ID=%s, got %s", wf.ID, got.ID)
	}

	if s.Get("nonexistent") != nil {
		t.Fatal("expected nil for nonexistent ID")
	}
}

func TestIssueWorkflowStore_List(t *testing.T) {
	s := newIssueWorkflowStore()
	s.Create("1", "dev", "task1")
	s.Create("2", "dev", "task2")

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(list))
	}
}

func TestIssueWorkflowStore_FindByIssue(t *testing.T) {
	s := newIssueWorkflowStore()
	wf := s.Create("529", "dev", "task")

	found := s.FindByIssue("529")
	if found == nil {
		t.Fatal("expected to find workflow for issue 529")
	}
	if found.ID != wf.ID {
		t.Fatalf("expected ID=%s, got %s", wf.ID, found.ID)
	}

	if s.FindByIssue("999") != nil {
		t.Fatal("expected nil for issue 999")
	}

	// Completed workflow should not be found
	wf.complete("")
	if s.FindByIssue("529") != nil {
		t.Fatal("expected nil for completed workflow")
	}
}

func TestIssueWorkflowStore_FindByIssue_SkipsFailed(t *testing.T) {
	s := newIssueWorkflowStore()
	wf := s.Create("529", "dev", "task")
	wf.fail("some error")

	if s.FindByIssue("529") != nil {
		t.Fatal("expected nil for failed workflow")
	}
}

func TestIssueWorkflowStore_Eviction(t *testing.T) {
	s := newIssueWorkflowStore()

	for i := 0; i < maxWorkflows+5; i++ {
		wf := s.Create(fmt.Sprintf("%d", i), "dev", "task")
		wf.complete("")
	}

	s.mu.RLock()
	count := len(s.workflows)
	s.mu.RUnlock()

	if count > maxWorkflows+1 {
		t.Fatalf("expected at most %d workflows after eviction, got %d", maxWorkflows+1, count)
	}
}

// ── Workflow State Tests ──────────────────────────────────────

func TestIssueWorkflow_Fail(t *testing.T) {
	s := newIssueWorkflowStore()
	wf := s.Create("1", "dev", "task")

	wf.fail("something went wrong")

	if wf.Status != "failed" {
		t.Fatalf("expected status=failed, got %s", wf.Status)
	}
	if wf.Error != "something went wrong" {
		t.Fatalf("expected error message, got %s", wf.Error)
	}
	if wf.DoneAt == nil {
		t.Fatal("expected DoneAt to be set")
	}
	if wf.Phase != "failed" {
		t.Fatalf("expected phase=failed, got %s", wf.Phase)
	}
}

func TestIssueWorkflow_Complete(t *testing.T) {
	s := newIssueWorkflowStore()
	wf := s.Create("1", "dev", "task")

	wf.complete("https://github.com/org/repo/pull/42")

	if wf.Status != "done" {
		t.Fatalf("expected status=done, got %s", wf.Status)
	}
	if wf.PRUrl != "https://github.com/org/repo/pull/42" {
		t.Fatalf("expected PR URL, got %s", wf.PRUrl)
	}
	if wf.DoneAt == nil {
		t.Fatal("expected DoneAt to be set")
	}
}

// ── HTTP Handler Tests ──────────────────────────────────────

func TestHandleIssueWorkflow_BadJSON(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
	}

	req := httptest.NewRequest("POST", "/api/issue-workflow", strings.NewReader("not-json"))
	w := httptest.NewRecorder()

	d.handleIssueWorkflow(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleIssueWorkflow_MissingFields(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
	}

	tests := []struct {
		name string
		body string
	}{
		{"missing issue_id", `{"member":"dev","task":"do stuff"}`},
		{"missing member", `{"issue_id":"529","task":"do stuff"}`},
		{"missing task", `{"issue_id":"529","member":"dev"}`},
		{"all empty", `{"issue_id":"","member":"","task":""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/issue-workflow", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			d.handleIssueWorkflow(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %s, got %d", tt.name, w.Code)
			}
		})
	}
}

func TestHandleIssueWorkflow_DuplicateDetection(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
		localdalRoot:   t.TempDir(),
		containers:     map[string]*Container{},
		tasks:          newTaskStore(),
		feedback:       newFeedbackStore(),
		costs:          newCostStore(),
	}

	// Create an active workflow manually
	wf := d.issueWorkflows.Create("529", "dev", "existing task")
	wf.Status = "working"

	// Try to create another for the same issue
	body := `{"issue_id":"529","member":"dev","task":"new task"}`
	req := httptest.NewRequest("POST", "/api/issue-workflow", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleIssueWorkflow(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["workflow_id"] != wf.ID {
		t.Fatalf("expected existing workflow ID in response, got %s", resp["workflow_id"])
	}
}

func TestHandleIssueWorkflow_AcceptsValid(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
		localdalRoot:   t.TempDir(),
		containers:     map[string]*Container{},
		tasks:          newTaskStore(),
		feedback:       newFeedbackStore(),
		costs:          newCostStore(),
	}

	body := `{"issue_id":"529","member":"dev","task":"implement feature"}`
	req := httptest.NewRequest("POST", "/api/issue-workflow", strings.NewReader(body))
	w := httptest.NewRecorder()

	d.handleIssueWorkflow(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["workflow_id"] == "" {
		t.Fatal("expected workflow_id in response")
	}
	if resp["issue_id"] != "529" {
		t.Fatalf("expected issue_id=529, got %s", resp["issue_id"])
	}

	// Wait for async goroutine (will fail — no Docker, no dal.cue)
	time.Sleep(100 * time.Millisecond)

	wf := d.issueWorkflows.Get(resp["workflow_id"])
	if wf == nil {
		t.Fatal("expected workflow to exist")
	}
	if wf.Status != "failed" {
		t.Logf("workflow status: %s (may still be running)", wf.Status)
	}
}

func TestHandleIssueWorkflowStatus_NotFound(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
	}

	req := httptest.NewRequest("GET", "/api/issue-workflow/iwf-nonexistent", nil)
	req.SetPathValue("id", "iwf-nonexistent")
	w := httptest.NewRecorder()

	d.handleIssueWorkflowStatus(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleIssueWorkflowStatus_Found(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
	}
	wf := d.issueWorkflows.Create("100", "dev", "task")

	req := httptest.NewRequest("GET", "/api/issue-workflow/"+wf.ID, nil)
	req.SetPathValue("id", wf.ID)
	w := httptest.NewRecorder()

	d.handleIssueWorkflowStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp IssueWorkflow
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.IssueID != "100" {
		t.Fatalf("expected issue_id=100, got %s", resp.IssueID)
	}
	if resp.Member != "dev" {
		t.Fatalf("expected member=dev, got %s", resp.Member)
	}
}

func TestHandleIssueWorkflowList_Empty(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
	}

	req := httptest.NewRequest("GET", "/api/issue-workflows", nil)
	w := httptest.NewRecorder()

	d.handleIssueWorkflowList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp []*IssueWorkflow
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Fatalf("expected empty list, got %d", len(resp))
	}
}

func TestHandleIssueWorkflowList_WithEntries(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
	}
	d.issueWorkflows.Create("1", "dev", "task1")
	d.issueWorkflows.Create("2", "qa", "task2")

	req := httptest.NewRequest("GET", "/api/issue-workflows", nil)
	w := httptest.NewRecorder()

	d.handleIssueWorkflowList(w, req)

	var resp []*IssueWorkflow
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(resp))
	}
}

// ── captureResponseWriter Tests ──────────────────────────────

func TestCaptureResponseWriter(t *testing.T) {
	w := &captureResponseWriter{}

	w.WriteHeader(201)
	w.Write([]byte("hello"))
	w.Write([]byte(" world"))

	if w.code != 201 {
		t.Fatalf("expected code=201, got %d", w.code)
	}
	if w.body.String() != "hello world" {
		t.Fatalf("expected body='hello world', got %q", w.body.String())
	}

	h := w.Header()
	if h == nil {
		t.Fatal("expected non-nil header")
	}
	h2 := w.Header()
	h.Set("X-Test", "val")
	if h2.Get("X-Test") != "val" {
		t.Fatal("expected same header map")
	}
}

// ── Integration: Full Workflow with Mock Bridge ──────────────

func TestIssueWorkflow_FullFlow_WakeFailure(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
		localdalRoot:   t.TempDir(),
		containers:     map[string]*Container{},
		tasks:          newTaskStore(),
		feedback:       newFeedbackStore(),
		costs:          newCostStore(),
	}

	wf := d.issueWorkflows.Create("529", "dev", "implement feature")
	d.runIssueWorkflow(wf, "")

	if wf.Status != "failed" {
		t.Fatalf("expected status=failed, got %s", wf.Status)
	}
	if !strings.Contains(wf.Error, "wake failed") {
		t.Fatalf("expected wake failure error, got %s", wf.Error)
	}
	if wf.DoneAt == nil {
		t.Fatal("expected DoneAt to be set")
	}

	hasWakeEvent := false
	hasFailEvent := false
	for _, e := range wf.Events {
		if e.Phase == "wake" {
			hasWakeEvent = true
		}
		if e.Phase == "failed" {
			hasFailEvent = true
		}
	}
	if !hasWakeEvent {
		t.Fatal("expected wake event")
	}
	if !hasFailEvent {
		t.Fatal("expected fail event")
	}
}

func TestIssueWorkflow_FullFlow_WithBridgeNotification(t *testing.T) {
	var bridgeMessages []string
	bridgeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bridgeMessages = append(bridgeMessages, string(body))
		w.Write([]byte(`{"ok":true}`))
	}))
	defer bridgeSrv.Close()

	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
		localdalRoot:   t.TempDir(),
		bridgeURL:      bridgeSrv.URL,
		containers:     map[string]*Container{},
		tasks:          newTaskStore(),
		feedback:       newFeedbackStore(),
		costs:          newCostStore(),
	}

	wf := d.issueWorkflows.Create("529", "dev", "implement feature")
	d.runIssueWorkflow(wf, "")

	if wf.Status != "failed" {
		t.Fatalf("expected status=failed, got %s", wf.Status)
	}
	if len(bridgeMessages) == 0 {
		t.Fatal("expected bridge notification on failure")
	}
	if !strings.Contains(bridgeMessages[0], "issue-workflow") {
		t.Fatalf("expected issue-workflow in bridge message, got %s", bridgeMessages[0])
	}
	if !strings.Contains(bridgeMessages[0], "529") {
		t.Fatalf("expected issue ID 529 in bridge message, got %s", bridgeMessages[0])
	}
}

func TestIssueWorkflow_DuplicateAllowedAfterCompletion(t *testing.T) {
	s := newIssueWorkflowStore()

	wf1 := s.Create("529", "dev", "task1")
	wf1.complete("")

	wf2 := s.Create("529", "dev", "task2")
	if wf2.ID == wf1.ID {
		t.Fatal("expected different workflow IDs")
	}

	found := s.FindByIssue("529")
	if found == nil {
		t.Fatal("expected to find new workflow")
	}
	if found.ID != wf2.ID {
		t.Fatalf("expected new workflow ID=%s, got %s", wf2.ID, found.ID)
	}
}

// ── Integration: HTTP End-to-End ──────────────────────────────

func TestIssueWorkflow_HTTPEndToEnd(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
		localdalRoot:   t.TempDir(),
		containers:     map[string]*Container{},
		tasks:          newTaskStore(),
		feedback:       newFeedbackStore(),
		costs:          newCostStore(),
	}

	// Step 1: Start workflow via HTTP
	body := `{"issue_id":"100","member":"dev","task":"implement auth"}`
	req := httptest.NewRequest("POST", "/api/issue-workflow", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleIssueWorkflow(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("step1: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var startResp map[string]string
	json.NewDecoder(w.Body).Decode(&startResp)
	workflowID := startResp["workflow_id"]

	// Wait for async workflow to complete (fails due to no Docker)
	time.Sleep(200 * time.Millisecond)

	// Step 2: Check status via HTTP
	statusReq := httptest.NewRequest("GET", "/api/issue-workflow/"+workflowID, nil)
	statusReq.SetPathValue("id", workflowID)
	statusW := httptest.NewRecorder()
	d.handleIssueWorkflowStatus(statusW, statusReq)

	if statusW.Code != http.StatusOK {
		t.Fatalf("step2: expected 200, got %d", statusW.Code)
	}

	var statusResp IssueWorkflow
	json.NewDecoder(statusW.Body).Decode(&statusResp)
	if statusResp.Status != "failed" {
		t.Fatalf("step2: expected failed status, got %s", statusResp.Status)
	}
	if statusResp.IssueID != "100" {
		t.Fatalf("step2: expected issue_id=100, got %s", statusResp.IssueID)
	}

	// Step 3: Verify it shows up in the list
	listReq := httptest.NewRequest("GET", "/api/issue-workflows", nil)
	listW := httptest.NewRecorder()
	d.handleIssueWorkflowList(listW, listReq)

	var listResp []*IssueWorkflow
	json.NewDecoder(listW.Body).Decode(&listResp)
	if len(listResp) != 1 {
		t.Fatalf("step3: expected 1 workflow, got %d", len(listResp))
	}

	// Step 4: Starting same issue again should work (previous one failed)
	body2 := `{"issue_id":"100","member":"dev","task":"retry auth"}`
	req2 := httptest.NewRequest("POST", "/api/issue-workflow", strings.NewReader(body2))
	w2 := httptest.NewRecorder()
	d.handleIssueWorkflow(w2, req2)

	if w2.Code != http.StatusAccepted {
		t.Fatalf("step4: expected 202 for retry, got %d: %s", w2.Code, w2.Body.String())
	}
}

// ── Workflow Event Tracking Tests ──────────────────────────────

func TestIssueWorkflow_EventTracking(t *testing.T) {
	wf := &IssueWorkflow{
		ID:        "test-wf",
		StartedAt: time.Now().UTC(),
	}

	wf.addEvent("created", "Workflow created")
	wf.addEvent("wake", "Waking member")
	wf.addEvent("assign", "Task assigned")

	if len(wf.Events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(wf.Events))
	}
	if wf.Events[0].Phase != "created" {
		t.Fatalf("expected first event phase=created, got %s", wf.Events[0].Phase)
	}
	if wf.Events[2].Phase != "assign" {
		t.Fatalf("expected third event phase=assign, got %s", wf.Events[2].Phase)
	}
	if wf.Phase != "assign" {
		t.Fatalf("expected current phase=assign, got %s", wf.Phase)
	}
}

// ── Auth Integration Tests ──────────────────────────────────────

func TestHandleIssueWorkflow_RequiresAuth(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
		apiToken:       "secret",
	}

	body := `{"issue_id":"1","member":"dev","task":"do stuff"}`
	req := httptest.NewRequest("POST", "/api/issue-workflow", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.requireAuth(d.handleIssueWorkflow)(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", w.Code)
	}

	req2 := httptest.NewRequest("POST", "/api/issue-workflow", strings.NewReader(body))
	req2.Header.Set("Authorization", "Bearer secret")
	w2 := httptest.NewRecorder()
	d.requireAuth(d.handleIssueWorkflow)(w2, req2)

	if w2.Code == http.StatusUnauthorized {
		t.Fatal("expected auth to pass with correct token")
	}
}

func TestHandleIssueWorkflowStatus_NoAuth(t *testing.T) {
	d := &Daemon{
		issueWorkflows: newIssueWorkflowStore(),
		apiToken:       "secret",
	}

	req := httptest.NewRequest("GET", "/api/issue-workflow/test", nil)
	req.SetPathValue("id", "test")
	w := httptest.NewRecorder()
	d.handleIssueWorkflowStatus(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatal("read endpoint should not require auth")
	}
}
