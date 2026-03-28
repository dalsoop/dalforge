package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateDir_CreatesDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	dir := stateDir("/path/to/myrepo")
	base := filepath.Base(dir)
	if !strings.HasPrefix(base, "myrepo-") {
		t.Errorf("got %s, want myrepo-{hash} prefix", base)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("directory not created: %v", err)
	}
}

func TestStateDir_DefaultName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	dir := stateDir("")
	base := filepath.Base(dir)
	if !strings.HasPrefix(base, "default-") {
		t.Errorf("got %s, want default-{hash} prefix", base)
	}
}

func TestHistoryBufferDir_Creates(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)
	dir := historyBufferDir("/path/to/repo")
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("history-buffer not created: %v", err)
	}
}

func TestWisdomInboxDir_Creates(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)
	dir := wisdomInboxDir("/path/to/repo")
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("wisdom/inbox not created: %v", err)
	}
}

func TestInboxDir_CreatesNestedDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	dir := inboxDir("/path/to/myrepo")
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("inbox directory not created: %v", err)
	}
	// Should end with decisions/inbox
	if filepath.Base(filepath.Dir(dir)) != "decisions" {
		t.Errorf("unexpected path: %s", dir)
	}
}
