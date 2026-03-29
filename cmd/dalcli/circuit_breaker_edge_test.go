package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func installFailingProviders(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	script := []byte("#!/bin/sh\nexit 1\n")
	for _, name := range []string{"claude", "codex"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, script, 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}

	t.Setenv("PATH", dir)
}

// ══════════════════════════════════════════════════════════════
// Circuit Breaker: timing & boundary edge cases
// ══════════════════════════════════════════════════════════════

// Zero cooldown: open→half-open 즉시 전환
func TestCB_ZeroCooldown(t *testing.T) {
	cb := NewCircuitBreaker(1, 0)
	cb.RecordFailure()
	assertState(t, cb, "open", "after failure")

	// cooldown=0 이면 ShouldFallback 호출 시 즉시 half-open
	if cb.ShouldFallback() {
		t.Error("zero cooldown should transition to half-open immediately")
	}
	assertState(t, cb, "half-open", "zero cooldown → immediate half-open")
}

// cooldown 정확히 경계에서의 동작
func TestCB_CooldownBoundary(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)
	cb.RecordFailure()

	// 40ms: 아직 cooldown 내
	time.Sleep(40 * time.Millisecond)
	if !cb.ShouldFallback() {
		t.Error("should still fallback before cooldown expires")
	}
	assertState(t, cb, "open", "before cooldown")

	// 추가 20ms (총 60ms > 50ms cooldown)
	time.Sleep(20 * time.Millisecond)
	if cb.ShouldFallback() {
		t.Error("should NOT fallback after cooldown expires")
	}
	assertState(t, cb, "half-open", "after cooldown")
}

// lastFailure가 half-open 실패 시 갱신되어 cooldown 리셋
func TestCB_HalfOpenFailureResetsCooldown(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)
	cb.RecordFailure()

	time.Sleep(60 * time.Millisecond)
	cb.ShouldFallback() // → half-open
	cb.RecordFailure()  // → open (lastFailure 갱신됨)
	assertState(t, cb, "open", "back to open")

	// 즉시 체크: 새 cooldown이 시작되었으므로 fallback
	if !cb.ShouldFallback() {
		t.Error("cooldown should restart from half-open failure time")
	}

	// 새 cooldown 기다림
	time.Sleep(60 * time.Millisecond)
	if cb.ShouldFallback() {
		t.Error("should transition to half-open after new cooldown")
	}
	assertState(t, cb, "half-open", "after new cooldown")
}

// open 상태에서 RecordSuccess → closed (cooldown 무시하고 복구)
func TestCB_SuccessWhileOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, time.Hour)
	cb.RecordFailure()
	cb.RecordFailure()
	assertState(t, cb, "open", "opened")

	// open 상태에서 직접 RecordSuccess 호출
	cb.RecordSuccess()
	assertState(t, cb, "closed", "success resets even from open")
	if cb.ShouldFallback() {
		t.Error("should not fallback after success reset")
	}
}

// ══════════════════════════════════════════════════════════════
// Concurrent stress test: race detector 검증
// ══════════════════════════════════════════════════════════════

func TestCB_ConcurrentRapidTransitions(t *testing.T) {
	cb := NewCircuitBreaker(2, 1*time.Millisecond)
	var wg sync.WaitGroup

	// 100 goroutine이 동시에 상태 전이
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			cb.RecordFailure()
		}()
		go func() {
			defer wg.Done()
			cb.RecordSuccess()
		}()
		go func() {
			defer wg.Done()
			_ = cb.ShouldFallback()
			_ = cb.State()
		}()
	}
	wg.Wait()

	s := cb.State()
	valid := s == "closed" || s == "open" || s == "half-open"
	if !valid {
		t.Errorf("invalid state after stress: %q", s)
	}
}

// ══════════════════════════════════════════════════════════════
// isRetryable / isAuthError: 실제 provider 에러 메시지 패턴
// ══════════════════════════════════════════════════════════════

