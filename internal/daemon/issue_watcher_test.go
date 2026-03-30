package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssueStoreSeen(t *testing.T) {
	s := &issueStore{issues: make(map[int]*trackedIssue)}

	if s.Seen(1) {
		t.Error("expected issue 1 not seen")
	}

	s.Track(&trackedIssue{Number: 1, Title: "test", Status: "dispatched", DetectedAt: time.Now()})

	if !s.Seen(1) {
		t.Error("expected issue 1 seen after Track")
	}
	if s.Seen(2) {
		t.Error("expected issue 2 not seen")
	}
}

func TestIssueStoreList(t *testing.T) {
	s := &issueStore{issues: make(map[int]*trackedIssue)}
	s.Track(&trackedIssue{Number: 1, Title: "first", Status: "dispatched", DetectedAt: time.Now()})
	s.Track(&trackedIssue{Number: 2, Title: "second", Status: "error", DetectedAt: time.Now()})

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(list))
	}
}

func TestIssueStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issues_seen.json")

	// Create store and add issues
	s := newIssueStore(path)
	s.Track(&trackedIssue{Number: 10, Title: "persisted", Status: "dispatched", DetectedAt: time.Now()})

	// Verify file was written
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}

	// Load from file
	s2 := newIssueStore(path)
	if !s2.Seen(10) {
		t.Error("expected issue 10 to be persisted and loaded")
	}
	if s2.Seen(99) {
		t.Error("expected issue 99 not seen")
	}
}

func TestIsPullRequest(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://github.com/owner/repo/issues/1", false},
		{"https://github.com/owner/repo/pull/2", true},
		{"https://github.com/owner/repo/issues/3", false},
	}
	for _, tt := range tests {
		issue := ghIssue{URL: tt.url}
		if got := isPullRequest(issue); got != tt.want {
			t.Errorf("isPullRequest(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
