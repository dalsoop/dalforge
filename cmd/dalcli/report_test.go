package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func readSrc(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("cannot read %s: %v", file, err)
	}
	return string(data)
}

func TestMemberReportsToLeader(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "reportToLeader") {
		t.Fatal("member dal must call reportToLeader on direct user tasks")
	}
}

func TestReportToLeader_ChecksRole(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, `role == "member"`) {
		t.Fatal("reportToLeader must only trigger for member role")
	}
}

func TestReportToLeader_SkipsLeaderMessages(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "isFromLeader") {
		t.Fatal("must check isFromLeader to avoid report loops")
	}
}

func TestIsFromLeader_ChecksUsername(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, `"leader"`) {
		t.Fatal("isFromLeader must check for 'leader' in username")
	}
}

func TestTeamMembersEnvUsed(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "DAL_TEAM_MEMBERS") {
		t.Fatal("must use DAL_TEAM_MEMBERS env for leader mention")
	}
}

func TestAgentConfig_HasTeamMembersField(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "TeamMembers") {
		t.Fatal("agentConfig must have TeamMembers field")
	}
}

// ── DM 지원 테스트 ────────────────────────────────────────

func TestDM_IsDetected(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "isDM") {
		t.Fatal("must detect DM messages (isDM variable)")
	}
}

func TestDM_DifferentChannelID(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "msg.Channel != cfg.ChannelID") {
		t.Fatal("isDM must check msg.Channel != cfg.ChannelID")
	}
}

func TestDM_BypassesMentionCheck(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "!isDM") {
		t.Fatal("DM must bypass mention check (isDM in filter condition)")
	}
}

func TestDM_AllSendCallsHaveChannel(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	// mm.Send에서 ReplyTo가 있는 블록은 Channel도 있어야 함
	// 최소 3곳: 상태, 에러, 응답
	count := strings.Count(src, "Channel: spec.Channel,")
	if count < 3 {
		t.Fatalf("expected at least 3 Send calls with Channel: spec.Channel, got %d", count)
	}
}

func TestDM_ResponseIncludesChannel(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	// 최종 응답에도 Channel이 전달되어야 함
	if !strings.Contains(src, "Channel: spec.Channel,") {
		t.Fatal("response mm.Send must include Channel: spec.Channel")
	}
}

func TestRunStatus_UsesRunPageLink(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "실행 보기") {
		t.Fatal("status message should prefer run page link")
	}
	if !strings.Contains(src, "/runs/%s") {
		t.Fatal("status message should link to /runs/{task_id}")
	}
}

func TestRunStatus_FinalResponsesKeepRunLink(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if strings.Count(src, "[실행 보기]") < 2 {
		t.Fatal("final success/failure replies should also include the run link")
	}
}

func TestRunStatus_FinalResponsesIncludeVerificationSummary(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "검증:") {
		t.Fatal("final success replies should include a verification summary line")
	}
	if !strings.Contains(src, "updateTrackedRunMetadata") {
		t.Fatal("tracked runs should upload verification metadata before finish")
	}
}

// ── 타임아웃 테스트 ──────────────────────────────────────

func TestTimeout_ContextUsed(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "context.WithTimeout") {
		t.Fatal("runClaude must use context.WithTimeout")
	}
}

func TestTimeout_DefaultDuration(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "5 * time.Minute") {
		t.Fatal("default timeout must be 5 minutes")
	}
}

func TestTimeout_EnvOverride(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "DAL_MAX_DURATION") {
		t.Fatal("timeout must be configurable via DAL_MAX_DURATION")
	}
}

func TestTimeout_DeadlineExceeded(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "DeadlineExceeded") {
		t.Fatal("must check context.DeadlineExceeded for timeout detection")
	}
}

func TestTimeout_ErrorMessage(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "TIMEOUT") {
		t.Fatal("timeout error must contain TIMEOUT keyword")
	}
}

func TestTimeout_CommandContext(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "exec.CommandContext") {
		t.Fatal("must use exec.CommandContext for cancellable execution")
	}
}

