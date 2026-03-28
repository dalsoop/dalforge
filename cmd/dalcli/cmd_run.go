package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dalsoop/dalcenter/internal/bridge"
	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/spf13/cobra"
)

// agentConfig holds MM connection info fetched from dalcenter daemon.
type agentConfig struct {
	DalName     string `json:"dal_name"`
	BotToken    string `json:"bot_token"`
	ChannelID   string `json:"channel_id"`
	MMURL       string `json:"mm_url"`
	TeamMembers string `json:"team_members"`
}

func runCmd(dalName string) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Start agent loop — poll Mattermost, execute tasks via Claude, report back",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentLoop(dalName)
		},
	}
}

func runAgentLoop(dalName string) error {
	log.Printf("[agent] starting agent loop for %s", dalName)

	cfg, err := fetchAgentConfig(dalName)
	mmAvailable := err == nil && cfg != nil && cfg.BotToken != "" && cfg.MMURL != "" && cfg.ChannelID != ""

	// Fallback: 환경변수에서 MM 정보 직접 읽기 (호스트 모드 — Docker 없이 실행 시)
	if !mmAvailable {
		if envURL := os.Getenv("DAL_MM_URL"); envURL != "" {
			if envToken := os.Getenv("DAL_BOT_TOKEN"); envToken != "" {
				if envCh := os.Getenv("DAL_CHANNEL_ID"); envCh != "" {
					cfg = &agentConfig{
						DalName:     dalName,
						MMURL:       envURL,
						BotToken:    envToken,
						ChannelID:   envCh,
						TeamMembers: os.Getenv("DAL_TEAM_MEMBERS"),
					}
					mmAvailable = true
					log.Printf("[agent] using env-based MM config (host mode)")
				}
			}
		}
	}

	// Auto-task-only mode: MM 없어도 auto_task만 돌릴 수 있음 (scribe 등 백그라운드 dal)
	autoTask := os.Getenv("DAL_AUTO_TASK")
	if !mmAvailable && autoTask != "" {
		log.Printf("[agent] MM not available — entering auto-task-only mode")
		return runAutoTaskOnly(dalName, autoTask)
	}

	if !mmAvailable {
		if err != nil {
			return fmt.Errorf("fetch agent config: %w", err)
		}
		return fmt.Errorf("incomplete agent config: mm_url=%q bot_token_set=%v channel_id=%q",
			cfg.MMURL, cfg.BotToken != "", cfg.ChannelID)
	}
	log.Printf("[agent] connected: mm=%s channel=%s", cfg.MMURL, cfg.ChannelID[:8])

	// Inject team members into env so leader mentions work correctly
	if cfg.TeamMembers != "" {
		os.Setenv("DAL_TEAM_MEMBERS", cfg.TeamMembers)
	}

	// Periodic team member refresh (leader needs updated member list)
	go func() {
		refreshTicker := time.NewTicker(30 * time.Second)
		defer refreshTicker.Stop()
		for range refreshTicker.C {
			if updated, err := fetchAgentConfig(dalName); err == nil && updated.TeamMembers != "" {
				os.Setenv("DAL_TEAM_MEMBERS", updated.TeamMembers)
			}
		}
	}()

	mm := bridge.NewMattermostBridge(cfg.MMURL, cfg.BotToken, cfg.ChannelID, 5*time.Second)
	if os.Getenv("DAL_NO_DM") == "1" {
		mm.NoDM = true
	}
	if err := mm.Connect(); err != nil {
		return fmt.Errorf("mattermost connect: %w", err)
	}
	defer mm.Close()

	log.Printf("[agent] listening...")

	uuidShort := os.Getenv("DAL_UUID_SHORT")
	var mention string
	if uuidShort != "" {
		mention = fmt.Sprintf("@dal-%s-%s", dalName, uuidShort)
	} else {
		mention = fmt.Sprintf("@dal-%s", dalName)
	}
	// Also respond to @{dalName} directly (e.g. @dalroot without dal- prefix)
	altMention := fmt.Sprintf("@%s", dalName)
	var activeThreads sync.Map

	// Periodic auto-task support: DAL_AUTO_TASK + DAL_AUTO_INTERVAL
	autoTask = os.Getenv("DAL_AUTO_TASK")
	autoInterval := parseInterval(os.Getenv("DAL_AUTO_INTERVAL"), 0)
	var autoTicker *time.Ticker
	var autoC <-chan time.Time
	if autoTask != "" && autoInterval > 0 {
		autoTicker = time.NewTicker(autoInterval)
		autoC = autoTicker.C
		log.Printf("[agent] auto-task enabled: interval=%s", autoInterval)
		defer autoTicker.Stop()
	}

	msgC := mm.Listen()
	var autoTaskConsecutiveFails int

	for {
		var msg bridge.Message
		var isAuto bool

		select {
		case m, ok := <-msgC:
			if !ok {
				return nil
			}
			msg = m
		case <-autoC:
			isAuto = true
		}

		if isAuto {
			if shouldSkipIfNoChange() {
				log.Printf("[agent] auto-task skipped: no git changes")
				continue
			}
			log.Printf("[agent] auto-task triggered")
			output, err := executeTask(autoTask)
			if err != nil {
				log.Printf("[agent] auto-task failed: %v", err)
				mm.Send(bridge.Message{Content: fmt.Sprintf("⚠️ 자동 검증 실패: %v\n```\n%s\n```", err, truncate(output, 500))})
				autoTaskConsecutiveFails = escalateAutoTaskFailure(autoTaskConsecutiveFails, dalName, autoTask, fmt.Sprintf("%v: %s", err, truncate(output, 500)))
				continue
			}
			autoTaskConsecutiveFails = 0
			log.Printf("[agent] auto-task done (%d bytes)", len(output))

			// If output contains FAIL or error indicators → create GitHub issue
			if containsFailure(output) {
				issueURL := createGitHubIssue(dalName, output)
				result := truncate(strings.TrimSpace(output), 2000)
				if issueURL != "" {
					result += fmt.Sprintf("\n\n🐛 GitHub issue 생성: %s", issueURL)
				}
				mm.Send(bridge.Message{Content: result})
			} else {
				log.Printf("[agent] auto-task: all passed")
			}
			continue
		}

		// --- MM message handling (existing logic) ---
		if msg.From == mm.BotUserID {
			log.Printf("[agent] skipped own message: %s", truncate(msg.Content, 40))
			continue
		}

		isDirectMention := strings.Contains(msg.Content, mention) || strings.Contains(msg.Content, altMention)
		isThreadReply := msg.RootID != "" && isActiveThread(&activeThreads, msg.RootID)
		isDM := msg.Channel != "" && msg.Channel != cfg.ChannelID // DM = different channel than main

		log.Printf("[agent] msg from=%s mention=%v(m=%q alt=%q) thread=%v dm=%v content=%s",
			msg.From[:8], isDirectMention, mention, altMention, isThreadReply, isDM, truncate(msg.Content, 60))

		if !isDirectMention && !isThreadReply && !isDM {
			continue
		}

		// Track thread
		threadID := msg.RootID
		if threadID == "" {
			threadID = msg.ID
		}
		activeThreads.Store(threadID, true)

		// Extract task — either "작업 지시:" format or free-form mention
		task := extractTask(msg.Content, "작업 지시:")
		if task == "" && isDirectMention {
			// Free-form: strip mention, use entire message
			task = strings.TrimSpace(strings.ReplaceAll(msg.Content, mention, ""))
			task = strings.TrimSpace(strings.ReplaceAll(task, altMention, ""))
		}
		if task == "" && isThreadReply {
			task = msg.Content
		}
		if task == "" && isDM {
			task = msg.Content
		}
		if task == "" {
			continue
		}

		log.Printf("[agent] message: %s", truncate(task, 80))

		externalURL := os.Getenv("DALCENTER_EXTERNAL_URL")
		var statusMsg string
		if externalURL != "" {
			logsURL := fmt.Sprintf("%s/api/logs/%s", externalURL, dalName)
			statusMsg = fmt.Sprintf("💬 작업 중... ([로그](%s))", logsURL)
		} else {
			statusMsg = "💬 작업 중..."
		}
		mm.Send(bridge.Message{
			Content: statusMsg,
			Channel: msg.Channel,
			ReplyTo: threadID,
		})

		// Build context for thread replies
		prompt := task
		if isThreadReply && !isDirectMention {
			prompt = buildThreadContext(mm, msg, dalName)
		}

		output, err := executeTask(prompt)
		if err != nil {
			log.Printf("[agent] failed: %v", err)

			// Self-repair: try to fix and retry once
			if shouldRetry, fix := selfRepair(prompt, output, err); shouldRetry {
				log.Printf("[agent] self-repair applied: %s, retrying", fix)
				mm.Send(bridge.Message{
					Content: fmt.Sprintf("🔧 자가 수리: %s — 재시도 중...", fix),
					Channel: msg.Channel,
					ReplyTo: threadID,
				})
				output, err = executeTask(prompt)
			}

			if err != nil {
				class := classifyTaskError(output)
				mm.Send(bridge.Message{
					Content: fmt.Sprintf("❌ 실패 (%s): %v\n```\n%s\n```", class, err, truncate(output, 500)),
					Channel: msg.Channel,
					ReplyTo: threadID,
				})
				appendHistoryBuffer(dalName, prompt, err.Error(), "실패")
				escalateToHost(dalName, prompt, output, string(class))
				daemon.DispatchTaskFailed(dalName, truncate(prompt, 200), err.Error(), len(output))
				// Auto-claim for environment/blocked issues
				if class == ErrClassEnv || class == ErrClassDeps {
					autoFileClaim(dalName, class, prompt, output)
				}
				continue
			}
		}

		log.Printf("[agent] done (%d bytes)", len(output))

		// Check if files were modified → auto git workflow (member only — leader는 라우터, 직접 커밋 안 함)
		var gitResult string
		if os.Getenv("DAL_ROLE") != "leader" {
			gitResult = autoGitWorkflow(dalName)
		}

		// Extract git changes and PR URL for webhook
		var gitChanges []string
		var prURL string
		if gitResult != "" {
			for _, line := range strings.Split(gitResult, "\n") {
				if strings.HasPrefix(line, "M ") || strings.HasPrefix(line, "?? ") || strings.HasPrefix(line, "A ") {
					gitChanges = append(gitChanges, strings.TrimSpace(line))
				}
				if strings.Contains(line, "github.com") && strings.Contains(line, "/pull/") {
					prURL = strings.TrimSpace(line)
				}
			}
		}

		// History buffer: record completed task
		appendHistoryBuffer(dalName, prompt, truncate(output, 200), "완료")

		// Webhook: task complete
		daemon.DispatchTaskComplete(dalName, truncate(prompt, 200), len(output), gitChanges, prURL)

		// Format response
		response := truncate(strings.TrimSpace(output), 3000)
		if gitResult != "" {
			response += "\n\n" + gitResult
		}

		mm.Send(bridge.Message{
			Content: response,
			Channel: msg.Channel,
			ReplyTo: threadID,
		})

		// Report to leader: when a member dal receives a direct task from user (not from leader),
		// notify the leader so they stay in the loop
		role := os.Getenv("DAL_ROLE")
		if role == "member" && isDirectMention && !isFromLeader(msg.From, mm) {
			reportToLeader(mm, dalName, task, response, threadID)
		}
	}
}

