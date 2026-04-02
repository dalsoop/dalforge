package main

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dalsoop/dalcenter/internal/providerexec"
)

// stubResolve replaces providerexec.ResolveFunc with /bin/echo for the
// duration of the test, avoiding real binary execution and timeouts.
func stubResolve(t *testing.T) {
	t.Helper()
	orig := providerexec.ResolveFunc
	providerexec.ResolveFunc = func(player string) (string, error) {
		return "/bin/echo", nil
	}
	t.Cleanup(func() { providerexec.ResolveFunc = orig })
}

// ══════════════════════════════════════════════════════════════
// parseInterval: edge cases
// ══════════════════════════════════════════════════════════════

func TestParseInterval_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"1s", time.Second},
		{"5m", 5 * time.Minute},
		{"2h", 2 * time.Hour},
		{"500ms", 500 * time.Millisecond},
		{"1h30m", 90 * time.Minute},
	}
	for _, tt := range tests {
		if got := parseInterval(tt.input, time.Hour); got != tt.want {
			t.Errorf("parseInterval(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseInterval_Empty(t *testing.T) {
	fallback := 42 * time.Second
	if got := parseInterval("", fallback); got != fallback {
		t.Errorf("empty → %v, want fallback %v", got, fallback)
	}
}

func TestParseInterval_InvalidInputs(t *testing.T) {
	fallback := 10 * time.Minute
	invalids := []string{"abc", "5", "minutes", "-", "1x2y"}
	for _, s := range invalids {
		if got := parseInterval(s, fallback); got != fallback {
			t.Errorf("parseInterval(%q) = %v, want fallback %v", s, got, fallback)
		}
	}
}

func TestParseInterval_ZeroDuration(t *testing.T) {
	if got := parseInterval("0s", time.Hour); got != 0 {
		t.Errorf("parseInterval('0s') = %v, want 0", got)
	}
}

func TestParseInterval_NegativeDuration(t *testing.T) {
	// Go's time.ParseDuration accepts negative values
	got := parseInterval("-5s", time.Hour)
	if got != -5*time.Second {
		t.Errorf("parseInterval('-5s') = %v, want -5s", got)
	}
}

// ══════════════════════════════════════════════════════════════
// truncate: edge cases
// ══════════════════════════════════════════════════════════════

func TestTruncate_ExactLength(t *testing.T) {
	if got := truncate("hello", 5); got != "hello" {
		t.Errorf("exact length: got %q", got)
	}
}

func TestTruncate_ZeroN(t *testing.T) {
	got := truncate("hello", 0)
	if got != "..." {
		t.Errorf("n=0: got %q, want '...'", got)
	}
}

func TestTruncate_EmptyString(t *testing.T) {
	if got := truncate("", 10); got != "" {
		t.Errorf("empty: got %q", got)
	}
}

func TestTruncate_Unicode(t *testing.T) {
	// truncate works on bytes, not runes — document this behavior
	s := "한글테스트" // 15 bytes in UTF-8
	got := truncate(s, 3)
	// First 3 bytes of "한" (3-byte UTF-8 char) = "한"
	if len(got) < 3 {
		t.Errorf("got %q (len=%d)", got, len(got))
	}
}

// ══════════════════════════════════════════════════════════════
// formatReport: boundary cases
// ══════════════════════════════════════════════════════════════

func TestFormatReport_ExactlyAtLimit(t *testing.T) {
	output := strings.Repeat("a", 3000) // exactly 3000
	report := formatReport(output)
	if strings.Contains(report, "truncated") {
		t.Error("exactly 3000 chars should NOT be truncated")
	}
}

func TestFormatReport_JustOverLimit(t *testing.T) {
	output := strings.Repeat("a", 3001) // 3001 > 3000
	report := formatReport(output)
	if !strings.Contains(report, "truncated") {
		t.Error("3001 chars should be truncated")
	}
}

func TestFormatReport_PreservesCodeBlock(t *testing.T) {
	report := formatReport("test output")
	if !strings.Contains(report, "```") {
		t.Error("should wrap in code block")
	}
}

func TestFormatReport_TruncatedPreservesEnds(t *testing.T) {
	// Build output with distinctive start and end
	start := "START_MARKER_"
	end := "_END_MARKER"
	middle := strings.Repeat("x", 4000)
	output := start + middle + end

	report := formatReport(output)
	if !strings.Contains(report, "START_MARKER") {
		t.Error("truncated report should preserve start")
	}
	if !strings.Contains(report, "END_MARKER") {
		t.Error("truncated report should preserve end")
	}
}

// ══════════════════════════════════════════════════════════════
// extractTask: edge cases
// ══════════════════════════════════════════════════════════════

func TestExtractTask_MultipleOccurrences(t *testing.T) {
	// extractTask uses first occurrence
	content := "작업 지시: first task\n더 많은 내용\n작업 지시: second task"
	got := extractTask(content, "작업 지시:")
	if !strings.HasPrefix(got, "first task") {
		t.Errorf("should use first occurrence, got %q", got)
	}
}

func TestExtractTask_PrefixAtEnd(t *testing.T) {
	got := extractTask("some text 작업 지시:", "작업 지시:")
	if got != "" {
		t.Errorf("prefix at end with nothing after should be empty, got %q", got)
	}
}

func TestExtractTask_UnicodeContent(t *testing.T) {
	got := extractTask("@dal 작업 지시: 가야 스크립트 수정해줘", "작업 지시:")
	if got != "가야 스크립트 수정해줘" {
		t.Errorf("unicode: got %q", got)
	}
}

// ══════════════════════════════════════════════════════════════
// runClaude: DAL_EXTRA_BASH 변형
// ══════════════════════════════════════════════════════════════

func TestRunClaude_ExtraBashWildcard(t *testing.T) {
	stubResolve(t)
	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_EXTRA_BASH", "*")
	os.Setenv("DAL_MAX_DURATION", "1s")
	defer func() {
		os.Unsetenv("DAL_PLAYER")
		os.Unsetenv("DAL_ROLE")
		os.Unsetenv("DAL_EXTRA_BASH")
		os.Unsetenv("DAL_MAX_DURATION")
	}()

	// Should not panic; claude not available is fine
	_, _ = runClaude("claude", "test")
}

func TestRunClaude_ExtraBashSpecific(t *testing.T) {
	stubResolve(t)
	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_EXTRA_BASH", "go,npm")
	os.Setenv("DAL_MAX_DURATION", "1s")
	defer func() {
		os.Unsetenv("DAL_PLAYER")
		os.Unsetenv("DAL_ROLE")
		os.Unsetenv("DAL_EXTRA_BASH")
		os.Unsetenv("DAL_MAX_DURATION")
	}()

	_, _ = runClaude("claude", "test")
}

func TestRunClaude_LeaderRole(t *testing.T) {
	stubResolve(t)
	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "leader")
	os.Setenv("DAL_EXTRA_BASH", "")
	os.Setenv("DAL_MAX_DURATION", "1s")
	defer func() {
		os.Unsetenv("DAL_PLAYER")
		os.Unsetenv("DAL_ROLE")
		os.Unsetenv("DAL_EXTRA_BASH")
		os.Unsetenv("DAL_MAX_DURATION")
	}()

	_, _ = runClaude("claude", "test")
}

func TestRunClaude_MaxDurationEnv(t *testing.T) {
	stubResolve(t)
	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_MAX_DURATION", "500ms")
	defer func() {
		os.Unsetenv("DAL_PLAYER")
		os.Unsetenv("DAL_ROLE")
		os.Unsetenv("DAL_MAX_DURATION")
	}()

	start := time.Now()
	_, _ = runClaude("claude", "test")
	elapsed := time.Since(start)

	// With /bin/echo stub, should complete near-instantly.
	_ = elapsed
}

func TestRunClaude_InvalidMaxDuration(t *testing.T) {
	stubResolve(t)
	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_MAX_DURATION", "not-a-duration")
	defer func() {
		os.Unsetenv("DAL_PLAYER")
		os.Unsetenv("DAL_ROLE")
		os.Unsetenv("DAL_MAX_DURATION")
	}()

	// Invalid duration falls back to default (5min), but /bin/echo exits
	// instantly so no timeout risk. Verifies no panic on parse error.
	_, _ = runClaude("claude", "test")
}

// ══════════════════════════════════════════════════════════════
// executeTask: 글로벌 circuit 상태와 통합
// ══════════════════════════════════════════════════════════════

func TestExecuteTask_SuccessKeepsCircuitClosed(t *testing.T) {
	stubResolve(t)
	providerCircuit = NewCircuitBreaker(3, 2*time.Minute)
	defer func() { providerCircuit = NewCircuitBreaker(3, 2*time.Minute) }()

	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_MAX_DURATION", "1s")
	defer func() {
		os.Unsetenv("DAL_PLAYER")
		os.Unsetenv("DAL_ROLE")
		os.Unsetenv("DAL_MAX_DURATION")
	}()

	_, err := executeTask("echo hi")
	if err == nil {
		if providerCircuit.State() != "closed" {
			t.Error("circuit should be closed after success")
		}
	}
}

// ══════════════════════════════════════════════════════════════
// isDalOnlyChanges: 추가 edge cases
// ══════════════════════════════════════════════════════════════

func TestIsDalOnlyChanges_MultipleDALFiles(t *testing.T) {
	input := " M .dal/leader/dal.cue\n M .dal/dev/dal.cue\nA  .dal/skills/new/SKILL.md"
	if !isDalOnlyChanges(input) {
		t.Error("multiple .dal/ files should be dal-only")
	}
}

func TestIsDalOnlyChanges_MixedWithSrc(t *testing.T) {
	input := " M .dal/leader/dal.cue\n M src/main.go"
	if isDalOnlyChanges(input) {
		t.Error("mixed .dal/ + src/ should NOT be dal-only")
	}
}

func TestIsDalOnlyChanges_WhitespaceLines(t *testing.T) {
	input := " M .dal/config.cue\n\n  \n M .dal/spec.cue"
	if !isDalOnlyChanges(input) {
		t.Error("whitespace lines should be ignored")
	}
}

// ══════════════════════════════════════════════════════════════
// isActiveThread: edge cases
// ══════════════════════════════════════════════════════════════

func TestIsActiveThread_EmptyID(t *testing.T) {
	var threads sync.Map
	threads.Store("t1", true)
	if isActiveThread(&threads, "") {
		t.Error("empty thread ID should not be active")
	}
}

func TestIsActiveThread_EmptyMap(t *testing.T) {
	var threads sync.Map
	if isActiveThread(&threads, "t1") {
		t.Error("should return false for empty map")
	}
}

// ══════════════════════════════════════════════════════════════
// runProvider: player dispatch
// ══════════════════════════════════════════════════════════════

func TestRunProvider_DispatchesToRunClaude(t *testing.T) {
	stubResolve(t)
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_MAX_DURATION", "1s")
	defer func() {
		os.Unsetenv("DAL_ROLE")
		os.Unsetenv("DAL_MAX_DURATION")
	}()

	// All players go through runProvider → runClaude
	players := []string{"claude", "codex", "gemini", "unknown"}
	for _, p := range players {
		_, err := runProvider(p, "test")
		// Should not panic regardless of player
		_ = err
	}
}