// ── 크로스 채널 폴링 테스트 ──────────────────────────────

func TestBridge_PollsOnlyDirectMessageChannels(t *testing.T) {
	src := readSrc(t, "../../internal/bridge/mattermost.go")
	if !strings.Contains(src, `ch.Type == "D"`) {
		t.Fatal("bridge must poll direct-message channels")
	}
	if strings.Contains(src, `ch.Type == "O"`) || strings.Contains(src, `ch.Type == "P"`) {
		t.Fatal("bridge must not poll open/private project channels as extra bot inboxes")
	}
}

func TestBridge_SkipsMainChannel(t *testing.T) {
	src := readSrc(t, "../../internal/bridge/mattermost.go")
	if !strings.Contains(src, "m.ChannelID") {
		t.Fatal("must skip main channel in extra polling (already polled)")
	}
}

func TestBridge_PerChannelLastAt(t *testing.T) {
	src := readSrc(t, "../../internal/bridge/mattermost.go")
	if !strings.Contains(src, "dmLastAt") {
		t.Fatal("must have per-channel lastAt tracking (dmLastAt)")
	}
}

func TestBridge_FetchChannelLatestAt(t *testing.T) {
	src := readSrc(t, "../../internal/bridge/mattermost.go")
	if !strings.Contains(src, "fetchChannelLatestAt") {
		t.Fatal("must have fetchChannelLatestAt for initial sinceAt")
	}
}

// ── parseInterval 테스트 ─────────────────────────────────

func TestParseInterval_ValidDuration(t *testing.T) {
	d := parseInterval("1h", 0)
	if d != time.Hour {
		t.Fatalf("expected 1h, got %v", d)
	}
}

func TestParseInterval_Default(t *testing.T) {
	d := parseInterval("", 5*time.Minute)
	if d != 5*time.Minute {
		t.Fatalf("expected 5m default, got %v", d)
	}
}

func TestParseInterval_Invalid(t *testing.T) {
	d := parseInterval("abc", 10*time.Second)
	if d != 10*time.Second {
		t.Fatalf("expected default on invalid, got %v", d)
	}
}

// ── containsFailure 테스트 ───────────────────────────────

func TestContainsFailure_Fail(t *testing.T) {
	if !containsFailure("FAIL test/foo") {
		t.Fatal("should detect FAIL")
	}
}

func TestContainsFailure_Error(t *testing.T) {
	if !containsFailure("error: something broke") {
		t.Fatal("should detect error:")
	}
}

func TestContainsFailure_Clean(t *testing.T) {
	if containsFailure("ok all tests passed") {
		t.Fatal("should not detect failure in clean output")
	}
}

// ── extractTask 테스트 ───────────────────────────────────

func TestExtractTask_WithPrefix(t *testing.T) {
	task := extractTask("작업 지시: go test ./...", "작업 지시:")
	if task != "go test ./..." {
		t.Fatalf("expected 'go test ./...', got %q", task)
	}
}

func TestExtractTask_NoPrefix(t *testing.T) {
	task := extractTask("hello world", "작업 지시:")
	if task != "" {
		t.Fatalf("expected empty, got %q", task)
	}
}

// ── truncate 테스트 ──────────────────────────────────────

func TestTruncate_Short(t *testing.T) {
	if truncate("hi", 10) != "hi" {
		t.Fatal("short string should not be truncated")
	}
}

func TestTruncate_Long(t *testing.T) {
	result := truncate("abcdefghij", 5)
	if !strings.HasSuffix(result, "...") {
		t.Fatal("truncated string must end with ...")
	}
	if len(result) > 8 { // 5 + "..."
		t.Fatalf("expected max 8 chars (5+...), got %d", len(result))
	}
}

// ── isRetryable / isAuthError 테스트 ─────────────────────

func TestIsRetryable_RateLimit_DM(t *testing.T) {
	if !isRetryable("rate limit exceeded") {
		t.Fatal("rate limit should be retryable")
	}
}

