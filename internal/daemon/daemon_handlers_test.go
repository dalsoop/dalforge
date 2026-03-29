package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleLogs_DalNotFound(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{},
	}

	req := httptest.NewRequest("GET", "/api/logs/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()

	d.handleLogs(w, req)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleLogs_DalFound(t *testing.T) {
	// handleLogs calls dockerLogs which needs real Docker — we test the lookup only
	d := &Daemon{
		containers: map[string]*Container{
			"dev": {DalName: "dev", ContainerID: "fake-id"},
		},
	}

	req := httptest.NewRequest("GET", "/api/logs/dev", nil)
	req.SetPathValue("name", "dev")
	w := httptest.NewRecorder()

	d.handleLogs(w, req)

	// Will fail because Docker is not available, but should NOT be 404
	if w.Code == 404 {
		t.Fatal("should find the dal, even if Docker fails")
	}
}

func TestHandleRunPage_TaskFound(t *testing.T) {
	d := &Daemon{
		tasks: newTaskStore(),
	}
	tr := d.tasks.New("leader", "triage issue")
	tr.Output = "still running"
	tr.GitDiff = "M  README.md"
	tr.Completion = &CompletionResult{
		BuildOK:    true,
		TestOK:     false,
		Duration:   "2.3s",
		TestOutput: "FAIL test",
	}

	req := httptest.NewRequest("GET", "/runs/"+tr.ID, nil)
	req.SetPathValue("id", tr.ID)
	w := httptest.NewRecorder()

	d.handleRunPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "run "+tr.ID) {
		t.Fatalf("expected run id in body: %s", body)
	}
	if !strings.Contains(body, `fetch("/api/task/" + taskId`) {
		t.Fatalf("expected polling endpoint in body: %s", body)
	}
	if !strings.Contains(body, "Verification") || !strings.Contains(body, "Git Diff") {
		t.Fatalf("expected verification sections in body: %s", body)
	}
	if !strings.Contains(body, tr.GitDiff) || !strings.Contains(body, tr.Completion.TestOutput) {
		t.Fatalf("expected task detail content in body: %s", body)
	}
}

func TestHandleRunPage_TaskNotFound(t *testing.T) {
	d := &Daemon{
		tasks: newTaskStore(),
	}
	req := httptest.NewRequest("GET", "/runs/task-404", nil)
	req.SetPathValue("id", "task-404")
	w := httptest.NewRecorder()

	d.handleRunPage(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestMmPost_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	}))
	defer srv.Close()

	_, err := mmPost(srv.URL, "token", "/api/v4/posts", `{"message":"test"}`)
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestMmPost_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"post-1"}`))
	}))
	defer srv.Close()

	resp, err := mmPost(srv.URL, "token", "/api/v4/posts", `{"message":"test"}`)
	if err != nil {
		t.Fatalf("mmPost: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}
}

func TestDalCuePath(t *testing.T) {
	d := &Daemon{
		localdalRoot: "/root/project/.dal",
	}
	got := d.dalCuePath("dev")
	want := "/root/project/.dal/dev/dal.cue"
	if got != want {
		t.Errorf("dalCuePath = %q, want %q", got, want)
	}
}

func TestHandleMessage_NoMM(t *testing.T) {
	d := &Daemon{
		mm:         nil,
		channelID:  "",
		containers: map[string]*Container{},
	}

	body := `{"dal":"dev","message":"hello"}`
	req := httptest.NewRequest("POST", "/api/message", nil)
	req.Body = nopCloser(body)
	w := httptest.NewRecorder()

	d.handleMessage(w, req)

	// MM 없으면 503 또는 200(empty) — 둘 다 허용
	if w.Code != 503 && w.Code != 200 {
		t.Fatalf("status = %d, want 503 or 200", w.Code)
	}
}

func TestHandlePs_OutputFormat(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"dev":    {DalName: "dev", Player: "claude", Role: "member", Status: "running", ContainerID: "abc123def456", Skills: 3},
			"leader": {DalName: "leader", Player: "claude", Role: "leader", Status: "running", ContainerID: "def456abc789", Skills: 5},
		},
	}

	req := httptest.NewRequest("GET", "/api/ps", nil)
	w := httptest.NewRecorder()

	d.handlePs(w, req)

	var containers []Container
	json.NewDecoder(w.Body).Decode(&containers)
	if len(containers) != 2 {
		t.Fatalf("got %d containers, want 2", len(containers))
	}
}

// nopCloser wraps a string as an io.ReadCloser for request body
type nopReader struct {
	data []byte
	pos  int
}

func (r *nopReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, nil
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
func (r *nopReader) Close() error   { return nil }
func nopCloser(s string) *nopReader { return &nopReader{data: []byte(s)} }
