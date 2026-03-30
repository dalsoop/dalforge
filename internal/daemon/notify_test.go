package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildNotifyPayload_Done(t *testing.T) {
	tr := &taskResult{
		ID:         "task-001",
		Dal:        "leader",
		Task:       "go test ./...",
		Status:     "done",
		Output:     "all tests passed\nhttps://github.com/dalsoop/dalcenter/pull/42\n",
		GitChanges: 3,
		Verified:   "yes",
	}
	p := buildNotifyPayload("leader", tr)
	if p.Event != "task_done" {
		t.Errorf("expected event=task_done, got %s", p.Event)
	}
	if p.PRUrl != "https://github.com/dalsoop/dalcenter/pull/42" {
		t.Errorf("expected PR URL extracted, got %q", p.PRUrl)
	}
	if p.Error != "" {
		t.Errorf("expected no error for done task, got %q", p.Error)
	}
	if p.Changes != 3 {
		t.Errorf("expected 3 changes, got %d", p.Changes)
	}
}

func TestBuildNotifyPayload_Failed(t *testing.T) {
	tr := &taskResult{
		ID:     "task-002",
		Dal:    "dev",
		Task:   "implement feature X",
		Status: "failed",
		Error:  "compilation error: undefined variable",
	}
	p := buildNotifyPayload("dev", tr)
	if p.Event != "task_failed" {
		t.Errorf("expected event=task_failed, got %s", p.Event)
	}
	if p.Error == "" {
		t.Error("expected error content in payload")
	}
}

func TestBuildNotifyPayload_Blocked(t *testing.T) {
	tr := &taskResult{
		ID:     "task-003",
		Status: "blocked",
		Error:  "need approval",
	}
	p := buildNotifyPayload("dev", tr)
	if p.Event != "task_failed" {
		t.Errorf("blocked should map to task_failed event, got %s", p.Event)
	}
}

func TestExtractPRUrl(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "github PR URL in output",
			output: "Created PR: https://github.com/dalsoop/dalcenter/pull/42",
			want:   "https://github.com/dalsoop/dalcenter/pull/42",
		},
		{
			name:   "no PR URL",
			output: "all tests passed\nno changes",
			want:   "",
		},
		{
			name:   "PR URL with trailing punctuation",
			output: "see https://github.com/dalsoop/dalcenter/pull/99.",
			want:   "https://github.com/dalsoop/dalcenter/pull/99",
		},
		{
			name:   "multiple lines with PR",
			output: "line1\nline2\nhttps://github.com/org/repo/pull/123\nline4",
			want:   "https://github.com/org/repo/pull/123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPRUrl(tt.output)
			if got != tt.want {
				t.Errorf("extractPRUrl() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSendNotifyHTTP(t *testing.T) {
	var received NotifyPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := NotifyPayload{
		Event:  "task_done",
		Dal:    "leader",
		TaskID: "task-001",
		Status: "done",
		PRUrl:  "https://github.com/dalsoop/dalcenter/pull/42",
	}
	sendNotifyHTTP(srv.URL, payload)

	if received.Event != "task_done" {
		t.Errorf("expected task_done, got %s", received.Event)
	}
	if received.PRUrl != "https://github.com/dalsoop/dalcenter/pull/42" {
		t.Errorf("expected PR URL, got %s", received.PRUrl)
	}
}

func TestNotifyPayload_JSONSerialization(t *testing.T) {
	p := NotifyPayload{
		Event:  "task_done",
		Dal:    "leader",
		TaskID: "task-001",
		Task:   "run tests",
		Status: "done",
		PRUrl:  "https://github.com/org/repo/pull/1",
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded NotifyPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.PRUrl != p.PRUrl {
		t.Errorf("PR URL lost in round-trip: got %q", decoded.PRUrl)
	}
	if decoded.Event != "task_done" {
		t.Errorf("event lost in round-trip: got %q", decoded.Event)
	}
}