// isDalOnlyChanges returns true if every changed file in git porcelain output
// is under the .dal/ directory. These are internal metadata changes that don't
// need a PR.
func isDalOnlyChanges(porcelainOutput string) bool {
	lines := strings.Split(porcelainOutput, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// git porcelain format: "XY filename" or "XY filename -> renamed"
		// strip the two-char status prefix + space
		file := line
		if len(file) > 3 {
			file = file[3:]
		}
		// handle renames: "old -> new"
		if idx := strings.Index(file, " -> "); idx >= 0 {
			file = file[idx+4:]
		}
		if !strings.HasPrefix(file, ".dal/") {
			return false
		}
	}
	return true
}

// isOnlyArtifacts returns true if all changes are runtime artifacts (not real code changes).
func isOnlyArtifacts(porcelainOutput string) bool {
	artifacts := []string{
		"wisdom.md",           // root-level copy (not .dal/wisdom.md)
		"decisions.md",        // root-level copy
		".dal/data/",          // runtime data
		"now.md",              // state mount leak
		"decisions/inbox/",    // state mount leak
		"history-buffer/",     // state mount leak
		"wisdom-inbox/",       // state mount leak
	}
	lines := strings.Split(porcelainOutput, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		file := line
		if len(file) > 3 {
			file = file[3:]
		}
		isArtifact := false
		for _, a := range artifacts {
			if file == a || strings.HasPrefix(file, a) {
				isArtifact = true
				break
			}
		}
		if !isArtifact {
			return false
		}
	}
	return len(lines) > 0
}

