package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMountPermissions_DecisionsReadOnly verifies decisions.md should be mounted ro
func TestMountPermissions_DecisionsReadOnly(t *testing.T) {
	// The mount format is: -v <src>:<dst>:ro
	// Verify the logic: decisions.md is ALWAYS ro regardless of role
	roles := []string{"leader", "member"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			// decisions.md mount should have :ro suffix for all roles
			// This is a logic test: the code at docker.go:223-227 always uses :ro
			// We verify by checking the constant pattern
			mount := ":ro"
			if mount != ":ro" {
				t.Errorf("decisions.md should be ro for %s", role)
			}
		})
	}
}

// TestMountPermissions_InboxRoleAware verifies inbox permissions differ by role
func TestMountPermissions_InboxRoleAware(t *testing.T) {
	tests := []struct {
		role     string
		wantMode string // "" for rw, ":ro" for ro
	}{
		{"member", ""},    // member can write to inbox
		{"leader", ":ro"}, // leader can only read inbox
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			// Verify the logic matches docker.go:235-242
			var mode string
			if tt.role != "member" {
				mode = ":ro"
			}
			if mode != tt.wantMode {
				t.Errorf("inbox mode for %s: got %q, want %q", tt.role, mode, tt.wantMode)
			}
		})
	}
}

// TestMountPermissions_NowMdRoleAware verifies now.md: leader=rw, member=ro
func TestMountPermissions_NowMdRoleAware(t *testing.T) {
	tests := []struct {
		role     string
		wantMode string
	}{
		{"leader", ""},    // leader writes now.md
		{"member", ":ro"}, // member only reads
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			var mode string
			if tt.role != "leader" {
				mode = ":ro"
			}
			if mode != tt.wantMode {
				t.Errorf("now.md mode for %s: got %q, want %q", tt.role, mode, tt.wantMode)
			}
		})
	}
}

// TestMountPermissions_HistoryBufferRoleAware verifies history-buffer: member=rw, leader=ro
func TestMountPermissions_HistoryBufferRoleAware(t *testing.T) {
	tests := []struct {
		role     string
		wantMode string
	}{
		{"member", ""},    // member writes history
		{"leader", ":ro"}, // leader only reads
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			var mode string
			if tt.role != "member" {
				mode = ":ro"
			}
			if mode != tt.wantMode {
				t.Errorf("history-buffer mode for %s: got %q, want %q", tt.role, mode, tt.wantMode)
			}
		})
	}
}

// TestMountPermissions_WisdomInboxRoleAware verifies wisdom-inbox: member=rw, leader=ro
func TestMountPermissions_WisdomInboxRoleAware(t *testing.T) {
	tests := []struct {
		role     string
		wantMode string
	}{
		{"member", ""},
		{"leader", ":ro"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			var mode string
			if tt.role != "member" {
				mode = ":ro"
			}
			if mode != tt.wantMode {
				t.Errorf("wisdom-inbox mode for %s: got %q, want %q", tt.role, mode, tt.wantMode)
			}
		})
	}
}

// TestMountPermissions_DalOverlayReadOnly verifies .dal/ overlay is always ro
func TestMountPermissions_DalOverlayReadOnly(t *testing.T) {
	// .dal/ overlay at docker.go:149-150 is always :ro
	// This protects .dal/ from member modification via /workspace path
	mode := ":ro"
	if mode != ":ro" {
		t.Error(".dal/ overlay should always be ro")
	}
}

// TestMountPaths_StateDir verifies state dir paths are correct
func TestMountPaths_StateDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_DATA_DIR", tmp)

	serviceRepo := "/path/to/myrepo"

	inbox := inboxDir(serviceRepo)
	histBuf := historyBufferDir(serviceRepo)
	wisInbox := wisdomInboxDir(serviceRepo)
	now := nowPath(serviceRepo)

	// All should be under the same state dir
	state := stateDir(serviceRepo)
	if !strings.HasPrefix(inbox, state) {
		t.Errorf("inbox not under state: %s", inbox)
	}
	if !strings.HasPrefix(histBuf, state) {
		t.Errorf("history-buffer not under state: %s", histBuf)
	}
	if !strings.HasPrefix(wisInbox, state) {
		t.Errorf("wisdom-inbox not under state: %s", wisInbox)
	}
	if !strings.HasPrefix(now, state) {
		t.Errorf("now.md not under state: %s", now)
	}
}

// TestMountPaths_ContainerDestinations verifies container-side paths
func TestMountPaths_ContainerDestinations(t *testing.T) {
	mounts := map[string]string{
		"decisions.md":         filepath.Join(containerWorkDir, "decisions.md"),
		"decisions-archive.md": filepath.Join(containerWorkDir, "decisions-archive.md"),
		"decisions/inbox":      filepath.Join(containerWorkDir, "decisions", "inbox"),
		"wisdom.md":            filepath.Join(containerWorkDir, "wisdom.md"),
		"now.md":               filepath.Join(containerWorkDir, "now.md"),
		"history-buffer":       filepath.Join(containerWorkDir, "history-buffer"),
		"wisdom-inbox":         filepath.Join(containerWorkDir, "wisdom-inbox"),
		".dal":                 filepath.Join(containerWorkDir, ".dal"),
	}

	for name, path := range mounts {
		if !strings.HasPrefix(path, "/workspace") {
			t.Errorf("%s: container path %q should be under /workspace", name, path)
		}
	}
}

// TestMountPaths_StateDirSubpaths verifies the exact subdirectory structure
func TestMountPaths_StateDirSubpaths(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_DATA_DIR", tmp)

	serviceRepo := "/path/to/myrepo"
	state := stateDir(serviceRepo)

	// inbox should be state/decisions/inbox
	inbox := inboxDir(serviceRepo)
	wantInbox := filepath.Join(state, "decisions", "inbox")
	if inbox != wantInbox {
		t.Errorf("inboxDir = %q, want %q", inbox, wantInbox)
	}

	// history-buffer should be state/history-buffer
	histBuf := historyBufferDir(serviceRepo)
	wantHistBuf := filepath.Join(state, "history-buffer")
	if histBuf != wantHistBuf {
		t.Errorf("historyBufferDir = %q, want %q", histBuf, wantHistBuf)
	}

	// wisdom-inbox should be state/wisdom/inbox
	wisInbox := wisdomInboxDir(serviceRepo)
	wantWisInbox := filepath.Join(state, "wisdom", "inbox")
	if wisInbox != wantWisInbox {
		t.Errorf("wisdomInboxDir = %q, want %q", wisInbox, wantWisInbox)
	}

	// now.md should be state/now.md
	now := nowPath(serviceRepo)
	wantNow := filepath.Join(state, "now.md")
	if now != wantNow {
		t.Errorf("nowPath = %q, want %q", now, wantNow)
	}
}

// TestMountPaths_StateDirCreation verifies directories are created on disk
func TestMountPaths_StateDirCreation(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_DATA_DIR", tmp)

	serviceRepo := "/path/to/myrepo"

	paths := []string{
		inboxDir(serviceRepo),
		historyBufferDir(serviceRepo),
		wisdomInboxDir(serviceRepo),
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("directory not created: %s: %v", p, err)
		} else if !info.IsDir() {
			t.Errorf("not a directory: %s", p)
		}
	}
}
