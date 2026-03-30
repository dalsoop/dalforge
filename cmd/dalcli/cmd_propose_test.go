package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposeDecision_DirectCall(t *testing.T) {
	inboxDir := t.TempDir()
	t.Setenv("DALCLI_DECISIONS_INBOX", inboxDir)

	err := proposeDecision("test-dal", "test title", "test body")
	if err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob(filepath.Join(inboxDir, "test-dal-*.md"))
	if len(files) == 0 {
		t.Fatal("no proposal file created")
	}

	data, _ := os.ReadFile(files[0])
	content := string(data)
	if !strings.Contains(content, "test-dal") {
		t.Error("missing dal name in content")
	}
	if !strings.Contains(content, "test body") {
		t.Error("missing body in content")
	}
}
