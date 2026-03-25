package main

import (
	"strings"
	"testing"
)

func TestExtractTask(t *testing.T) {
	tests := []struct {
		name    string
		content string
		prefix  string
		want    string
	}{
		{"standard assign", "@dal-checker 작업 지시: do the thing", "작업 지시:", "do the thing"},
		{"with whitespace", "@dal-checker 작업 지시:   lots of spaces   ", "작업 지시:", "lots of spaces"},
		{"no prefix", "@dal-checker hello", "작업 지시:", ""},
		{"empty content", "", "작업 지시:", ""},
		{"prefix only", "작업 지시:", "작업 지시:", ""},
		{"multiline", "@dal-writer 작업 지시: first\nsecond", "작업 지시:", "first\nsecond"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTask(tt.content, tt.prefix)
			if got != tt.want {
				t.Errorf("extractTask(%q, %q) = %q, want %q", tt.content, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 3, "hel..."},
		{"", 5, ""},
		{"ab", 1, "a..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

func TestFormatReport(t *testing.T) {
	report := formatReport("task done")
	if !strings.Contains(report, "✅ 작업 완료") {
		t.Error("should contain success marker")
	}
	if !strings.Contains(report, "task done") {
		t.Error("should contain output")
	}

	long := strings.Repeat("a", 4000)
	report = formatReport(long)
	if len(report) > 3500 {
		t.Errorf("long report should be truncated, got len=%d", len(report))
	}
	if !strings.Contains(report, "... (truncated) ...") {
		t.Error("should contain truncation marker")
	}
}

func TestFormatReport_Empty(t *testing.T) {
	report := formatReport("")
	if !strings.Contains(report, "✅ 작업 완료") {
		t.Error("empty output should still show success")
	}
}

// --- Free-form mention parsing ---

func TestFreeFormMention(t *testing.T) {
	mention := "@dal-leader"
	tests := []struct {
		content string
		want    string
	}{
		{"@dal-leader 가야가 좀 밋밋한데", "가야가 좀 밋밋한데"},
		{"@dal-leader 작업 지시: 뭐 해", "뭐 해"},   // 작업 지시가 우선
		{"@dal-leader", ""},                         // 멘션만
		{"@dal-leader    ", ""},                     // 멘션 + 공백
		{"hey @dal-leader 이거 봐봐", "hey  이거 봐봐"}, // 중간 멘션
	}
	for _, tt := range tests {
		// Simulate free-form extraction
		task := extractTask(tt.content, "작업 지시:")
		if task == "" {
			task = strings.TrimSpace(strings.ReplaceAll(tt.content, mention, ""))
		}
		if task != tt.want {
			t.Errorf("free-form(%q) = %q, want %q", tt.content, task, tt.want)
		}
	}
}

// --- autoGitWorkflow (unit: no git available, should handle gracefully) ---

func TestAutoGitWorkflow_NoChanges(t *testing.T) {
	// In test env, /workspace doesn't exist → git status fails → returns ""
	result := autoGitWorkflow("test-dal")
	if result != "" {
		t.Errorf("expected empty for no workspace, got %q", result)
	}
}
