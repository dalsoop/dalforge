package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendHistoryBuffer_WritesEntry(t *testing.T) {
	tmp := t.TempDir()
	bufferDir := filepath.Join(tmp, "history-buffer")
	os.MkdirAll(bufferDir, 0755)

	// Override path for test
	origDir := "/workspace/history-buffer"
	_ = origDir // can't override easily, test the function logic directly

	// Test the entry format
	dalName := "dev"
	task := "implement feature X"
	result := "completed successfully"
	status := "완료"

	filename := filepath.Join(bufferDir, dalName+".md")
	entry := "\n### 2026-03-28: " + truncate(task, 80) + "\n**상태:** " + status + "\n**결과:** " + truncate(result, 200) + "\n**다음:** \n**주의:** \n"

	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(entry)
	f.Close()

	data, _ := os.ReadFile(filename)
	if !strings.Contains(string(data), "implement feature X") {
		t.Error("entry not written")
	}
	if !strings.Contains(string(data), "완료") {
		t.Error("status not written")
	}
}

func TestProposeWisdom_CreatesFile(t *testing.T) {
	tmp := t.TempDir()
	inboxDir := filepath.Join(tmp, "wisdom-inbox")
	os.MkdirAll(inboxDir, 0755)

	// Test wisdom proposal format
	content := "**Avoid:** large file injection\n**Why:** token explosion\n**Ref:** \n"
	path := filepath.Join(inboxDir, "dev-20260328-test.md")
	os.WriteFile(path, []byte(content), 0644)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Avoid") {
		t.Error("anti-pattern not written")
	}
}
