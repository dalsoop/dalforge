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
	d := New(":0", "/tmp/test", "/tmp/repo", nil)
	body := `{"dal":"nonexistent","task":"hello"}`
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleTask(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleTask_MissingFields(t *testing.T) {
	d := New(":0", "/tmp/test", "/tmp/repo", nil)
	body := `{"dal":"","task":""}`
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleTask(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleTaskList_Empty(t *testing.T) {
	d := New(":0", "/tmp/test", "/tmp/repo", nil)
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
	d := New(":0", "/tmp/test", "/tmp/repo", nil)
	req := httptest.NewRequest("GET", "/api/task/task-9999", nil)
	req.SetPathValue("id", "task-9999")
	w := httptest.NewRecorder()
	d.handleTaskStatus(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
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

func TestMessageFallback_NoMM(t *testing.T) {
	d := New(":0", "/tmp/test", "/tmp/repo", nil)
	// No MM configured, no running dals → should return 503
	body := `{"from":"host","message":"test"}`
	req := httptest.NewRequest("POST", "/api/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleMessage(w, req)
	if w.Code != 503 {
		t.Errorf("expected 503, got %d", w.Code)
	}
}
