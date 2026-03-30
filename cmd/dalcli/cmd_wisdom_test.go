package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposeWisdom_DirectCall_Pattern(t *testing.T) {
	inboxDir := t.TempDir()
	t.Setenv("DALCLI_WISDOM_INBOX", inboxDir)

	err := proposeWisdom("test-dal", "always test first", "prevents regressions", false)
	if err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob(filepath.Join(inboxDir, "test-dal-*.md"))
	if len(files) == 0 {
		t.Fatal("no wisdom file created")
	}

	data, _ := os.ReadFile(files[0])
	content := string(data)
	if !strings.Contains(content, "**Pattern:**") {
		t.Error("missing Pattern format")
	}
}

func TestProposeWisdom_DirectCall_AntiPattern(t *testing.T) {
	inboxDir := t.TempDir()
	t.Setenv("DALCLI_WISDOM_INBOX", inboxDir)

	err := proposeWisdom("test-dal", "force push", "destroys history", true)
	if err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob(filepath.Join(inboxDir, "test-dal-*.md"))
	if len(files) == 0 {
		t.Fatal("no wisdom file created")
	}

	data, _ := os.ReadFile(files[0])
	if !strings.Contains(string(data), "**Avoid:**") {
		t.Error("missing Avoid format for anti-pattern")
	}
}
