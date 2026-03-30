package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAuditLog_WritesEntry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_DATA_DIR", tmp)

	// Create repo-name dir
	repoDir := filepath.Join(tmp, "state", "test-repo")
	os.MkdirAll(repoDir, 0755)

	// Override wd
	origWd, _ := os.Getwd()
	os.Chdir(filepath.Join(tmp, "state", "test-repo"))
	defer os.Chdir(origWd)
	// Need to create the directory first
	os.MkdirAll(filepath.Join(tmp, "state", "test-repo"), 0755)
	os.Chdir(filepath.Join(tmp, "state", "test-repo"))

	appendAuditLog("attach", "dev", "긴급 디버깅")

	data, err := os.ReadFile(filepath.Join(tmp, "state", "test-repo", "audit.log"))
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

func TestAppendAuditLog_MultipleEntries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_DATA_DIR", tmp)
	repoDir := filepath.Join(tmp, "state", "test-repo")
	os.MkdirAll(repoDir, 0755)
	origWd, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(origWd)

	appendAuditLog("attach", "dev", "디버깅")
	appendAuditLog("logs", "tester", "로그 확인")
	appendAuditLog("task", "verifier", "수동 검증")

	data, _ := os.ReadFile(filepath.Join(repoDir, "audit.log"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 entries, got %d", len(lines))
	}
}

func TestAppendAuditLog_TSVFormat(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_DATA_DIR", tmp)
	repoDir := filepath.Join(tmp, "state", "test-repo")
	os.MkdirAll(repoDir, 0755)
	origWd, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(origWd)

	appendAuditLog("attach", "dev", "reason text")

	data, _ := os.ReadFile(filepath.Join(repoDir, "audit.log"))
	fields := strings.Split(strings.TrimSpace(string(data)), "\t")
	if len(fields) != 4 {
		t.Errorf("expected 4 TSV fields, got %d: %v", len(fields), fields)
	}
}

func TestAppendAuditLog_NoStateDir(t *testing.T) {
	t.Setenv("DALCENTER_DATA_DIR", "/nonexistent/path/that/does/not/exist")
	origWd, _ := os.Getwd()
	os.Chdir("/tmp")
	defer os.Chdir(origWd)
	// Should not panic
	appendAuditLog("test", "target", "reason")
}

func TestAppendAuditLog_TimestampFormat(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DALCENTER_DATA_DIR", tmp)
	repoDir := filepath.Join(tmp, "state", "test-repo")
	os.MkdirAll(repoDir, 0755)
	origWd, _ := os.Getwd()
	os.Chdir(repoDir)
	defer os.Chdir(origWd)

	appendAuditLog("wake", "leader", "시작")

	data, _ := os.ReadFile(filepath.Join(repoDir, "audit.log"))
	fields := strings.Split(strings.TrimSpace(string(data)), "\t")
	if len(fields) < 1 {
		t.Fatal("no fields")
	}
	// RFC3339 timestamp should contain "T" and timezone offset
	ts := fields[0]
	if !strings.Contains(ts, "T") {
		t.Errorf("timestamp should be RFC3339 format, got: %s", ts)
	}
}
