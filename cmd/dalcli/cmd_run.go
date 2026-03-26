package main

import (
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
	"github.com/spf13/cobra"
)

// agentConfig holds MM connection info fetched from dalcenter daemon.
type agentConfig struct {
	DalName   string `json:"dal_name"`
	BotToken  string `json:"bot_token"`
	ChannelID string `json:"channel_id"`
	MMURL     string `json:"mm_url"`
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
	if err != nil {
		return fmt.Errorf("fetch agent config: %w", err)
	}
	if cfg.BotToken == "" || cfg.MMURL == "" || cfg.ChannelID == "" {
		return fmt.Errorf("incomplete agent config: mm_url=%q bot_token_set=%v channel_id=%q",
			cfg.MMURL, cfg.BotToken != "", cfg.ChannelID)
	}
	log.Printf("[agent] connected: mm=%s channel=%s", cfg.MMURL, cfg.ChannelID[:8])

	mm := bridge.NewMattermostBridge(cfg.MMURL, cfg.BotToken, cfg.ChannelID, 5*time.Second)
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
	var activeThreads sync.Map

	// Periodic auto-task support: DAL_AUTO_TASK + DAL_AUTO_INTERVAL
	autoTask := os.Getenv("DAL_AUTO_TASK")
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
			log.Printf("[agent] auto-task triggered")
			output, err := executeTask(autoTask)
			if err != nil {
				log.Printf("[agent] auto-task failed: %v", err)
				mm.Send(bridge.Message{Content: fmt.Sprintf("⚠️ 자동 검증 실패: %v\n```\n%s\n```", err, truncate(output, 500))})
				continue
			}
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
			continue
		}

		isDirectMention := strings.Contains(msg.Content, mention)
		isThreadReply := msg.RootID != "" && isActiveThread(&activeThreads, msg.RootID)

		if !isDirectMention && !isThreadReply {
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
		}
		if task == "" && isThreadReply {
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
					ReplyTo: threadID,
				})
				output, err = executeTask(prompt)
			}

			if err != nil {
				class := classifyTaskError(output)
				// Post failure to thread
				mm.Send(bridge.Message{
					Content: fmt.Sprintf("❌ 실패 (%s): %v\n```\n%s\n```", class, err, truncate(output, 500)),
					ReplyTo: threadID,
				})
				// Escalate to daemon
				escalateToHost(dalName, prompt, output, string(class))
				continue
			}
		}

		log.Printf("[agent] done (%d bytes)", len(output))

		// Check if files were modified → auto git workflow
		gitResult := autoGitWorkflow(dalName)

		// Format response
		response := truncate(strings.TrimSpace(output), 3000)
		if gitResult != "" {
			response += "\n\n" + gitResult
		}

		mm.Send(bridge.Message{
			Content: response,
			ReplyTo: threadID,
		})
	}
}