// autoGitWorkflow checks for file changes and creates a branch + commit + PR.
// Gate 1: .dal/ only → skip
// Gate 2: artifacts only → skip
// Gate 3: no real code files (*.go, *.rs, *.md in .dal/) → skip
// Gate 4: PR 생성 전 diff 재확인 — 빈 PR 방지
func autoGitWorkflow(dalName string) string {
	run := func(args ...string) (string, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = "/workspace"
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	// 먼저 main으로 복귀 (이전 실행에서 다른 브랜치에 남아있을 수 있음)
	run("git", "checkout", "main")

	// Check changes
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = "/workspace"
	statusOut, err := statusCmd.Output()
	if err != nil || len(strings.TrimSpace(string(statusOut))) == 0 {
		return ""
	}

	changes := strings.TrimSpace(string(statusOut))
	log.Printf("[git] changes detected:\n%s", changes)

	// Gate 1: .dal/ only
	if isDalOnlyChanges(changes) {
		log.Printf("[git] gate1: .dal/ only — skip")
		return ""
	}

	// Gate 2: artifacts only
	if isOnlyArtifacts(changes) {
		log.Printf("[git] gate2: artifacts only — skip")
		return ""
	}

	// Gate 3: 실제 코드 파일이 있는지 확인
	hasCodeChange := false
	for _, line := range strings.Split(changes, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		file := line
		if len(file) > 3 {
			file = file[3:]
		}
		// 코드 파일: .go, .rs, .py, .ts, .js, .cue 또는 docs/
		if strings.HasSuffix(file, ".go") || strings.HasSuffix(file, ".rs") ||
			strings.HasSuffix(file, ".py") || strings.HasSuffix(file, ".ts") ||
			strings.HasSuffix(file, ".js") || strings.HasSuffix(file, ".cue") ||
			strings.HasPrefix(file, "docs/") {
			hasCodeChange = true
			break
		}
	}
	if !hasCodeChange {
		log.Printf("[git] gate3: no code files changed — skip")
		// 부산물 정리 (git checkout으로 되돌리기)
		run("git", "checkout", "--", ".")
		return ""
	}

	// Create branch
	branch := fmt.Sprintf("dal/%s/%d", dalName, time.Now().Unix())
	if _, err := run("git", "checkout", "-b", branch); err != nil {
		return fmt.Sprintf("⚠️ 브랜치 생성 실패: %v", err)
	}

	// Stage only code files (not artifacts)
	// git add specific patterns instead of -A
	codePatterns := []string{"*.go", "*.rs", "*.py", "*.ts", "*.js", "*.cue", "docs/"}
	for _, pattern := range codePatterns {
		run("git", "add", pattern)
	}
	// .dal/ charter/skill 변경도 포함 (코드 변경과 함께인 경우)
	run("git", "add", ".dal/")

	// Gate 4: staged 파일 확인 — 빈 커밋 방지
	diffOut, _ := run("git", "diff", "--cached", "--stat")
	if strings.TrimSpace(diffOut) == "" {
		log.Printf("[git] gate4: nothing staged — skip")
		run("git", "checkout", "main")
		run("git", "branch", "-D", branch)
		return ""
	}

	log.Printf("[git] staged:\n%s", diffOut)

	// Commit
	commitMsg := fmt.Sprintf("feat: %s dal 자동 반영\n\n%s\n\nCo-Authored-By: dal-%s <dal-%s@dalcenter.local>",
		dalName, diffOut, dalName, dalName)
	if _, err := run("git", "commit", "-m", commitMsg); err != nil {
		run("git", "checkout", "main")
		return fmt.Sprintf("⚠️ 커밋 실패: %v", err)
	}

	// Push
	if _, err := run("git", "push", "-u", "origin", branch); err != nil {
		run("git", "checkout", "main")
		return fmt.Sprintf("⚠️ 푸시 실패: %v", err)
	}

	// Create PR
	prTitle := fmt.Sprintf("feat(%s): %s", dalName, branch)
	prBody := fmt.Sprintf("dal-%s 작업 결과.\n\n```\n%s\n```", dalName, diffOut)
	prOut, err := run("gh", "pr", "create", "--title", prTitle, "--body", prBody)
	if err != nil {
		run("git", "checkout", "main")
		return fmt.Sprintf("✅ 커밋+푸시 완료 (`%s`)\n⚠️ PR 생성 실패: %v", branch, err)
	}

	run("git", "checkout", "main")
	return fmt.Sprintf("🔀 PR 생성 완료\n브랜치: `%s`\n%s", branch, prOut)
}

func isActiveThread(threads *sync.Map, threadID string) bool {
	_, ok := threads.Load(threadID)
	return ok
}

func buildThreadContext(mm *bridge.MattermostBridge, newMsg bridge.Message, dalName string) string {
	var sb strings.Builder
	sb.WriteString("너는 Mattermost 스레드에서 대화 중이다. ")
	sb.WriteString("아래는 스레드 전체 대화 내역이다. 마지막 메시지에 대해 응답하라.\n\n")

	// Fetch full thread from MM API
	threadID := newMsg.RootID
	if threadID == "" {
		threadID = newMsg.ID
	}

	dcURL := os.Getenv("DALCENTER_URL")
	agentCfg, _ := fetchAgentConfig(dalName)
	if agentCfg != nil && agentCfg.MMURL != "" && agentCfg.BotToken != "" {
		url := fmt.Sprintf("%s/api/v4/posts/%s/thread", agentCfg.MMURL, threadID)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+agentCfg.BotToken)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			var thread struct {
				Order []string                   `json:"order"`
				Posts map[string]json.RawMessage `json:"posts"`
			}
			if json.Unmarshal(body, &thread) == nil {
				for _, pid := range thread.Order {
					var post struct {
						UserID  string `json:"user_id"`
						Message string `json:"message"`
					}
					if json.Unmarshal(thread.Posts[pid], &post) == nil {
						role := "상대방"
						if post.UserID == mm.BotUserID {
							role = "나(" + dalName + ")"
						}
						sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", role, post.Message))
					}
				}
				sb.WriteString(fmt.Sprintf("---\n너의 이름: %s. 위 대화 맥락을 보고 마지막 메시지에 응답하라. 간결하게.", dalName))
				return sb.String()
			}
		}
	}

	// Fallback: just use the new message
	_ = dcURL
	sb.WriteString(fmt.Sprintf("[상대방]: %s\n\n", newMsg.Content))
	sb.WriteString(fmt.Sprintf("너의 이름: %s. 간결하게 응답.", dalName))
	return sb.String()
}

