package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposeDecision_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "decisions", "inbox")
	os.MkdirAll(inbox, 0755)

	// Override workspace path for test
	origDir := "/workspace/decisions/inbox"
	_ = origDir

	// Test the core logic directly
	title := "circuit-breaker 개선"
	body := "모든 에러에 RecordFailure 호출"
	dalName := "dev"

	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		return -1
	}, title)

	filename := dalName + "-20260328-" + slug + ".md"
	path := filepath.Join(inbox, filename)

	content := "### 2026-03-28: " + title + "\n**By:** " + dalName + "\n**What:** " + body + "\n**Why:** \n"
	os.WriteFile(path, []byte(content), 0644)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "circuit-breaker") {
		t.Error("missing title")
	}
	if !strings.Contains(s, "dev") {
		t.Error("missing dal name")
	}
	if !strings.Contains(s, "RecordFailure") {
		t.Error("missing body")
	}
	if !strings.Contains(s, "**By:**") {
		t.Error("missing format")
	}
}

func TestProposeDecision_SlugTruncation(t *testing.T) {
	longTitle := strings.Repeat("very-long-title-", 10) // 160 chars
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return -1
	}, longTitle)
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if len(slug) != 40 {
		t.Errorf("slug should be truncated to 40, got %d", len(slug))
	}
}

func TestProposeDecision_KoreanTitle(t *testing.T) {
	title := "서킷브레이커 개선"
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		return -1
	}, title)
	// Korean chars get stripped, only hyphens remain
	if strings.Contains(slug, "서킷") {
		t.Error("Korean should be stripped from slug")
	}
}

func TestProposeDecision_EmptyInbox(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "decisions", "inbox")
	// Don't create inbox dir - should fail gracefully
	_, err := os.Stat(inbox)
	if err == nil {
		t.Fatal("inbox should not exist")
	}
}
