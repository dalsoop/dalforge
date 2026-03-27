package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateDir_CreatesDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	dir := stateDir("/path/to/myrepo")
	if filepath.Base(dir) != "myrepo" {
		t.Errorf("got %s, want myrepo suffix", dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("directory not created: %v", err)
	}
}

func TestStateDir_DefaultName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	dir := stateDir("")
	if filepath.Base(dir) != "default" {
		t.Errorf("got %s, want default", filepath.Base(dir))
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
