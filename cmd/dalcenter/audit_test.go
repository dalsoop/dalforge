package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAuditLog_WritesEntry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_STATE_DIR", tmp)

	// Create repo-name dir
	repoDir := filepath.Join(tmp, "test-repo")
	os.MkdirAll(repoDir, 0755)

	// Override wd
	origWd, _ := os.Getwd()
	os.Chdir(filepath.Join(tmp, "test-repo"))
	defer os.Chdir(origWd)
	// Need to create the directory first
	os.MkdirAll(filepath.Join(tmp, "test-repo"), 0755)
	os.Chdir(filepath.Join(tmp, "test-repo"))

	appendAuditLog("attach", "dev", "긴급 디버깅")

	data, err := os.ReadFile(filepath.Join(tmp, "test-repo", "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "attach") {
		t.Error("missing action")
	}
	if !strings.Contains(content, "dev") {
		t.Error("missing target")
	}
	if !strings.Contains(content, "긴급 디버깅") {
		t.Error("missing reason")
	}
}
