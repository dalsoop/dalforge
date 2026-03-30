package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendHistoryBuffer_DirectCall(t *testing.T) {
	bufDir := t.TempDir()
	t.Setenv("DALCLI_HISTORY_BUFFER", bufDir)

	appendHistoryBuffer("test-dal", "implement feature", "done", "완료")

	data, err := os.ReadFile(filepath.Join(bufDir, "test-dal.md"))
	if err != nil {
		t.Fatal("history buffer not written")
	}
	content := string(data)
	if !strings.Contains(content, "implement feature") {
		t.Error("missing task in entry")
	}
	if !strings.Contains(content, "완료") {
		t.Error("missing status")
	}
}
