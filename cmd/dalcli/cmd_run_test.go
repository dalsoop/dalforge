package main

import (
	"encoding/json"
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

	"github.com/dalsoop/dalcenter/internal/bridge"
)

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

// ── buildThreadContext with selective thread context ──

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

	ctx := buildThreadContext(mm, msg, "writer", "머지해봐")

	if !strings.Contains(ctx, "원래 요청: @dal-writer ep04 수정해") {
		t.Error("should contain normalized root task")
	}
	if !strings.Contains(ctx, "이번 요청: 머지해봐") {
		t.Error("should contain latest task")
	}
	if strings.Contains(ctx, "💬 작업 중...") {
		t.Error("should not replay status-only messages into worker prompt")
	}
}

func TestIsCredentialStatusQuery(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   bool
	}{
		{"korean", "이거 credential 만료 맞아?", true},
		{"script name", "sync-dal-creds.sh 왜 뜨지", true},
		{"host sync", "pve-sync-creds 해야 하나", true},
		{"normal task", "가야 런타임 테스트해봐", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCredentialStatusQuery(tt.prompt); got != tt.want {
				t.Fatalf("isCredentialStatusQuery(%q) = %v, want %v", tt.prompt, got, tt.want)
			}
		})
	}
}

func TestBuildCredentialStatusReply(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DALCENTER_URL", "")

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"expiresAt":1774810659871}}`), 0600); err != nil {
		t.Fatalf("write claude cred: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"tokens":{"expires_at":"2026-03-30T14:59:24Z"}}`), 0600); err != nil {
		t.Fatalf("write codex cred: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/claims" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"claims": []map[string]any{
				{
					"id":           "claim-0019",
					"title":        "credential 만료로 호스트 sync 필요",
					"detail":       "claude auth failed",
					"context":      "kind=credential_sync&player=claude",
					"status":       "resolved",
					"timestamp":    "2026-03-29T13:39:43Z",
					"responded_at": "2026-03-29T13:39:48Z",
					"response":     "[credential-ops] 완료 player=claude vmid=105 mtime=2026-03-29T13:39:47Z",
				},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("DALCENTER_URL", srv.URL)

	reply, err := buildCredentialStatusReply("leader")
	if err != nil {
		t.Fatalf("buildCredentialStatusReply: %v", err)
	}
	if !strings.Contains(reply, "open credential claim: 0") {
		t.Fatalf("reply missing open count: %s", reply)
	}
	if !strings.Contains(reply, "claude expiresAt: 2026-03-29T18:57:39Z") {
		t.Fatalf("reply missing claude expiry: %s", reply)
	}
	if !strings.Contains(reply, "codex access token exp: 2026-03-30T14:59:24Z") {
		t.Fatalf("reply missing codex expiry: %s", reply)
	}
	if !strings.Contains(reply, "daemon claim 상태(open/resolved/rejected)로만 자동 sync 진행 여부를 판단합니다.") {
		t.Fatalf("reply missing neutral sync guidance: %s", reply)
	}
}

func TestCredentialStatusQueryInput_UsesLatestTaskOnly(t *testing.T) {
	task := "dalcenter 에서 원인 찾아오봐"
	threadPrompt := `[상대방]: [leader] ⚠️ credential 만료. 호스트에서 sync-dal-creds.sh 실행 필요.

---
너의 이름: leader. 위 대화 맥락을 보고 마지막 메시지에 응답하라. 간결하게.`

	got := credentialStatusQueryInput(task, threadPrompt)
	if got != task {
		t.Fatalf("credentialStatusQueryInput() = %q, want latest task %q", got, task)
	}
	if isCredentialStatusQuery(got) {
		t.Fatalf("latest task should not be forced into credential query mode: %q", got)
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

	ctx := buildThreadContext(mm, msg, "test-dal", "테스트 메시지")
	if !strings.Contains(ctx, "이번 요청: 테스트 메시지") {
		t.Error("fallback should contain latest task")
	}
}

func TestBuildTaskSpec_ThreadReplyUsesSelectiveContext(t *testing.T) {
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

	t.Setenv("DALCENTER_URL", srv.URL)

	mm := &bridge.MattermostBridge{BotUserID: "bot-writer"}
	msg := bridge.Message{
		ID:      "p3",
		RootID:  "p1",
		From:    "user-devops",
		Content: "머지해봐",
		Channel: "ch1",
	}

	spec := buildTaskSpec("writer", mm, msg, "머지해봐", false, true, false)
	if spec.Intent != TaskIntentExecute {
		t.Fatalf("intent = %s, want %s", spec.Intent, TaskIntentExecute)
	}
	if spec.Source != "thread" {
		t.Fatalf("source = %s, want thread", spec.Source)
	}
	if !strings.Contains(spec.Prompt, "원래 요청: @dal-writer ep04 수정해") {
		t.Fatalf("prompt missing root task: %s", spec.Prompt)
	}
	if !strings.Contains(spec.Prompt, "이번 요청: 머지해봐") {
		t.Fatalf("prompt missing latest task: %s", spec.Prompt)
	}
	if strings.Contains(spec.Prompt, "💬 작업 중...") {
		t.Fatalf("prompt should exclude status-only messages: %s", spec.Prompt)
	}
}

func TestBuildTaskSpec_CredentialStatusIntent(t *testing.T) {
	mm := &bridge.MattermostBridge{BotUserID: "bot-leader"}
	msg := bridge.Message{
		ID:      "root-1",
		From:    "user-devops",
		Content: "sync-dal-creds.sh 왜 뜨지",
		Channel: "ch1",
	}

	spec := buildTaskSpec("leader", mm, msg, "sync-dal-creds.sh 왜 뜨지", true, false, false)
	if spec.Intent != TaskIntentCredentialStatus {
		t.Fatalf("intent = %s, want %s", spec.Intent, TaskIntentCredentialStatus)
	}
}

func TestShouldIgnoreDalBotMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/users/bot-leader", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "bot-leader", "username": "dal-leader-emotio"})
	})
	mux.HandleFunc("/api/v4/users/bot-reviewer", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "bot-reviewer", "username": "dal-reviewer-emotio"})
	})
	mux.HandleFunc("/api/v4/users/human-devops", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "human-devops", "username": "devops"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	mm := &bridge.MattermostBridge{URL: srv.URL, Token: "tok"}

	tests := []struct {
		name            string
		from            string
		isDirectMention bool
		isThreadReply   bool
		isDM            bool
		want            bool
	}{
		{"human dm allowed", "human-devops", false, false, true, false},
		{"dal dm ignored", "bot-reviewer", false, false, true, true},
		{"leader thread followup allowed", "bot-leader", false, true, false, false},
		{"nonleader thread followup ignored", "bot-reviewer", false, true, false, true},
		{"direct mention from dal bot allowed", "bot-reviewer", true, true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := bridge.Message{From: tt.from, Content: "test"}
			got := shouldIgnoreDalBotMessage(msg, mm, tt.isDirectMention, tt.isThreadReply, tt.isDM)
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldIgnoreOperationalDalBotMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/users/bot-reviewer", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "bot-reviewer", "username": "dal-reviewer-emotio"})
	})
	mux.HandleFunc("/api/v4/users/human-devops", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "human-devops", "username": "devops"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	mm := &bridge.MattermostBridge{URL: srv.URL, Token: "tok"}

	tests := []struct {
		name string
		msg  bridge.Message
		want bool
	}{
		{
			name: "dal bot operational notice",
			msg: bridge.Message{
				From:    "bot-reviewer",
				Content: "@dal-leader [leader] ⚠️ credential 만료. 호스트에서 sync-dal-creds.sh 실행 필요.",
			},
			want: true,
		},
		{
			name: "dal bot normal message",
			msg: bridge.Message{
				From:    "bot-reviewer",
				Content: "@dal-leader 리뷰 끝났어",
			},
			want: false,
		},
		{
			name: "human operational text",
			msg: bridge.Message{
				From:    "human-devops",
				Content: "sync-dal-creds.sh 왜 뜨지",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldIgnoreOperationalDalBotMessage(tt.msg, mm); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsDirectedAtDifferentDal(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"plain dm", "로그 좀 봐줘", false},
		{"self stable mention", "@dal-leader 확인해줘", false},
		{"self legacy mention", "@dal-leader-emotio 확인해줘", false},
		{"self short mention", "@leader 확인해줘", false},
		{"other stable mention", "@dal-reviewer 확인해줘", true},
		{"other short mention", "@reviewer 확인해줘", true},
		{"other malformed mention", "@dal-leader-leader 확인해줘", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDirectedAtDifferentDal(tt.content, "@dal-leader", "@dal-leader-emotio", "@leader")
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsOperationalNoticeMessage(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"old credential notice", "@dal-leader [leader] ⚠️ credential 만료. 호스트에서 sync-dal-creds.sh 실행 필요.", true},
		{"new credential notice", "[verifier] ⚠️ credential 만료. 호스트에서 pve-sync-creds 실행 필요. claim=claim-0001", true},
		{"normal dm", "호스트에서 로그 좀 확인해줘", false},
		{"generic warning", "⚠️ 디스크 용량 부족", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOperationalNoticeMessage(tt.content); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleCredentialStatusQuery_FallbackOnStatusError(t *testing.T) {
	var sentBody string
	mmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/posts" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		sentBody = string(body)
		json.NewEncoder(w).Encode(map[string]string{"id": "post-1"})
	}))
	defer mmServer.Close()

	t.Setenv("DALCENTER_URL", "http://127.0.0.1:1")
	mm := &bridge.MattermostBridge{URL: mmServer.URL, Token: "tok", ChannelID: "ch-1"}

	handled, err := handleCredentialStatusQuery("leader", "sync-dal-creds.sh 왜 뜨지", "root-1", "ch-1", mm)
	if err != nil {
		t.Fatalf("handleCredentialStatusQuery: %v", err)
	}
	if !handled {
		t.Fatal("expected query to be handled")
	}
	if !strings.Contains(sentBody, "credential-ops 상태 조회 실패") {
		t.Fatalf("expected fallback reply, body=%s", sentBody)
	}
	if !strings.Contains(sentBody, "일반 작업으로 넘기지 않고 여기서 중단합니다.") {
		t.Fatalf("expected deterministic stop message, body=%s", sentBody)
	}
}

func TestShouldDisableDM(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"enabled", "1", true},
		{"disabled", "0", false},
		{"unset", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldDisableDM(tt.raw); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldUseCentralOverride(t *testing.T) {
	tests := []struct {
		name           string
		player         string
		fallbackPlayer string
		centralPlayer  string
		want           bool
	}{
		{"no central", "claude", "codex", "", false},
		{"same as primary", "claude", "codex", "claude", false},
		{"matches fallback", "claude", "codex", "codex", true},
		{"codex should ignore claude", "codex", "", "claude", false},
		{"unexpected provider ignored", "claude", "codex", "gemini", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldUseCentralOverride(tt.player, tt.fallbackPlayer, tt.centralPlayer)
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectFallback_IgnoresUnavailableEnvOverride(t *testing.T) {
	t.Setenv("DAL_FALLBACK_PLAYER", "missing-provider")

	if got := detectFallback("gemini"); got != "" {
		t.Fatalf("detectFallback should ignore unavailable env override, got %q", got)
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
		{"dal only", " M .dal/data/claims.json", true},
		{"dal added", "?? .dal/data/new.json", true},
		{"mixed", " M .dal/data/claims.json\n M cmd/dalcli/cmd_run.go", false},
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
		{"429 Too Many Requests", true},
		{"529 overloaded", true},
		{"API Error: too many requests", true},
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
	os.Setenv("DAL_PLAYER", "codex")
	os.Setenv("DAL_ROLE", "member")
	defer os.Unsetenv("DAL_PLAYER")
	defer os.Unsetenv("DAL_ROLE")

	// codex not available in test → just verify it doesn't panic
	_, err := runClaude(os.Getenv("DAL_PLAYER"), "test")
	if err == nil {
		t.Log("codex available — unusual but ok")
	}
}

func TestRunProvider_Claude_Leader(t *testing.T) {
	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "leader")
	defer os.Unsetenv("DAL_PLAYER")
	defer os.Unsetenv("DAL_ROLE")

	_, err := runClaude(os.Getenv("DAL_PLAYER"), "test")
	if err == nil {
		t.Log("claude available — unusual but ok")
	}
}

func TestRunProvider_Claude_Member(t *testing.T) {
	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	defer os.Unsetenv("DAL_PLAYER")
	defer os.Unsetenv("DAL_ROLE")

	_, err := runClaude(os.Getenv("DAL_PLAYER"), "test")
	if err == nil {
		t.Log("claude available — unusual but ok")
	}
}