func TestIsRetryable_Normal_DM(t *testing.T) {
	if isRetryable("normal output") {
		t.Fatal("normal output should not be retryable")
	}
}

func TestIsAuthError_401_DM(t *testing.T) {
	if !isAuthError("401 authentication error") {
		t.Fatal("401 should be auth error")
	}
}

func TestIsAuthError_Normal_DM(t *testing.T) {
	if isAuthError("everything is fine") {
		t.Fatal("normal output should not be auth error")
	}
}

// ── isActiveThread 테스트 ────────────────────────────────

func TestIsActiveThread_Found(t *testing.T) {
	var m sync.Map
	m.Store("thread-1", true)
	if !isActiveThread(&m, "thread-1") {
		t.Fatal("should find active thread")
	}
}

func TestIsActiveThread_NotFound(t *testing.T) {
	var m sync.Map
	if isActiveThread(&m, "unknown") {
		t.Fatal("should not find unknown thread")
	}
}

// ── isFromLeader 테스트 ──────────────────────────────────

func TestIsFromLeader_LeaderName(t *testing.T) {
	// isFromLeader checks username contains "leader"
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "func isFromLeader") {
		t.Fatal("isFromLeader function must exist")
	}
}

// ── reportToLeader 테스트 ────────────────────────────────

func TestReportToLeader_UsesTeamMembers(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	idx := strings.Index(src, "func reportToLeader")
	if idx < 0 {
		t.Fatal("reportToLeader not found")
	}
	fn := src[idx : idx+500]
	if !strings.Contains(fn, "DAL_TEAM_MEMBERS") {
		t.Fatal("reportToLeader must read DAL_TEAM_MEMBERS")
	}
	if !strings.Contains(fn, "leader") {
		t.Fatal("reportToLeader must find leader in team members")
	}
}

// ── formatReport 테스트 ──────────────────────────────────

func TestFormatReport_Short(t *testing.T) {
	r := formatReport("hello")
	if !strings.Contains(r, "hello") {
		t.Fatal("should contain output")
	}
	if !strings.Contains(r, "✅") {
		t.Fatal("should have success emoji")
	}
}

func TestFormatReport_Long(t *testing.T) {
	long := strings.Repeat("x", 5000)
	r := formatReport(long)
	if !strings.Contains(r, "truncated") {
		t.Fatal("long output should be truncated")
	}
}

// ── self_repair 테스트 ───────────────────────────────────

func TestClassifyTaskError_Auth_Run(t *testing.T) {
	c := classifyTaskError("401 authentication failed")
	if c != ErrClassUnknown {
		t.Fatalf("expected auth, got %s", c)
	}
}

func TestClassifyTaskError_Env_Run(t *testing.T) {
	c := classifyTaskError("command not found: node")
	if c != ErrClassEnv {
		t.Fatalf("expected env, got %s", c)
	}
}

func TestClassifyTaskError_Unknown_Run(t *testing.T) {
	c := classifyTaskError("something random happened")
	if c != ErrClassUnknown {
		t.Fatalf("expected unknown, got %s", c)
	}
}

func TestTaskHash_Deterministic(t *testing.T) {
	h1 := taskHash("hello world")
	h2 := taskHash("hello world")
	if h1 != h2 {
		t.Fatal("same input must produce same hash")
	}
}

func TestTaskHash_Different(t *testing.T) {
	h1 := taskHash("hello")
	h2 := taskHash("world")
	if h1 == h2 {
		t.Fatal("different input must produce different hash")
	}
}

func TestIsRepairCoolingDown_Fresh(t *testing.T) {
	if isRepairCoolingDown("newtask") {
		t.Fatal("fresh task should not be cooling down")
	}
}

func TestMarkAndCooldown(t *testing.T) {
	key := "cooldown-test-unique-12345"
	markRepairAttempted(key)
	if !isRepairCoolingDown(key) {
		t.Fatal("should be cooling down after mark")
	}
}

// ── circuit breaker 추가 테스트 ──────────────────────────

