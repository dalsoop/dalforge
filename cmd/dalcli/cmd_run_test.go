package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dalsoop/dalcenter/internal/bridge"
)

func writeFakeCommand(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	script := `#!/bin/sh
stdin=$(cat)
{
  printf 'argv:'
  for arg in "$@"; do
    printf '[%s]' "$arg"
  done
  printf '\nstdin:%s\n' "$stdin"
} > "$DAL_TEST_CAPTURE"
printf '%s' "${DAL_TEST_STDOUT:-ok}"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake command: %v", err)
	}
	return dir
}

func readCapture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	return string(data)
}

func ensureWorkspaceDir(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll("/workspace", 0o755); err != nil {
		t.Fatalf("ensure /workspace: %v", err)
	}
}

// ── extractTask ──

func TestExtractTask(t *testing.T) {
	tests := []struct {
		name, content, prefix, want string
	}{
		{"standard", "@dal-checker 작업 지시: do the thing", "작업 지시:", "do the thing"},
		{"whitespace", "@dal-checker 작업 지시:   spaces   ", "작업 지시:", "spaces"},
		{"no prefix", "@dal-checker hello", "작업 지시:", ""},
		{"empty", "", "작업 지시:", ""},
		{"prefix only", "작업 지시:", "작업 지시:", ""},
		{"multiline", "@dal-writer 작업 지시: first\nsecond", "작업 지시:", "first\nsecond"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractTask(tt.content, tt.prefix); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ── truncate ──

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
		if got := truncate(tt.input, tt.n); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

// ── formatReport ──

func TestFormatReport(t *testing.T) {
	report := formatReport("done")
	if !strings.Contains(report, "✅ 작업 완료") || !strings.Contains(report, "done") {
		t.Error("should contain marker and output")
	}
}

func TestFormatReport_Truncation(t *testing.T) {
	long := strings.Repeat("a", 4000)
	report := formatReport(long)
	if !strings.Contains(report, "... (truncated) ...") {
		t.Error("should truncate long output")
	}
	if len(report) > 3500 {
		t.Errorf("too long: %d", len(report))
	}
}

func TestFormatReport_Empty(t *testing.T) {
	if !strings.Contains(formatReport(""), "✅ 작업 완료") {
		t.Error("empty should still show success")
	}
}

// ── Free-form mention parsing ──

func TestFreeFormMention_DirectMention(t *testing.T) {
	mention := "@dal-leader"
	tests := []struct {
		name, content, want string
	}{
		{"free text", "@dal-leader 가야가 밋밋해", "가야가 밋밋해"},
		{"assign takes priority", "@dal-leader 작업 지시: 해줘", "해줘"},
		{"mention only", "@dal-leader", ""},
		{"mention + spaces", "@dal-leader    ", ""},
		{"mid mention", "hey @dal-leader 봐봐", "hey  봐봐"},
		{"korean sentence", "@dal-leader 스토리 좀 고쳐줘 가야가 너무 수동적이야", "스토리 좀 고쳐줘 가야가 너무 수동적이야"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := extractTask(tt.content, "작업 지시:")
			if task == "" {
				task = strings.TrimSpace(strings.ReplaceAll(tt.content, mention, ""))
			}
			if task != tt.want {
				t.Errorf("got %q, want %q", task, tt.want)
			}
		})
	}
}

// ── Message type detection ──

func TestMessageTypeDetection(t *testing.T) {
	mention := "@dal-writer"
	assignPrefix := "작업 지시:"
	var threads sync.Map
	threads.Store("thread-123", true)

	tests := []struct {
		name           string
		content        string
		rootID         string
		wantAssignment bool
		wantMention    bool
		wantThread     bool
		wantProcess    bool
	}{
		{
			name:           "assignment",
			content:        "@dal-writer 작업 지시: ep04 수정해",
			rootID:         "",
			wantAssignment: true,
			wantMention:    true,
			wantProcess:    true,
		},
		{
			name:        "free mention",
			content:     "@dal-writer 가야가 밋밋해",
			rootID:      "",
			wantMention: true,
			wantProcess: true,
		},
		{
			name:        "thread reply in active thread",
			content:     "이거 좀 더 고쳐줘",
			rootID:      "thread-123",
			wantThread:  true,
			wantProcess: true,
		},
		{
			name:        "thread reply in unknown thread",
			content:     "이거 좀 더 고쳐줘",
			rootID:      "unknown-thread",
			wantProcess: false,
		},
		{
			name:        "no mention no thread",
			content:     "그냥 채널 메시지",
			rootID:      "",
			wantProcess: false,
		},
		{
			name:        "other dal mention",
			content:     "@dal-architect 구조 분석해",
			rootID:      "",
			wantProcess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isDirectMention := strings.Contains(tt.content, mention)
			isAssignment := isDirectMention && strings.Contains(tt.content, assignPrefix)
			isThreadReply := tt.rootID != "" && isActiveThread(&threads, tt.rootID)
			shouldProcess := isAssignment || isDirectMention || isThreadReply

			if isAssignment != tt.wantAssignment {
				t.Errorf("assignment: got %v, want %v", isAssignment, tt.wantAssignment)
			}
			if isDirectMention != tt.wantMention {
				t.Errorf("mention: got %v, want %v", isDirectMention, tt.wantMention)
			}
			if isThreadReply != tt.wantThread {
				t.Errorf("thread: got %v, want %v", isThreadReply, tt.wantThread)
			}
			if shouldProcess != tt.wantProcess {
				t.Errorf("process: got %v, want %v", shouldProcess, tt.wantProcess)
			}
		})
	}
}

// ── isActiveThread ──

func TestIsActiveThread(t *testing.T) {
	var threads sync.Map
	threads.Store("t1", true)
	threads.Store("t2", true)

	if !isActiveThread(&threads, "t1") {
		t.Error("t1 should be active")
	}
	if isActiveThread(&threads, "t3") {
		t.Error("t3 should not be active")
	}
}

// ── autoGitWorkflow ──

func TestAutoGitWorkflow_NoWorkspace(t *testing.T) {
	result := autoGitWorkflow("test-dal")
	if result != "" {
		t.Errorf("expected empty for no workspace, got %q", result)
	}
}

// ── buildThreadContext with mock MM API ──

func TestBuildThreadContext_WithAPI(t *testing.T) {
	// Mock MM server that returns thread posts
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/thread"):
			resp := map[string]interface{}{
				"order": []string{"p1", "p2", "p3"},
				"posts": map[string]interface{}{
					"p1": map[string]interface{}{"user_id": "user-devops", "message": "@dal-writer ep04 수정해"},
					"p2": map[string]interface{}{"user_id": "bot-writer", "message": "💬 작업 중..."},
					"p3": map[string]interface{}{"user_id": "user-devops", "message": "머지해봐"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/api/agent-config/writer":
			json.NewEncoder(w).Encode(agentConfig{
				DalName:   "writer",
				BotToken:  "tok",
				ChannelID: "ch1",
				MMURL:     srvURL,
			})
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	// Set env for fetchAgentConfig
	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	mm := &bridge.MattermostBridge{BotUserID: "bot-writer"}
	msg := bridge.Message{
		ID:      "p3",
		RootID:  "p1",
		From:    "user-devops",
		Content: "머지해봐",
	}

	ctx := buildThreadContext(mm, msg, "writer")

	if !strings.Contains(ctx, "ep04 수정해") {
		t.Error("should contain first message")
	}
	if !strings.Contains(ctx, "머지해봐") {
		t.Error("should contain last message")
	}
	if !strings.Contains(ctx, "나(writer)") {
		t.Error("should identify self")
	}
	if !strings.Contains(ctx, "상대방") {
		t.Error("should identify others")
	}
}

func TestBuildThreadContext_Fallback(t *testing.T) {
	// No DALCENTER_URL → fallback to single message
	os.Unsetenv("DALCENTER_URL")

	mm := &bridge.MattermostBridge{BotUserID: "bot-123"}
	msg := bridge.Message{
		Content: "테스트 메시지",
		RootID:  "root-1",
	}

	ctx := buildThreadContext(mm, msg, "test-dal")
	if !strings.Contains(ctx, "테스트 메시지") {
		t.Error("fallback should contain message content")
	}
}

func TestBuildThreadContext_LimitsMessages(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/thread"):
			resp := map[string]interface{}{
				"order": []string{"p1", "p2", "p3", "p4"},
				"posts": map[string]interface{}{
					"p1": map[string]interface{}{"user_id": "user-devops", "message": "첫 번째"},
					"p2": map[string]interface{}{"user_id": "bot-writer", "message": "두 번째"},
					"p3": map[string]interface{}{"user_id": "user-devops", "message": "세 번째"},
					"p4": map[string]interface{}{"user_id": "bot-writer", "message": "네 번째"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/api/agent-config/writer":
			json.NewEncoder(w).Encode(agentConfig{
				DalName:   "writer",
				BotToken:  "tok",
				ChannelID: "ch1",
				MMURL:     srvURL,
			})
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	t.Setenv("DALCENTER_URL", srv.URL)
	t.Setenv("DAL_THREAD_CONTEXT_MESSAGES", "2")
	t.Setenv("DAL_THREAD_CONTEXT_CHARS", "6000")

	mm := &bridge.MattermostBridge{BotUserID: "bot-writer"}
	msg := bridge.Message{
		ID:      "p4",
		RootID:  "p1",
		From:    "user-devops",
		Content: "네 번째",
	}

	ctx := buildThreadContext(mm, msg, "writer")

	if !strings.Contains(ctx, "(최근 2개 메시지만 포함)") {
		t.Fatal("should mention message truncation")
	}
	if strings.Contains(ctx, "첫 번째") || strings.Contains(ctx, "두 번째") {
		t.Fatal("should drop older messages from thread context")
	}
	if !strings.Contains(ctx, "세 번째") || !strings.Contains(ctx, "네 번째") {
		t.Fatal("should retain most recent messages")
	}
}

func TestBuildThreadContext_LimitsChars(t *testing.T) {
	var srvURL string
	longFirst := strings.Repeat("가", 80)
	longSecond := strings.Repeat("나", 80)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/thread"):
			resp := map[string]interface{}{
				"order": []string{"p1", "p2"},
				"posts": map[string]interface{}{
					"p1": map[string]interface{}{"user_id": "user-devops", "message": longFirst},
					"p2": map[string]interface{}{"user_id": "bot-writer", "message": longSecond},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/api/agent-config/writer":
			json.NewEncoder(w).Encode(agentConfig{
				DalName:   "writer",
				BotToken:  "tok",
				ChannelID: "ch1",
				MMURL:     srvURL,
			})
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	t.Setenv("DALCENTER_URL", srv.URL)
	t.Setenv("DAL_THREAD_CONTEXT_MESSAGES", "8")
	t.Setenv("DAL_THREAD_CONTEXT_CHARS", "120")

	mm := &bridge.MattermostBridge{BotUserID: "bot-writer"}
	msg := bridge.Message{
		ID:      "p2",
		RootID:  "p1",
		From:    "user-devops",
		Content: longSecond,
	}

	ctx := buildThreadContext(mm, msg, "writer")

	if !strings.Contains(ctx, "...(이전 스레드 내용 생략)...") {
		t.Fatal("should include truncation marker when char cap is exceeded")
	}
	if len(ctx) <= 120 {
		t.Fatal("expected explanatory suffix beyond raw cap")
	}
}

func TestParsePositiveEnvInt(t *testing.T) {
	t.Setenv("DAL_THREAD_CONTEXT_MESSAGES", "12")
	if got := parsePositiveEnvInt("DAL_THREAD_CONTEXT_MESSAGES", 8); got != 12 {
		t.Fatalf("got %d, want 12", got)
	}

	t.Setenv("DAL_THREAD_CONTEXT_MESSAGES", "0")
	if got := parsePositiveEnvInt("DAL_THREAD_CONTEXT_MESSAGES", 8); got != 8 {
		t.Fatalf("zero should fall back, got %d", got)
	}

	t.Setenv("DAL_THREAD_CONTEXT_MESSAGES", "bad")
	if got := parsePositiveEnvInt("DAL_THREAD_CONTEXT_MESSAGES", 8); got != 8 {
		t.Fatalf("invalid value should fall back, got %d", got)
	}
}

// ── fetchAgentConfig ──

func TestFetchAgentConfig_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent-config/writer" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(agentConfig{
			DalName:   "writer",
			BotToken:  "tok-123",
			ChannelID: "ch-abc",
			MMURL:     "http://mm:8065",
		})
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	cfg, err := fetchAgentConfig("writer")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if cfg.BotToken != "tok-123" {
		t.Errorf("token = %q", cfg.BotToken)
	}
	if cfg.MMURL != "http://mm:8065" {
		t.Errorf("url = %q", cfg.MMURL)
	}
}

func TestFetchAgentConfig_NoURL(t *testing.T) {
	os.Unsetenv("DALCENTER_URL")
	_, err := fetchAgentConfig("test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchAgentConfig_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	}))
	defer srv.Close()

	os.Setenv("DALCENTER_URL", srv.URL)
	defer os.Unsetenv("DALCENTER_URL")

	_, err := fetchAgentConfig("test")
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

// ── executeTask role branching (verify command construction) ──

func TestExecuteTask_RoleBranching(t *testing.T) {
	// We can't actually run claude in tests, but we verify the function
	// handles missing binary gracefully
	os.Setenv("DAL_ROLE", "member")
	_, err := executeTask("test")
	// Should fail (claude not available in test) but not panic
	if err == nil {
		t.Log("claude available in test env — unusual but ok")
	}

	os.Setenv("DAL_ROLE", "leader")
	_, err = executeTask("test")
	if err == nil {
		t.Log("claude available in test env — unusual but ok")
	}
	os.Unsetenv("DAL_ROLE")
}

// ── Integration: full message routing logic ──

func TestMessageRouting_AssignmentPriority(t *testing.T) {
	// Assignment should be detected even if it's also a mention
	content := "@dal-writer 작업 지시: ep04 고쳐줘"
	mention := "@dal-writer"
	assignPrefix := "작업 지시:"

	isDirectMention := strings.Contains(content, mention)
	isAssignment := isDirectMention && strings.Contains(content, assignPrefix)

	if !isAssignment {
		t.Error("should detect as assignment")
	}

	task := extractTask(content, assignPrefix)
	if task != "ep04 고쳐줘" {
		t.Errorf("task = %q", task)
	}
}

func TestMessageRouting_FreeFormFallback(t *testing.T) {
	content := "@dal-writer 가야가 좀 수동적이야 능동적으로 바꿔줘"
	mention := "@dal-writer"
	assignPrefix := "작업 지시:"

	task := extractTask(content, assignPrefix) // empty — no prefix
	if task != "" {
		t.Errorf("should not extract from free-form: %q", task)
	}

	// Fallback: strip mention
	task = strings.TrimSpace(strings.ReplaceAll(content, mention, ""))
	expected := "가야가 좀 수동적이야 능동적으로 바꿔줘"
	if task != expected {
		t.Errorf("free-form: got %q, want %q", task, expected)
	}
}

// ── isDalOnlyChanges ──

func TestIsDalOnlyChanges(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"dal only", " M .dal/context/foo.md\n M .dal/data/claims.json", true},
		{"dal added", "?? .dal/context/new.md", true},
		{"mixed", " M .dal/context/foo.md\n M cmd/dalcli/cmd_run.go", false},
		{"no dal", " M README.md", false},
		{"rename into dal", " R old.txt -> .dal/data/new.txt", true},
		{"rename out of dal", " R .dal/old.txt -> cmd/new.txt", false},
		{"empty", "", true},
		{"single dal file", "A  .dal/spec.cue", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDalOnlyChanges(tt.input)
			if got != tt.want {
				t.Errorf("isDalOnlyChanges(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ── autoGitWorkflow branch naming ──

func TestAutoGitWorkflow_BranchFormat(t *testing.T) {
	branch := fmt.Sprintf("dal/%s/%d", "writer", 1774449828)
	if !strings.HasPrefix(branch, "dal/writer/") {
		t.Errorf("branch = %q, should start with dal/writer/", branch)
	}
}

// ── isRetryable ──

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		output string
		want   bool
	}{
		{"Error: rate limit exceeded", true},
		{"You've hit your limit · resets 6pm (UTC)", true},
		{"429 Too Many Requests", true},
		{"529 overloaded", true},
		{"API Error: too many requests", true},
		{"provider quota exceeded", true},
		{"server at capacity", true},
		{"Overloaded, please retry", true},
		{"normal error: file not found", false},
		{"authentication failed", false},
		{"", false},
		{"exit status 1", false},
	}
	for _, tt := range tests {
		got := isRetryable(tt.output)
		if got != tt.want {
			t.Errorf("isRetryable(%q) = %v, want %v", tt.output, got, tt.want)
		}
	}
}

// ── executeTask retry (no claude binary → fails fast, not retryable) ──

func TestExecuteTask_NonRetryable_NoLoop(t *testing.T) {
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_PLAYER", "claude")
	defer os.Unsetenv("DAL_ROLE")
	defer os.Unsetenv("DAL_PLAYER")

	// claude not available → error → NOT retryable → returns after 1 try
	start := time.Now()
	_, err := executeTask("test")
	elapsed := time.Since(start)

	if err == nil {
		t.Log("claude available — skip")
		return
	}
	// Should return fast (< 5s), not wait 30s for retry
	if elapsed > 10*time.Second {
		t.Errorf("took %s — seems like retry loop on non-retryable error", elapsed)
	}
}

// ── runProvider player branching ──

func TestRunProvider_Codex(t *testing.T) {
	ensureWorkspaceDir(t)
	fakeDir := writeFakeCommand(t, "codex")
	capture := filepath.Join(t.TempDir(), "codex.txt")
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))
	t.Setenv("DAL_TEST_CAPTURE", capture)
	t.Setenv("DAL_TEST_STDOUT", "codex ok")
	t.Setenv("DAL_ROLE", "member")

	out, err := runClaude("codex", "test")
	if err != nil {
		t.Fatalf("runClaude codex: %v", err)
	}
	if out != "codex ok" {
		t.Fatalf("stdout = %q", out)
	}
	got := readCapture(t, capture)
	for _, want := range []string{
		"argv:[exec][--dangerously-bypass-approvals-and-sandbox][--ephemeral][-C][/workspace][test]",
		"stdin:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capture missing %q in %q", want, got)
		}
	}
}

func TestRunProvider_Claude_Leader(t *testing.T) {
	ensureWorkspaceDir(t)
	fakeDir := writeFakeCommand(t, "claude")
	capture := filepath.Join(t.TempDir(), "claude-leader.txt")
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))
	t.Setenv("DAL_TEST_CAPTURE", capture)
	t.Setenv("DAL_TEST_STDOUT", "leader ok")
	t.Setenv("DAL_ROLE", "leader")

	out, err := runClaude("claude", "test")
	if err != nil {
		t.Fatalf("runClaude claude leader: %v", err)
	}
	if out != "leader ok" {
		t.Fatalf("stdout = %q", out)
	}
	got := readCapture(t, capture)
	for _, want := range []string{
		"[--allowedTools][Bash(dalcli-leader:*,git:*,gh:*) Read Write Glob Grep Edit]",
		"[--no-session-persistence]",
		"[--print][test]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capture missing %q in %q", want, got)
		}
	}
}

func TestRunProvider_Claude_Member(t *testing.T) {
	ensureWorkspaceDir(t)
	fakeDir := writeFakeCommand(t, "claude")
	capture := filepath.Join(t.TempDir(), "claude-member.txt")
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))
	t.Setenv("DAL_TEST_CAPTURE", capture)
	t.Setenv("DAL_TEST_STDOUT", "member ok")
	t.Setenv("DAL_ROLE", "member")

	out, err := runClaude("claude", "test")
	if err != nil {
		t.Fatalf("runClaude claude member: %v", err)
	}
	if out != "member ok" {
		t.Fatalf("stdout = %q", out)
	}
	got := readCapture(t, capture)
	for _, want := range []string{
		"[--allowedTools][Bash(git:*,gh:*) Read Write Glob Grep Edit]",
		"[--no-session-persistence]",
		"[--print][test]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capture missing %q in %q", want, got)
		}
	}
}

func TestRunProvider_Claude_ExtraBashOverride(t *testing.T) {
	ensureWorkspaceDir(t)
	fakeDir := writeFakeCommand(t, "claude")
	capture := filepath.Join(t.TempDir(), "claude-extra-bash.txt")
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))
	t.Setenv("DAL_TEST_CAPTURE", capture)
	t.Setenv("DAL_ROLE", "member")
	t.Setenv("DAL_EXTRA_BASH", "make")

	if _, err := runClaude("claude", "test"); err != nil {
		t.Fatalf("runClaude claude extra bash: %v", err)
	}
	got := readCapture(t, capture)
	if !strings.Contains(got, "[--allowedTools][Bash(git:*,gh:*,make:*) Read Write Glob Grep Edit]") {
		t.Fatalf("capture = %q", got)
	}
}

func TestProviderSessionPersistenceEnabled(t *testing.T) {
	t.Setenv("DAL_PERSIST_PROVIDER_SESSION", "")
	if providerSessionPersistenceEnabled() {
		t.Fatal("persistence should be disabled by default")
	}

	for _, v := range []string{"1", "true", "yes", "on", "TRUE"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("DAL_PERSIST_PROVIDER_SESSION", v)
			if !providerSessionPersistenceEnabled() {
				t.Fatalf("expected %q to enable persistence", v)
			}
		})
	}
}

func TestRunProvider_Claude_PersistenceOverride(t *testing.T) {
	ensureWorkspaceDir(t)
	fakeDir := writeFakeCommand(t, "claude")
	capture := filepath.Join(t.TempDir(), "claude-persist.txt")
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))
	t.Setenv("DAL_TEST_CAPTURE", capture)
	t.Setenv("DAL_ROLE", "member")
	t.Setenv("DAL_PERSIST_PROVIDER_SESSION", "true")

	if _, err := runClaude("claude", "test"); err != nil {
		t.Fatalf("runClaude claude persistent: %v", err)
	}
	got := readCapture(t, capture)
	if strings.Contains(got, "--no-session-persistence") {
		t.Fatalf("capture should omit no-session-persistence: %q", got)
	}
}

func TestRunProvider_Codex_PersistenceOverride(t *testing.T) {
	ensureWorkspaceDir(t)
	fakeDir := writeFakeCommand(t, "codex")
	capture := filepath.Join(t.TempDir(), "codex-persist.txt")
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))
	t.Setenv("DAL_TEST_CAPTURE", capture)
	t.Setenv("DAL_PERSIST_PROVIDER_SESSION", "true")

	if _, err := runClaude("codex", "test"); err != nil {
		t.Fatalf("runClaude codex persistent: %v", err)
	}
	got := readCapture(t, capture)
	if strings.Contains(got, "--ephemeral") {
		t.Fatalf("capture should omit ephemeral: %q", got)
	}
}

func TestRunProvider_ReturnsStderrOnError(t *testing.T) {
	ensureWorkspaceDir(t)
	fakeDir := t.TempDir()
	path := filepath.Join(fakeDir, "claude")
	script := `#!/bin/sh
echo "529 overloaded" >&2
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))
	t.Setenv("DAL_ROLE", "member")

	out, err := runClaude("claude", "test")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(out, "529 overloaded") {
		t.Fatalf("stderr should be returned for classification, got %q", out)
	}
}