// autoGitWorkflow checks for file changes and creates a branch + commit + PR.
func autoGitWorkflow(dalName string) string {
	// Check if there are changes
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = "/workspace"
	statusOut, err := statusCmd.Output()
	if err != nil || len(strings.TrimSpace(string(statusOut))) == 0 {
		return "" // no changes
	}

	changes := strings.TrimSpace(string(statusOut))
	log.Printf("[git] changes detected:\n%s", changes)

	// Create branch
	branch := fmt.Sprintf("dal/%s/%d", dalName, time.Now().Unix())
	run := func(args ...string) (string, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = "/workspace"
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	if _, err := run("git", "checkout", "-b", branch); err != nil {
		return fmt.Sprintf("⚠️ 브랜치 생성 실패: %v", err)
	}

	// Stage all changes
	if _, err := run("git", "add", "-A"); err != nil {
		return fmt.Sprintf("⚠️ git add 실패: %v", err)
	}

	// Commit
	commitMsg := fmt.Sprintf("feat: %s dal 자동 반영\n\n변경 파일:\n%s\n\nCo-Authored-By: dal-%s <dal-%s@dalcenter.local>",
		dalName, changes, dalName, dalName)
	if _, err := run("git", "commit", "-m", commitMsg); err != nil {
		return fmt.Sprintf("⚠️ 커밋 실패: %v", err)
	}

	// Push
	if _, err := run("git", "push", "-u", "origin", branch); err != nil {
		return fmt.Sprintf("⚠️ 푸시 실패: %v", err)
	}

	// Create PR
	prTitle := fmt.Sprintf("dal/%s: 자동 반영", dalName)
	prBody := fmt.Sprintf("dal-%s가 자동으로 생성한 PR.\n\n변경 파일:\n```\n%s\n```", dalName, changes)
	prOut, err := run("gh", "pr", "create", "--title", prTitle, "--body", prBody)
	if err != nil {
		return fmt.Sprintf("✅ 커밋+푸시 완료 (브랜치: `%s`)\n⚠️ PR 생성 실패: %v", branch, err)
	}

	// Go back to main
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

	// If circuit is open, try fallback provider first
	if providerCircuit.ShouldFallback() && fallbackPlayer != "" {
		log.Printf("[circuit] primary %s is open, trying fallback %s", player, fallbackPlayer)
		out, err := runProvider(fallbackPlayer, task)
		if err == nil {
			return out, nil
		}
		log.Printf("[circuit] fallback %s also failed: %v", fallbackPlayer, err)
		// Fall through to retry primary
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

		// Auth error → wait for credential sync (cron every 5min on host)
		if isAuthError(out) {
			wait := 60 * time.Second
			name := os.Getenv("DAL_NAME")
			log.Printf("[agent] auth error (attempt %d/%d), waiting %s for credential sync...", attempt, maxRetries, wait)
			notifyCredentialRefresh(name)
			time.Sleep(wait)
			continue
		}

		if isRetryable(out) {
			providerCircuit.RecordFailure()

			if providerCircuit.ShouldFallback() && fallbackPlayer != "" {
				log.Printf("[circuit] switching to fallback %s (attempt %d/%d)", fallbackPlayer, attempt, maxRetries)
				fbOut, fbErr := runProvider(fallbackPlayer, task)
				if fbErr == nil {
					return fbOut, nil
				}
				log.Printf("[circuit] fallback %s failed: %v", fallbackPlayer, fbErr)
			}

			wait := time.Duration(attempt*30) * time.Second
			log.Printf("[agent] retrying primary in %s (attempt %d/%d)", wait, attempt, maxRetries)
			time.Sleep(wait)
			continue
		}

		// Non-retryable error
		return out, err
	}

	return lastOut, fmt.Errorf("max retries (%d) exceeded, circuit=%s: %w", maxRetries, providerCircuit.State(), lastErr)
}

// detectFallback returns the fallback player for the given primary.
func detectFallback(primary string) string {
	switch primary {
	case "claude":
		// Check if codex is available
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
		cmd = exec.Command("codex", "exec",
			"--dangerously-bypass-approvals-and-sandbox",
			"-C", "/workspace",
			task)
	default: // claude
		// Build allowed tools based on role and extra permissions
		var allowedTools string
		if extra := os.Getenv("DAL_EXTRA_BASH"); extra == "*" {
			// Unrestricted bash (e.g., verifier running go test)
			allowedTools = "Bash Read Write Glob Grep Edit"
		} else {
			bashPerms := "git:*,gh:*"
			if role == "leader" {
				bashPerms = "dalcli-leader:*,git:*,gh:*"
			}
			if extra != "" {
				bashPerms += "," + extra + ":*"
			}
			allowedTools = fmt.Sprintf("Bash(%s) Read Write Glob Grep Edit", bashPerms)
		}
		cmd = exec.Command("claude",
			"--allowedTools", allowedTools,
			"--print", task)
	}

	cmd.Dir = "/workspace"
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_ENTRYPOINT=dalcli")

	// Capture stdout only, discard stderr (prevents codex warnings in output)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

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
		strings.Contains(lower, "capacity")
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