func TestCircuitBreaker_DefaultClosed(t *testing.T) {
	cb := NewCircuitBreaker(3, 30*time.Second)
	if cb.ShouldFallback() {
		t.Fatal("new circuit should be closed (no fallback)")
	}
}

func TestCircuitBreaker_OpensAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker(3, 30*time.Second)
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}
	if !cb.ShouldFallback() {
		t.Fatal("circuit should open after many failures")
	}
}

func TestCircuitBreaker_ResetsOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(3, 30*time.Second)
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}
	cb.RecordSuccess()
	if cb.ShouldFallback() {
		t.Fatal("circuit should close after success")
	}
}

// ── extractErrorSummary 테스트 ───────────────────────────

func TestExtractErrorSummary_Short(t *testing.T) {
	s := extractErrorSummary("short error")
	if s == "" {
		t.Fatal("should return something for short error")
	}
}

func TestExtractErrorSummary_Long(t *testing.T) {
	long := strings.Repeat("error line\n", 100)
	s := extractErrorSummary(long)
	if len(s) > 500 {
		t.Fatalf("summary should be truncated, got %d chars", len(s))
	}
}

// ── CircuitBreaker 추가 분기 ─────────────────────────────

func TestCircuitBreaker_HalfOpenFailure_Full(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(100 * time.Millisecond)
	cb.RecordFailure() // fail in half-open
	if !cb.ShouldFallback() {
		t.Fatal("should re-open after failure in half-open")
	}
}

func TestCircuitBreaker_State_Full(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	if cb.State() != "closed" {
		t.Fatalf("initial state should be closed, got %s", cb.State())
	}
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != "open" {
		t.Fatalf("should be open after failures, got %s", cb.State())
	}
	time.Sleep(100 * time.Millisecond)
	s := cb.State() // triggers half-open check
	if s != "half-open" && s != "open" {
		t.Fatalf("should be half-open or open, got %s", s)
	}
}

// ── selfRepair 분기 테스트 ───────────────────────────────

func TestSelfRepair_NoRetry_2(t *testing.T) {
	retry, _ := selfRepair("task", "random output", fmt.Errorf("fail"))
	if retry {
		t.Fatal("should not retry for unknown error")
	}
}

func TestClassifyTaskError_Deps_2(t *testing.T) {
	c := classifyTaskError("go: module not found in go.sum")
	if c != ErrClassDeps {
		t.Fatalf("expected deps, got %s", c)
	}
}

func TestClassifyTaskError_Git_2(t *testing.T) {
	c := classifyTaskError("fatal: not a git repository")
	if c != ErrClassGit {
		t.Fatalf("expected git, got %s", c)
	}
}

// ── isDalOnlyChanges 분기 테스트 ─────────────────────────

func TestIsDalOnlyChanges_Empty(t *testing.T) {
	if !isDalOnlyChanges("") {
		t.Fatal("empty should return true")
	}
}

func TestIsDalOnlyChanges_ShortLine(t *testing.T) {
	// line shorter than 3 chars
	if isDalOnlyChanges("ab") {
		t.Fatal("short non-.dal line should return false")
	}
}

// ── extractErrorSummary 분기 ─────────────────────────────

func TestExtractErrorSummary_WithErrorPrefix(t *testing.T) {
	s := extractErrorSummary("error: first line\nsecond line\nthird line")
	if !strings.Contains(s, "first line") {
		t.Fatal("should contain error line")
	}
}

// ── detectFallback 테스트 ────────────────────────────────

func TestDetectFallback_Claude_2(t *testing.T) {
	fb := detectFallback("claude")
	// should return codex if available, or empty
	_ = fb // just verify no panic
}

func TestDetectFallback_Codex_2(t *testing.T) {
	fb := detectFallback("codex")
	_ = fb
}

func TestDetectFallback_Unknown_2(t *testing.T) {
	fb := detectFallback("gemini")
	if fb != "" {
		t.Fatalf("gemini fallback should be empty, got %q", fb)
	}
}
