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

func TestStateDir_HashUniqueness(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	// Same basename, different paths → different dirs
	dir1 := stateDir("/path/to/app")
	dir2 := stateDir("/other/path/to/app")
	if dir1 == dir2 {
		t.Errorf("same-name repos should have different state dirs: %s vs %s", dir1, dir2)
	}
}

func TestStateDir_Deterministic(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	dir1 := stateDir("/path/to/repo")
	dir2 := stateDir("/path/to/repo")
	if dir1 != dir2 {
		t.Errorf("same path should produce same dir: %s vs %s", dir1, dir2)
	}
}

func TestStateDir_EnvOverride(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", custom)

	dir := stateDir("/some/repo")
	if !strings.HasPrefix(dir, custom) {
		t.Errorf("should use custom base: got %s", dir)
	}
}

func TestNowPath_ReturnsFilePath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	path := nowPath("/some/repo")
	if !strings.HasSuffix(path, "now.md") {
		t.Errorf("should end with now.md: %s", path)
	}
}

func TestInboxDir_NestedUnderState(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	inbox := inboxDir("/my/repo")
	state := stateDir("/my/repo")
	if !strings.HasPrefix(inbox, state) {
		t.Errorf("inbox should be under state dir: inbox=%s state=%s", inbox, state)
	}
}

func TestHistoryBufferDir_NestedUnderState(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	buf := historyBufferDir("/my/repo")
	state := stateDir("/my/repo")
	if !strings.HasPrefix(buf, state) {
		t.Errorf("history-buffer should be under state dir")
	}
	if filepath.Base(buf) != "history-buffer" {
		t.Errorf("should end with history-buffer: %s", buf)
	}
}

func TestWisdomInboxDir_NestedUnderState(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	wis := wisdomInboxDir("/my/repo")
	state := stateDir("/my/repo")
	if !strings.HasPrefix(wis, state) {
		t.Errorf("wisdom inbox should be under state dir")
	}
}