func TestExecuteTask_FallbacksToCodexOnRetryableStderr(t *testing.T) {
	ensureWorkspaceDir(t)
	fakeDir := t.TempDir()
	claudePath := filepath.Join(fakeDir, "claude")
	codexPath := filepath.Join(fakeDir, "codex")

	claudeScript := `#!/bin/sh
echo "API Error: 529 overloaded" >&2
exit 1
`
	codexScript := `#!/bin/sh
printf '%s' "codex fallback ok"
`
	if err := os.WriteFile(claudePath, []byte(claudeScript), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	if err := os.WriteFile(codexPath, []byte(codexScript), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))
	t.Setenv("DAL_PLAYER", "claude")
	t.Setenv("DAL_ROLE", "member")

	oldCircuit := providerCircuit
	providerCircuit = NewCircuitBreaker(1, time.Minute)
	defer func() {
		providerCircuit = oldCircuit
	}()

	out, err := executeTask("test")
	if err != nil {
		t.Fatalf("executeTask should fallback to codex: %v", err)
	}
	if out != "codex fallback ok" {
		t.Fatalf("fallback output = %q", out)
	}
}

func TestExecuteTask_FallbacksToCodexOnUsageLimitMessage(t *testing.T) {
	ensureWorkspaceDir(t)
	fakeDir := t.TempDir()
	claudePath := filepath.Join(fakeDir, "claude")
	codexPath := filepath.Join(fakeDir, "codex")

	claudeScript := `#!/bin/sh
echo "You've hit your limit · resets 6pm (UTC)" >&2
exit 1
`
	codexScript := `#!/bin/sh
printf '%s' "codex fallback ok"
`
	if err := os.WriteFile(claudePath, []byte(claudeScript), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	if err := os.WriteFile(codexPath, []byte(codexScript), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))
	t.Setenv("DAL_PLAYER", "claude")
	t.Setenv("DAL_ROLE", "member")

	oldCircuit := providerCircuit
	providerCircuit = NewCircuitBreaker(1, time.Minute)
	defer func() {
		providerCircuit = oldCircuit
	}()

	out, err := executeTask("test")
	if err != nil {
		t.Fatalf("executeTask should fallback to codex on usage limit: %v", err)
	}
	if out != "codex fallback ok" {
		t.Fatalf("fallback output = %q", out)
	}
}