// notifyCredentialRefresh tells dalcenter daemon that credentials need refresh.
// The daemon can log this or trigger external sync.
func notifyCredentialRefresh(dalName string) {
	dcURL := os.Getenv("DALCENTER_URL")
	if dcURL == "" {
		return
	}
	body := fmt.Sprintf(`{"dal":"%s","message":"[%s] ⚠️ credential 만료. 호스트에서 sync-dal-creds.sh 실행 필요."}`, dalName, dalName)
	req, _ := http.NewRequest("POST", dcURL+"/api/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	log.Printf("[agent] credential refresh requested for %s", dalName)
}

// fetchCentralProvider checks the dalcenter daemon's centralized provider circuit.
// Returns the active provider (e.g. "codex") or empty string if unavailable.
func fetchCentralProvider() string {
	dcURL := os.Getenv("DALCENTER_URL")
	if dcURL == "" {
		return ""
	}
	resp, err := http.Get(dcURL + "/api/provider-status")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	var result struct {
		ActiveProvider string `json:"active_provider"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.ActiveProvider
}

// reportCentralTrip reports a rate limit to the dalcenter daemon's centralized circuit.
func reportCentralTrip(dalName, reason string) {
	dcURL := os.Getenv("DALCENTER_URL")
	if dcURL == "" {
		return
	}
	body := fmt.Sprintf(`{"dal_name":%q,"reason":%q}`, dalName, reason)
	resp, err := http.Post(dcURL+"/api/provider-trip", "application/json", strings.NewReader(body))
	if err != nil {
		log.Printf("[central-circuit] trip report failed: %v", err)
		return
	}
	resp.Body.Close()
	log.Printf("[central-circuit] reported trip to daemon (by %s)", dalName)
}

func fetchAgentConfig(dalName string) (*agentConfig, error) {
	dcURL := os.Getenv("DALCENTER_URL")
	if dcURL == "" {
		return nil, fmt.Errorf("DALCENTER_URL not set")
	}
	resp, err := http.Get(dcURL + "/api/agent-config/" + dalName)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	var cfg agentConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

const maxRetries = 3

// circuit breaker: 3 failures → fallback, 2min cooldown
var providerCircuit = NewCircuitBreaker(3, 2*time.Minute)

func executeTask(task string) (string, error) {
	player := os.Getenv("DAL_PLAYER")
	fallbackPlayer := detectFallback(player)

	// Check centralized provider circuit first (dalcenter daemon level)
	if centralPlayer := fetchCentralProvider(); centralPlayer != "" && centralPlayer != player {
		log.Printf("[central-circuit] using %s (central override)", centralPlayer)
		out, err := runProvider(centralPlayer, task)
		if err == nil {
			return out, nil
		}
		log.Printf("[central-circuit] %s failed: %v, falling through to local circuit", centralPlayer, err)
	}

	// Local circuit breaker fallback
	if providerCircuit.ShouldFallback() && fallbackPlayer != "" {
		log.Printf("[circuit] primary %s is open, trying fallback %s", player, fallbackPlayer)
		out, err := runProvider(fallbackPlayer, task)
		if err == nil {
			return out, nil
		}
		log.Printf("[circuit] fallback %s also failed: %v", fallbackPlayer, err)
	}

	var lastOut string
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		out, err := runProvider(player, task)
		if err == nil {
			providerCircuit.RecordSuccess()
			return out, nil
		}

		lastOut = out
		lastErr = err

		// Auth error → credential 문제이므로 circuit breaker 대상 아님
		if isAuthError(out) {
			wait := 60 * time.Second
			name := os.Getenv("DAL_NAME")
			log.Printf("[agent] auth error (attempt %d/%d), waiting %s for credential sync...", attempt, maxRetries, wait)
			notifyCredentialRefresh(name)
			time.Sleep(wait)
			continue
		}

		// 모든 provider 에러 → circuit breaker에 기록
		providerCircuit.RecordFailure()

		// Rate limit → 중앙 서킷브레이커에 보고
		if isRetryable(out) && providerCircuit.ShouldFallback() {
			reportCentralTrip(os.Getenv("DAL_NAME"), truncate(out, 200))
		}

		// circuit open → fallback 시도
		if providerCircuit.ShouldFallback() && fallbackPlayer != "" {
			log.Printf("[circuit] switching to fallback %s (attempt %d/%d)", fallbackPlayer, attempt, maxRetries)
			fbOut, fbErr := runProvider(fallbackPlayer, task)
			if fbErr == nil {
				return fbOut, nil
			}
			log.Printf("[circuit] fallback %s failed: %v", fallbackPlayer, fbErr)
		}

		// retryable → 대기 후 재시도
		if isRetryable(out) {
			wait := time.Duration(attempt*30) * time.Second
			log.Printf("[agent] retrying primary in %s (attempt %d/%d)", wait, attempt, maxRetries)
			time.Sleep(wait)
			continue
		}

		// non-retryable → 즉시 종료 (circuit에는 이미 기록됨)
		return out, err
	}

	return lastOut, fmt.Errorf("max retries (%d) exceeded, circuit=%s: %w", maxRetries, providerCircuit.State(), lastErr)
}

// detectFallback returns the fallback player for the given primary.
// Priority: DAL_FALLBACK_PLAYER env (from dal.cue) → auto-detect by availability.
func detectFallback(primary string) string {
	if fp := os.Getenv("DAL_FALLBACK_PLAYER"); fp != "" {
		if fp != primary {
			return fp
		}
		// fallback_player == primary makes no sense; fall through to auto-detect
	}
	switch primary {
	case "claude":
		if _, err := exec.LookPath("codex"); err == nil {
			return "codex"
		}
	case "codex":
		if _, err := exec.LookPath("claude"); err == nil {
			return "claude"
		}
	}
	return ""
}

// runProvider executes a task with the specified provider.
func runProvider(player, task string) (string, error) {
	return runClaude(player, task)
}

func runClaude(player, task string) (string, error) {
	role := os.Getenv("DAL_ROLE")

	var cmd *exec.Cmd
	switch player {
	case "codex":
		workDir, _ := os.Getwd()
		cmd = exec.Command("codex", "exec",
			"--dangerously-bypass-approvals-and-sandbox",
			"-C", workDir,
			task)
	default: // claude
		// Build allowed tools based on role and extra permissions
		var allowedTools string
		if role == "leader" {
			// Leader = 라우터 + 중개자. Bash 전체 허용 (gh, dalcli-leader 등 필요), Write/Edit만 금지.
			allowedTools = "Bash Read Glob Grep"
		} else if extra := os.Getenv("DAL_EXTRA_BASH"); extra == "*" {
			// Unrestricted bash (e.g., verifier running go test)
			allowedTools = "Bash Read Write Glob Grep Edit"
		} else {
			bashPerms := "git:*,gh:*"
			if extra != "" {
				bashPerms += "," + extra + ":*"
			}
			allowedTools = fmt.Sprintf("Bash(%s) Read Write Glob Grep Edit", bashPerms)
		}
		claudeArgs := []string{
			"--allowedTools", allowedTools,
		}
		if model := os.Getenv("DAL_MODEL"); model != "" {
			claudeArgs = append(claudeArgs, "--model", model)
		}
		claudeArgs = append(claudeArgs, "--print", task)
		cmd = exec.Command("claude", claudeArgs...)
	}

	// Task timeout: max 5 minutes per execution
	maxDuration := 5 * time.Minute
	if env := os.Getenv("DAL_MAX_DURATION"); env != "" {
		if d, err := time.ParseDuration(env); err == nil {
			maxDuration = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), maxDuration)
	defer cancel()

	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmd.Dir = "/workspace"
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_ENTRYPOINT=dalcli")

	// Capture stdout only, discard stderr (prevents codex warnings in output)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), fmt.Errorf("TIMEOUT: task exceeded %s", maxDuration)
	}

	if stderr.Len() > 0 {
		log.Printf("[agent] stderr: %s", truncate(stderr.String(), 200))
	}

	return stdout.String(), err
}

// isRetryable checks if the error output indicates a rate limit or overload.
func isRetryable(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "overloaded") ||
		strings.Contains(lower, "429") ||
		strings.Contains(lower, "529") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "capacity") ||
		strings.Contains(lower, "hit your limit") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "limit exceeded") ||
		strings.Contains(lower, "quota exceeded")
}

// isAuthError checks if the error is an authentication failure (401, expired token, etc).
func isAuthError(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "401") ||
		strings.Contains(lower, "authentication_error") ||
		strings.Contains(lower, "invalid authentication") ||
		strings.Contains(lower, "oauth token has expired") ||
		strings.Contains(lower, "failed to authenticate")
}

func extractTask(content, prefix string) string {
	idx := strings.Index(content, prefix)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(content[idx+len(prefix):])
}

func formatReport(output string) string {
	if len(output) > 3000 {
		output = output[:1500] + "\n\n... (truncated) ...\n\n" + output[len(output)-1500:]
	}
	return fmt.Sprintf("✅ 작업 완료\n```\n%s\n```", output)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// parseInterval parses a duration string, returning fallback on error.
func parseInterval(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

// runAutoTaskOnly runs only the auto-task loop without Mattermost.
// Used by background dals like scribe that don't need MM.
func runAutoTaskOnly(dalName, autoTask string) error {
	interval := parseInterval(os.Getenv("DAL_AUTO_INTERVAL"), 30*time.Minute)
	log.Printf("[agent] auto-task-only mode: interval=%s", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var consecutiveFails int

	// Run once immediately
	if shouldSkipIfNoChange() {
		log.Printf("[agent] auto-task initial run skipped: no git changes")
	} else {
		log.Printf("[agent] auto-task initial run")
		output, err := executeTask(autoTask)
		if err != nil {
			log.Printf("[agent] auto-task failed: %v", err)
			consecutiveFails = escalateAutoTaskFailure(consecutiveFails, dalName, autoTask, fmt.Sprintf("%v: %s", err, truncate(output, 500)))
		} else {
			consecutiveFails = 0
			log.Printf("[agent] auto-task done (%d bytes)", len(output))
		}
	}

	for range ticker.C {
		if shouldSkipIfNoChange() {
			log.Printf("[agent] auto-task skipped: no git changes")
			continue
		}
		log.Printf("[agent] auto-task triggered")
		output, err := executeTask(autoTask)
		if err != nil {
			log.Printf("[agent] auto-task failed: %v", err)
			consecutiveFails = escalateAutoTaskFailure(consecutiveFails, dalName, autoTask, fmt.Sprintf("%v: %s", err, truncate(output, 500)))
			continue
		}
		consecutiveFails = 0
		log.Printf("[agent] auto-task done (%d bytes)", len(output))
	}
	return nil
}

// containsFailure checks if output indicates test/build failures.
func containsFailure(output string) bool {
	lower := strings.ToLower(output)
	indicators := []string{"fail", "error", "panic", "fatal"}
	for _, ind := range indicators {
		if strings.Contains(lower, ind) {
			return true
		}
	}
	return false
}

// createGitHubIssue creates a GitHub issue for auto-detected problems.
func createGitHubIssue(dalName, output string) string {
	title := fmt.Sprintf("[auto] %s: 자동 검증 실패 감지", dalName)
	body := fmt.Sprintf("## 자동 검증 결과\n\n`%s` dal이 주기적 검증에서 문제를 발견했습니다.\n\n```\n%s\n```\n\n---\n🤖 자동 생성 by dal-%s", dalName, truncate(output, 3000), dalName)

	cmd := exec.Command("gh", "issue", "create", "--title", title, "--body", body)
	cmd.Dir = "/workspace"
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[agent] gh issue create failed: %v: %s", err, string(out))
		return ""
	}
	url := strings.TrimSpace(string(out))
	log.Printf("[agent] created issue: %s", url)
	return url
}

// hasGitChanges checks if there are uncommitted changes via git diff --stat HEAD.
func hasGitChanges() bool {
	cmd := exec.Command("git", "diff", "--stat", "HEAD")
	cmd.Dir = "/workspace"
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[agent] git diff --stat failed: %v", err)
		return true // assume changes on error to avoid skipping
	}
	return strings.TrimSpace(string(out)) != ""
}

// shouldSkipIfNoChange returns true when skip-if-no-change is enabled and there are no git changes.
func shouldSkipIfNoChange() bool {
	return strings.EqualFold(os.Getenv("DAL_AUTO_SKIP_IF_NO_CHANGE"), "true") && !hasGitChanges()
}

// escalateAutoTaskFailure increments fail count and escalates after 2 consecutive failures.
// Returns the updated consecutive fail count.
func escalateAutoTaskFailure(consecutiveFails int, dalName, autoTask, errMsg string) int {
	consecutiveFails++
	if consecutiveFails >= 2 {
		log.Printf("[agent] auto-task failed %d times consecutively, escalating", consecutiveFails)
		client, err := daemon.NewClient()
		if err != nil {
			log.Printf("[agent] escalate: cannot create client: %v", err)
			return consecutiveFails
		}
		if err := client.Escalate(dalName, autoTask, "consecutive_auto_task_failure", errMsg); err != nil {
			log.Printf("[agent] escalate failed: %v", err)
		} else {
			log.Printf("[agent] escalated to leader")
		}
	}
	return consecutiveFails
}

// isFromLeader checks if a message sender is a leader bot (by checking dal- prefix + leader keyword)
func isFromLeader(senderID string, mm *bridge.MattermostBridge) bool {
	username := mm.GetUsername(senderID)
	return strings.Contains(username, "leader")
}

// reportToLeader sends a summary to the leader bot in the same channel
func reportToLeader(mm *bridge.MattermostBridge, dalName, task, result, threadID string) {
	// Find leader mention from team_members env
	teamMembers := os.Getenv("DAL_TEAM_MEMBERS")
	var leaderMention string
	for _, entry := range strings.Split(teamMembers, ",") {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 && strings.Contains(parts[0], "leader") {
			leaderMention = "@" + parts[1]
			break
		}
	}
	if leaderMention == "" {
		return // no leader found in team
	}

	report := fmt.Sprintf("%s 📋 보고: **%s**가 사용자 직접 지시를 수행했습니다.\n\n**태스크:** %s\n**결과:** %s",
		leaderMention, dalName, truncate(task, 200), truncate(result, 500))
	mm.Send(bridge.Message{Content: report})
}
