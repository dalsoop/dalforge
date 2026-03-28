package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposeDecision_DirectCall(t *testing.T) {
	// proposeDecision writes to /workspace/decisions/inbox
	// We can't easily redirect, but we can test it if /workspace exists
	// Skip if /workspace doesn't exist (not in container)
	inboxDir := "/workspace/decisions/inbox"
	if _, err := os.Stat("/workspace"); err != nil {
		t.Skip("not in container environment")
	}
	os.MkdirAll(inboxDir, 0755)
	defer os.RemoveAll(inboxDir)

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
