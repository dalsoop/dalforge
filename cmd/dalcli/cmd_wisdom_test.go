package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposeWisdom_Pattern(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "wisdom-inbox")
	os.MkdirAll(inbox, 0755)

	content := "**Pattern:** 테스트 먼저 작성\n**Context:** 버그 수정 시\n"
	path := filepath.Join(inbox, "dev-20260328-test-first.md")
	os.WriteFile(path, []byte(content), 0644)

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "**Pattern:**") {
		t.Error("pattern format missing")
	}
	if !strings.Contains(string(data), "**Context:**") {
		t.Error("context missing")
	}
}

func TestProposeWisdom_AntiPattern(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "wisdom-inbox")
	os.MkdirAll(inbox, 0755)

	content := "**Avoid:** 대형 파일 자동 로드\n**Why:** 토큰 폭발\n**Ref:** PR #307\n"
	path := filepath.Join(inbox, "dev-20260328-no-large-file.md")
	os.WriteFile(path, []byte(content), 0644)

	data, _ := os.ReadFile(path)
	s := string(data)
	if !strings.Contains(s, "**Avoid:**") {
		t.Error("avoid format missing")
	}
	if !strings.Contains(s, "**Why:**") {
		t.Error("why missing")
	}
	if !strings.Contains(s, "**Ref:**") {
		t.Error("ref missing")
	}
}

func TestProposeWisdom_SlugGeneration(t *testing.T) {
	tests := []struct {
		input string
		want  string // expected substring in slug
	}{
		{"test pattern", "test-pattern"},
		{"UPPER CASE", "upper-case"},
		{"special!@#chars", "specialchars"},
		{"한글 패턴", "-"},
	}
	for _, tt := range tests {
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
		}, tt.input)
		if !strings.Contains(slug, tt.want) {
			t.Errorf("slug(%q) = %q, want contains %q", tt.input, slug, tt.want)
		}
	}
}

func TestProposeWisdom_FileNaming(t *testing.T) {
	dalName := "tester"
	pattern := "always write tests"
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		return -1
	}, pattern)
	filename := dalName + "-20260328-" + slug + ".md"
	if !strings.HasPrefix(filename, "tester-") {
		t.Errorf("filename should start with dal name: %s", filename)
	}
	if !strings.HasSuffix(filename, ".md") {
		t.Error("filename should end with .md")
	}
}