func TestIsRetryable_RealWorldMessages(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		// Claude API 실제 에러 형식
		{"Error: 429 Too Many Requests - Your request was rate limited", true},
		{"Error: 529 Server Overloaded - The API is temporarily overloaded", true},
		{"API Error: rate limit exceeded, please retry after 30 seconds", true},
		{"Error: server at capacity, try again later", true},

		// Claude 사용량 제한 (실제 발생 — 시간은 매번 다름)
		{"You've hit your limit · resets 6pm (UTC)", true},
		{"You've hit your limit", true},
		{"Error: usage limit reached for this billing period", true},
		{"API quota exceeded, retry after reset", true},
		{"daily limit exceeded", true},

		// 복합 메시지 (retryable 키워드 포함)
		{"connection error followed by 429", true},
		{"npm ERR! 429 Too Many Requests", true},

		// 알려진 false positive (부분문자열 매칭 한계, 정규식 도입 시 개선 가능)
		{"port 4290 is in use", true},              // 429 부분문자열 매칭
		{"file at line 529", true},                 // 529 부분문자열 매칭
		{"user has capacity to handle this", true}, // capacity 포함

		// 빈 문자열 / 공백
		{"", false},
		{"   ", false},

		// timeout은 retryable이 아님
		{"TIMEOUT: task exceeded 5m0s", false},
		{"context deadline exceeded", false},
	}
	for _, tt := range tests {
		if got := isRetryable(tt.msg); got != tt.want {
			t.Errorf("isRetryable(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestIsAuthError_RealWorldMessages(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		// Claude 실제 에러 형식
		{"Error: 401 Unauthorized - Invalid API key", true},
		{"authentication_error: Your API key is invalid", true},
		{"Error: Failed to authenticate with the provided credentials", true},
		{"OAuth token has expired. Please run 'claude auth login'.", true},

		// Codex 에러 형식
		{"401 - Invalid authentication token", true},
		{"Failed to authenticate: token expired", true},

		// false positive 방지
		{"Status: 200 OK, authenticated successfully", false},
		// 알려진 false positive (부분문자열 매칭 한계)
		{"Auth middleware initialized on port 8401", true}, // 8401에 401 포함

		// 빈 문자열
		{"", false},
	}
	for _, tt := range tests {
		if got := isAuthError(tt.msg); got != tt.want {
			t.Errorf("isAuthError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

// 긴 출력에서도 키워드 탐지
func TestIsRetryable_LongOutput(t *testing.T) {
	padding := strings.Repeat("normal log line\n", 500)
	msg := padding + "Error: 429 Too Many Requests\n" + padding
	if !isRetryable(msg) {
		t.Error("should detect rate limit in long output")
	}
}

func TestIsAuthError_LongOutput(t *testing.T) {
	padding := strings.Repeat("processing step ok\n", 500)
	msg := padding + "authentication_error: expired token\n" + padding
	if !isAuthError(msg) {
		t.Error("should detect auth error in long output")
	}
}

// ══════════════════════════════════════════════════════════════
// classifyTaskError 와 circuit breaker 분류의 직교성
// ══════════════════════════════════════════════════════════════

// retryable/auth 에러는 classifyTaskError에서 unknown이어야 함
// (circuit breaker가 처리, self-repair는 개입하지 않음)
func TestClassify_RetryableIsUnknown(t *testing.T) {
	retryables := []string{
		"rate limit exceeded",
		"429 Too Many Requests",
		"server overloaded",
	}
	for _, msg := range retryables {
		if class := classifyTaskError(msg); class != ErrClassUnknown {
			t.Errorf("classifyTaskError(%q) = %q, want unknown (circuit breaker handles this)", msg, class)
		}
	}
}

func TestClassify_AuthIsUnknown(t *testing.T) {
	auths := []string{
		"authentication_error: bad key",
		"OAuth token has expired",
		"Failed to authenticate",
	}
	for _, msg := range auths {
		if class := classifyTaskError(msg); class != ErrClassUnknown {
			t.Errorf("classifyTaskError(%q) = %q, want unknown", msg, class)
		}
	}
}

// ══════════════════════════════════════════════════════════════
// notifyCredentialRefresh: HTTP 호출 검증
// ══════════════════════════════════════════════════════════════

func TestNotifyCredentialRefresh_WithServer(t *testing.T) {
	credentialRefreshCooldown.mu.Lock()
	credentialRefreshCooldown.last = make(map[string]time.Time)
	credentialRefreshCooldown.mu.Unlock()

	var claimBody string
	var messageBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api/claim":
			claimBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"claim-0001","status":"open"}`))
			return
		case "/api/message":
			messageBody = string(b)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	os.Setenv("DAL_PLAYER", "codex")
	defer os.Unsetenv("DALCENTER_URL")
	defer os.Unsetenv("DAL_PLAYER")

	notifyCredentialRefresh("test-dal")

	if !strings.Contains(claimBody, "test-dal") {
		t.Errorf("claim body should contain dal name, got: %s", claimBody)
	}
	if !strings.Contains(claimBody, "pve-sync-creds") || !strings.Contains(claimBody, "blocked") {
		t.Errorf("claim body should request host sync, got: %s", claimBody)
	}
	if !strings.Contains(messageBody, "claim-0001") || !strings.Contains(messageBody, "credential") {
		t.Errorf("message body should include claim id and credential notice, got: %s", messageBody)
	}
}

func TestNotifyCredentialRefresh_ServerDown(t *testing.T) {
	credentialRefreshCooldown.mu.Lock()
	credentialRefreshCooldown.last = make(map[string]time.Time)
	credentialRefreshCooldown.mu.Unlock()

	os.Setenv("DALCENTER_URL", "http://127.0.0.1:1") // 연결 불가 포트
	defer os.Unsetenv("DALCENTER_URL")

	// panic 없이 리턴해야 함
	notifyCredentialRefresh("test-dal")
}

func TestNotifyCredentialRefresh_DedupesWithinCooldown(t *testing.T) {
	credentialRefreshCooldown.mu.Lock()
	credentialRefreshCooldown.last = make(map[string]time.Time)
	credentialRefreshCooldown.mu.Unlock()

	var claimCount int
	var messageCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/claim":
			claimCount++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"claim-0002","status":"open"}`))
			return
		case "/api/message":
			messageCount++
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	notifyCredentialRefresh("test-dal")
	notifyCredentialRefresh("test-dal")

	if claimCount != 1 {
		t.Fatalf("claim count = %d, want 1", claimCount)
	}
	if messageCount != 1 {
		t.Fatalf("message count = %d, want 1", messageCount)
	}
}

// ══════════════════════════════════════════════════════════════
// detectFallback: player별 분기
// ══════════════════════════════════════════════════════════════

func TestDetectFallback_AllPlayers(t *testing.T) {
	players := []string{"claude", "codex", "gemini", "", "unknown-player"}
	for _, p := range players {
		fb := detectFallback(p)
		// gemini, empty, unknown → no fallback
		if p != "claude" && p != "codex" && fb != "" {
			t.Errorf("detectFallback(%q) = %q, want empty", p, fb)
		}
		// claude/codex → 바이너리 있으면 상대방, 없으면 빈 문자열 (둘 다 valid)
	}
}

// ══════════════════════════════════════════════════════════════
// executeTask: 통합 시나리오 (실제 바이너리 없는 환경)
// ══════════════════════════════════════════════════════════════

// 모든 provider 에러는 circuit breaker에 기록되어야 함
func TestExecuteTask_AnyErrorRecordsFailure(t *testing.T) {
	providerCircuit = NewCircuitBreaker(3, 2*time.Minute)
	defer func() { providerCircuit = NewCircuitBreaker(3, 2*time.Minute) }()
	installFailingProviders(t)

	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_MAX_DURATION", "1s")
	defer os.Unsetenv("DAL_PLAYER")
	defer os.Unsetenv("DAL_ROLE")
	defer os.Unsetenv("DAL_MAX_DURATION")

	// claude 바이너리 없으면 에러 → RecordFailure 호출됨
	_, err := executeTask("test")
	if err == nil {
		t.Log("claude available — skip")
		return
	}

	// 에러 발생 시 circuit에 failure 기록됨
	s := providerCircuit.State()
	if s == "closed" {
		// threshold=3이므로 1회 실패로는 아직 closed일 수 있지만
		// 실패가 기록되었는지 간접 확인: 2번 더 실패하면 open
		_, _ = executeTask("test")
		_, _ = executeTask("test")
		if providerCircuit.State() != "open" {
			t.Errorf("3 failures should open circuit, got %s", providerCircuit.State())
		}
	}
}

// 반복 실패 시 circuit이 열려야 함
func TestExecuteTask_RepeatedFailuresOpenCircuit(t *testing.T) {
	providerCircuit = NewCircuitBreaker(3, 2*time.Minute)
	defer func() { providerCircuit = NewCircuitBreaker(3, 2*time.Minute) }()
	installFailingProviders(t)

	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_MAX_DURATION", "1s")
	defer os.Unsetenv("DAL_PLAYER")
	defer os.Unsetenv("DAL_ROLE")
	defer os.Unsetenv("DAL_MAX_DURATION")

	for i := 0; i < 5; i++ {
		_, _ = executeTask("test")
	}

	_, err := executeTask("test")
	if err == nil {
		t.Log("claude available — skip")
		return
	}

	if providerCircuit.State() == "closed" {
		t.Errorf("circuit should be open after repeated failures, got %s", providerCircuit.State())
	}
}

// ══════════════════════════════════════════════════════════════
// Circuit Breaker: 다중 인스턴스 독립성
// ══════════════════════════════════════════════════════════════

func TestCB_MultipleInstances_Independent(t *testing.T) {
	cb1 := NewCircuitBreaker(1, time.Minute)
	cb2 := NewCircuitBreaker(1, time.Minute)

	cb1.RecordFailure()
	assertState(t, cb1, "open", "cb1 should be open")
	assertState(t, cb2, "closed", "cb2 should be independent")

	cb2.RecordFailure()
	assertState(t, cb2, "open", "cb2 now open")

	cb1.RecordSuccess()
	assertState(t, cb1, "closed", "cb1 reset")
	assertState(t, cb2, "open", "cb2 still open")
}

// ══════════════════════════════════════════════════════════════
// self-repair과 circuit breaker 우선순위 분리
// ══════════════════════════════════════════════════════════════

// self-repair의 classifyTaskError와 isRetryable/isAuthError 사이에
// 잘못된 교차 분류가 없는지 전수 검사
func TestErrorClassification_Exhaustive(t *testing.T) {
	cases := []struct {
		output        string
		retryable     bool
		authErr       bool
		selfRepairEnv bool
	}{
		// rate limit 계열 → retryable만
		{"rate limit exceeded", true, false, false},
		{"429", true, false, false},
		{"overloaded", true, false, false},
		{"You've hit your limit", true, false, false},
		{"quota exceeded", true, false, false},

		// auth 계열 → authErr만
		{"authentication_error", false, true, false},
		{"OAuth token has expired", false, true, false},

		// env 계열 → self-repair만
		{"command not found", false, false, true},
		{"no such file or directory", false, false, true},
		{"permission denied", false, false, true},

		// 순수 에러 → 어디에도 안 걸림
		{"exit status 1", false, false, false},
		{"panic: runtime error", false, false, false},
	}

	for _, tt := range cases {
		r := isRetryable(tt.output)
		a := isAuthError(tt.output)
		e := classifyTaskError(tt.output) == ErrClassEnv

		if r != tt.retryable {
			t.Errorf("isRetryable(%q) = %v, want %v", tt.output, r, tt.retryable)
		}
		if a != tt.authErr {
			t.Errorf("isAuthError(%q) = %v, want %v", tt.output, a, tt.authErr)
		}
		if e != tt.selfRepairEnv {
			t.Errorf("classifyTaskError(%q)==env: %v, want %v", tt.output, e, tt.selfRepairEnv)
		}
	}
}

// ══════════════════════════════════════════════════════════════
// Circuit Breaker: 연속 3회 사이클 (open→recover→open→recover→open)
// ══════════════════════════════════════════════════════════════

func TestCB_ThreeCycles(t *testing.T) {
	cb := NewCircuitBreaker(1, 20*time.Millisecond)

	for cycle := 1; cycle <= 3; cycle++ {
		cb.RecordFailure()
		assertState(t, cb, "open", fmt.Sprintf("cycle %d: open", cycle))

		time.Sleep(30 * time.Millisecond)
		cb.ShouldFallback()
		assertState(t, cb, "half-open", fmt.Sprintf("cycle %d: half-open", cycle))

		cb.RecordSuccess()
		assertState(t, cb, "closed", fmt.Sprintf("cycle %d: recovered", cycle))
	}
}

// ══════════════════════════════════════════════════════════════
// 401 false positive: 포트번호 8401 등
// ══════════════════════════════════════════════════════════════

func TestIsAuthError_FalsePositive_PortNumber(t *testing.T) {
	// 현재 구현은 "401"을 Contains로 검사하므로 8401도 매칭됨
	// 이 테스트는 현재 동작을 문서화하고,
	// 나중에 정규식으로 개선할 때 변경 포인트로 사용
	msg := "server started on port 8401"
	got := isAuthError(msg)
	// 현재 구현: true (부분문자열 매칭)
	if !got {
		t.Log("port 8401 no longer false-positive — regex improvement applied?")
	}
}

func TestIsRetryable_FalsePositive_PortAndLine(t *testing.T) {
	// 마찬가지로 "429"가 포트/라인번호에 매칭되는 현재 동작 문서화
	cases := []struct {
		msg  string
		note string
	}{
		{"listening on port 4290", "port 4290 contains 429"},
		{"error at line 529 in main.go", "line 529 contains 529"},
	}
	for _, tt := range cases {
		got := isRetryable(tt.msg)
		// 현재 동작 기록: false positive 가능성 있음
		t.Logf("isRetryable(%q) = %v — %s", tt.msg, got, tt.note)
	}
}
