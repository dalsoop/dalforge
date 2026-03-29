package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskStore_New(t *testing.T) {
	s := newTaskStore()
	tr := s.New("dev", "go test ./...")
	if tr.ID != "task-0001" {
		t.Errorf("expected task-0001, got %s", tr.ID)
	}
	if tr.Dal != "dev" {
		t.Errorf("expected dal=dev, got %s", tr.Dal)
	}
	if tr.Status != "running" {
		t.Errorf("expected status=running, got %s", tr.Status)
	}
	if len(tr.Events) != 1 {
		t.Fatalf("expected initial event, got %d", len(tr.Events))
	}
	if tr.Events[0].Kind != "accepted" {
		t.Fatalf("expected initial accepted event, got %q", tr.Events[0].Kind)
	}
}

func TestTaskStore_Get(t *testing.T) {
	s := newTaskStore()
	tr := s.New("dev", "go test ./...")
	got := s.Get(tr.ID)
	if got == nil {
		t.Fatal("expected task, got nil")
	}
	if got.Task != "go test ./..." {
		t.Errorf("expected 'go test ./...', got %q", got.Task)
	}
}

func TestTaskStore_GetMissing(t *testing.T) {
	s := newTaskStore()
	got := s.Get("task-9999")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestTaskStore_List(t *testing.T) {
	s := newTaskStore()
	s.New("dev", "task1")
	s.New("leader", "task2")
	list := s.List()
	if len(list) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(list))
	}
}

func TestTaskStore_Eviction(t *testing.T) {
	s := newTaskStore()
	for i := 0; i < 55; i++ {
		tr := s.New("dev", "task")
		tr.Status = "done" // mark as completed so it can be evicted
	}
	list := s.List()
	if len(list) > 51 {
		t.Errorf("expected <=51 tasks after eviction, got %d", len(list))
	}
}

func TestHandleTask_NoDal(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	body := `{"dal":"nonexistent","task":"hello"}`
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleTask(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleTask_MissingFields(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	body := `{"dal":"","task":""}`
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleTask(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleTaskList_Empty(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	d.handleTaskList(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var result []*taskResult
	json.NewDecoder(w.Body).Decode(&result)
	// nil or empty is fine
}

func TestHandleTaskStatus_NotFound(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	req := httptest.NewRequest("GET", "/api/task/task-9999", nil)
	req.SetPathValue("id", "task-9999")
	w := httptest.NewRecorder()
	d.handleTaskStatus(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleTaskStartAndFinish(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)

	startReq := httptest.NewRequest("POST", "/api/task/start", strings.NewReader(`{"dal":"leader","task":"triage issue"}`))
	startW := httptest.NewRecorder()
	d.handleTaskStart(startW, startReq)
	if startW.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want 202", startW.Code)
	}
	var started map[string]string
	if err := json.NewDecoder(startW.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	if started["task_id"] == "" {
		t.Fatal("expected task_id")
	}

	finishReq := httptest.NewRequest("POST", "/api/task/"+started["task_id"]+"/finish", strings.NewReader(`{"status":"done","output":"ok","error":""}`))
	finishReq.SetPathValue("id", started["task_id"])
	finishW := httptest.NewRecorder()
	d.handleTaskFinish(finishW, finishReq)
	if finishW.Code != http.StatusOK {
		t.Fatalf("finish status = %d, want 200", finishW.Code)
	}

	tr := d.tasks.Get(started["task_id"])
	if tr == nil {
		t.Fatal("expected tracked task")
	}
	if tr.Status != "done" {
		t.Fatalf("status = %s, want done", tr.Status)
	}
	if tr.Output != "ok" {
		t.Fatalf("output = %q, want ok", tr.Output)
	}
	if tr.DoneAt == nil {
		t.Fatal("expected DoneAt to be set")
	}
	if len(tr.Events) < 2 {
		t.Fatalf("expected completion event, got %d events", len(tr.Events))
	}
	if tr.Events[len(tr.Events)-1].Kind != "done" {
		t.Fatalf("expected final done event, got %q", tr.Events[len(tr.Events)-1].Kind)
	}
}

func TestHandleTaskEvent(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	tr := d.tasks.New("leader", "triage issue")

	req := httptest.NewRequest("POST", "/api/task/"+tr.ID+"/event", strings.NewReader(`{"kind":"self_repair","message":"Retrying after fix"}`))
	req.SetPathValue("id", tr.ID)
	w := httptest.NewRecorder()
	d.handleTaskEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	updated := d.tasks.Get(tr.ID)
	if updated == nil {
		t.Fatal("expected tracked task")
	}
	if got := updated.Events[len(updated.Events)-1]; got.Kind != "self_repair" || got.Message != "Retrying after fix" {
		t.Fatalf("unexpected event: %+v", got)
	}
}

func TestHandleTaskMetadata(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	tr := d.tasks.New("leader", "triage issue")

	req := httptest.NewRequest("POST", "/api/task/"+tr.ID+"/metadata", strings.NewReader(`{"git_diff":"M README.md","git_changes":1,"verified":"yes","completion":{"build_ok":true,"test_ok":false,"duration":"1.2s"}}`))
	req.SetPathValue("id", tr.ID)
	w := httptest.NewRecorder()
	d.handleTaskMetadata(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	updated := d.tasks.Get(tr.ID)
	if updated == nil {
		t.Fatal("expected tracked task")
	}
	if updated.Verified != "yes" || updated.GitChanges != 1 {
		t.Fatalf("unexpected metadata: %+v", updated)
	}
	if updated.Completion == nil || updated.Completion.Duration != "1.2s" {
		t.Fatalf("unexpected completion metadata: %+v", updated.Completion)
	}
}

func TestTruncateStr(t *testing.T) {
	if truncateStr("hello", 10) != "hello" {
		t.Error("should not truncate short string")
	}
	if truncateStr("hello world", 5) != "hello..." {
		t.Errorf("got %q", truncateStr("hello world", 5))
	}
}

func TestVerifyTaskChanges_NoContainer(t *testing.T) {
	tr := &taskResult{ID: "test-001", Status: "done"}
	verifyTaskChanges("nonexistent-container-id", tr)
	if tr.Verified != "skipped" {
		t.Errorf("expected skipped for invalid container, got %q", tr.Verified)
	}
}

func TestTaskResult_VerifiedFields(t *testing.T) {
	tr := &taskResult{
		ID:         "test-002",
		Verified:   "no_changes",
		GitChanges: 0,
	}
	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	// Verify JSON serialization includes verified field
	if !strings.Contains(string(data), `"verified":"no_changes"`) {
		t.Errorf("JSON should contain verified field: %s", data)
	}
	// git_diff should be omitted when empty
	if strings.Contains(string(data), `"git_diff"`) {
		t.Errorf("git_diff should be omitted when empty: %s", data)
	}
}

func TestTaskResult_WithChanges(t *testing.T) {
	tr := &taskResult{
		ID:         "test-003",
		Verified:   "yes",
		GitDiff:    "M  README.md\nA  new-file.go",
		GitChanges: 2,
	}
	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"git_changes":2`) {
		t.Errorf("expected git_changes:2 in JSON: %s", data)
	}
	if !strings.Contains(string(data), `"verified":"yes"`) {
		t.Errorf("expected verified:yes in JSON: %s", data)
	}
}

func TestMessageFallback_NoMM(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	// No MM configured, no running dals → should return 503
	body := `{"from":"host","message":"test"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleMessage(w, req)
	if w.Code != 503 {
		t.Errorf("expected 503, got %d", w.Code)
	}
}
