package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocaldalRoot_DefaultIsCwd(t *testing.T) {
	os.Unsetenv("DALCENTER_LOCALDAL_PATH")
	root := localdalRoot()
	wd, _ := os.Getwd()
	expected := filepath.Join(wd, ".dal")
	if root != expected {
		t.Errorf("got %q, want %q", root, expected)
	}
}

func TestLocaldalRoot_EnvOverride(t *testing.T) {
	t.Setenv("DALCENTER_LOCALDAL_PATH", "/custom/path")
	root := localdalRoot()
	if root != "/custom/path" {
		t.Errorf("got %q, want /custom/path", root)
	}
}

func TestServeCmd_RepoOverridesLocaldalRoot(t *testing.T) {
	// Test the logic: if serviceRepo is set and .dal exists there, use it
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "myrepo")
	dalDir := filepath.Join(repoDir, ".dal")
	os.MkdirAll(dalDir, 0755)

	// Simulate the serve command logic
	serviceRepo := repoDir
	root := localdalRoot() // default = cwd/.dal

	if serviceRepo != "" {
		repoRoot := filepath.Join(serviceRepo, ".dal")
		if _, err := os.Stat(repoRoot); err == nil {
			root = repoRoot
		}
	}

	if root != dalDir {
		t.Errorf("root = %q, want %q", root, dalDir)
	}
}

func TestServeCmd_RepoWithoutDal_FallsBack(t *testing.T) {
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "myrepo")
	os.MkdirAll(repoDir, 0755)
	// .dal/ does NOT exist in repo

	serviceRepo := repoDir
	root := localdalRoot()
	originalRoot := root

	if serviceRepo != "" {
		repoRoot := filepath.Join(serviceRepo, ".dal")
		if _, err := os.Stat(repoRoot); err == nil {
			root = repoRoot
		}
	}

	// Should fall back to original
	if root != originalRoot {
		t.Errorf("should fall back to cwd-based root when .dal/ doesn't exist in repo")
	}
}
