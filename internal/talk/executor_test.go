package talk

import (
	"testing"
)

func TestExtractResultSuccess(t *testing.T) {
	input := `{"type":"system","subtype":"init","session_id":"abc"}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}
{"type":"user","message":{"content":[{"tool_use_id":"x","type":"tool_result","content":"file data"}]}}
{"type":"result","subtype":"success","result":"README의 첫 3줄입니다.","duration_ms":5000}`

	got := extractResult(input)
	want := "README의 첫 3줄입니다."
	if got != want {
		t.Fatalf("extractResult() = %q, want %q", got, want)
	}
}

func TestExtractResultEmpty(t *testing.T) {
	input := `{"type":"system","subtype":"init"}
{"type":"result","subtype":"success","result":"","duration_ms":100}`

	got := extractResult(input)
	// Empty result → returns raw output
	if got == "" {
		t.Fatal("extractResult() should not return empty for non-empty input")
	}
}

func TestExtractResultNoResultLine(t *testing.T) {
	input := `just some text output without json`
	got := extractResult(input)
	if got != "just some text output without json" {
		t.Fatalf("extractResult() fallback = %q", got)
	}
}

func TestExtractResultMultiline(t *testing.T) {
	input := `{"type":"result","subtype":"success","result":"line1\nline2\nline3"}`
	got := extractResult(input)
	if got != "line1\nline2\nline3" {
		t.Fatalf("extractResult() multiline = %q", got)
	}
}

func TestSanitizerClean(t *testing.T) {
	s := NewSanitizer()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"veilkey token", "token is VK:my-secret-key here", "token is [VK:***] here"},
		{"bearer token", "Bearer eyJhbGciOiJIUzI1NiJ9.test", "Bearer [REDACTED]"},
		{"aws secret", "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI", "AWS_SECRET_ACCESS_KEY=[REDACTED]"},
		{"no secrets", "just normal text", "just normal text"},
		{"empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Clean(tc.input)
			if got != tc.want {
				t.Fatalf("Clean(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}



func TestSanitizer_PreservesNormal(t *testing.T) {
	s := NewSanitizer()
	result := s.Clean("hello world 123")
	if result != "hello world 123" {
		t.Errorf("got %q", result)
	}
}

func TestExtractResult_Success(t *testing.T) {
	result := extractResult("some output\nmore output\nfinal line")
	if result == "" {
		t.Fatal("should extract result")
	}
}

func TestExtractResult_WithError(t *testing.T) {
	result := extractResult("partial output")
	if result == "" {
		t.Fatal("should still return something on error")
	}
}
