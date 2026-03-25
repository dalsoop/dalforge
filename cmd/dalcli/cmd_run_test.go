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
		{
			name:    "standard assign",
			content: "@dal-checker 작업 지시: do the thing",
			prefix:  "작업 지시:",
			want:    "do the thing",
		},
		{
			name:    "with whitespace",
			content: "@dal-checker 작업 지시:   lots of spaces   ",
			prefix:  "작업 지시:",
			want:    "lots of spaces",
		},
		{
			name:    "no prefix",
			content: "@dal-checker hello",
			prefix:  "작업 지시:",
			want:    "",
		},
		{
			name:    "empty content",
			content: "",
			prefix:  "작업 지시:",
			want:    "",
		},
		{
			name:    "prefix only",
			content: "작업 지시:",
			prefix:  "작업 지시:",
			want:    "",
		},
		{
			name:    "multiline task",
			content: "@dal-writer 작업 지시: first line\nsecond line\nthird line",
			prefix:  "작업 지시:",
			want:    "first line\nsecond line\nthird line",
		},
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
	// Short output
	report := formatReport("task done")
	if !strings.Contains(report, "✅ 작업 완료") {
		t.Errorf("report should contain success marker")
	}
	if !strings.Contains(report, "task done") {
		t.Errorf("report should contain output")
	}

	// Long output (>3000) should be truncated
	long := strings.Repeat("a", 4000)
	report = formatReport(long)
	if len(report) > 3500 {
		t.Errorf("long report should be truncated, got len=%d", len(report))
	}
	if !strings.Contains(report, "... (truncated) ...") {
		t.Errorf("truncated report should contain truncation marker")
	}
}

func TestFormatReport_EmptyOutput(t *testing.T) {
	report := formatReport("")
	if !strings.Contains(report, "✅ 작업 완료") {
		t.Error("empty output should still show success")
	}
}
