package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dalsoop/dalcenter/internal/bridge"
	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/dalsoop/dalcenter/internal/providerexec"
	"github.com/spf13/cobra"
)

// agentConfig holds bridge connection info fetched from dalcenter daemon.
type agentConfig struct {
	DalName     string `json:"dal_name"`
	BridgeURL   string `json:"bridge_url"`
	Gateway     string `json:"gateway"`
	TeamMembers string `json:"team_members"`
}

var credentialRefreshCooldown = struct {
	mu   sync.Mutex
	last map[string]time.Time
	ttl  time.Duration
}{
	last: make(map[string]time.Time),
	ttl:  10 * time.Minute,
}

var workspaceDir = "/workspace"

func shouldNotifyCredentialRefresh(dalName string) bool {
	if dalName == "" {
		return false
	}
	credentialRefreshCooldown.mu.Lock()
	defer credentialRefreshCooldown.mu.Unlock()
	now := time.Now()
	last, ok := credentialRefreshCooldown.last[dalName]
	if ok && now.Sub(last) < credentialRefreshCooldown.ttl {
		return false
	}
	credentialRefreshCooldown.last[dalName] = now
	return true
}

func shouldDisableDM(raw string) bool {
	if raw == "1" {
		return true
	}
	if raw == "0" {
		return false
	}
	return false
}

func startTrackedRun(dalName, task string) string {
	client, err := dalcenterClientOrFallback()
	if err != nil {
		return ""
	}
	result, err := client.StartTaskRun(dalName, truncate(task, 1000))
	if err != nil {
		log.Printf("[agent] task run start failed: %v", err)
		return ""
	}
	return result.ID
}

func finishTrackedRun(taskID, status, output, errMsg string) {
	if taskID == "" {
		return
	}
	client, err := dalcenterClientOrFallback()
	if err != nil {
		log.Printf("[agent] task run finish skipped for %s: %v", taskID, err)
		return
	}
	if _, err := client.FinishTaskRun(taskID, status, output, errMsg); err != nil {
		log.Printf("[agent] task run finish failed for %s: %v", taskID, err)
	}
}

func appendTrackedRunEvent(taskID, kind, message string) {
	if taskID == "" || strings.TrimSpace(message) == "" {
		return
	}
	client, err := dalcenterClientOrFallback()
	if err != nil {
		log.Printf("[agent] task run event skipped for %s: %v", taskID, err)
		return
	}
	if _, err := client.TaskEvent(taskID, kind, message); err != nil {
		log.Printf("[agent] task run event failed for %s: %v", taskID, err)
	}
}

type taskVerificationSnapshot struct {
	GitDiff    string
	GitChanges int
	Verified   string
	Completion *daemon.CompletionResult
}

type workspaceGitState struct {
	Status       string
	StatusByPath map[string]gitStatusEntry
	Fingerprints map[string]string
}

type gitStatusEntry struct {
	Raw  string
	Code string
	Path string
}

func updateTrackedRunMetadata(taskID string, snapshot *taskVerificationSnapshot) {
	if taskID == "" || snapshot == nil {
		return
	}
	client, err := dalcenterClientOrFallback()
	if err != nil {
		log.Printf("[agent] task run metadata skipped for %s: %v", taskID, err)
		return
	}
	update := daemon.TaskMetadataUpdate{
		GitDiff:    snapshot.GitDiff,
		GitChanges: snapshot.GitChanges,
		Verified:   snapshot.Verified,
		Completion: snapshot.Completion,
	}
	if _, err := client.UpdateTaskRun(taskID, update); err != nil {
		log.Printf("[agent] task run metadata failed for %s: %v", taskID, err)
	}
}

func collectTaskVerification(before *workspaceGitState) *taskVerificationSnapshot {
	snapshot := &taskVerificationSnapshot{
		Verified: "skipped",
		Completion: &daemon.CompletionResult{
			Skipped:    true,
			SkipReason: "verification not collected",
		},
	}

	after, err := captureWorkspaceGitState()
	if err != nil {
		snapshot.Completion.SkipReason = "git status failed"
		return snapshot
	}

	changedEntries := changedGitStatusEntries(before, after)
	snapshot.GitChanges = len(changedEntries)
	if snapshot.GitChanges == 0 {
		snapshot.Verified = "no_changes"
		snapshot.Completion.SkipReason = "no code changes detected"
		return snapshot
	}

	snapshot.Verified = "yes"
	snapshot.GitDiff = buildVerificationDiff(changedEntries)
	snapshot.Completion = collectWorkspaceCompletion()
	return snapshot
}

func workspaceGitSnapshot() (string, string, error) {
	diffCmd := exec.Command("git", "diff", "--stat", "HEAD")
	diffCmd.Dir = workspaceDir
	diffOut, diffErr := diffCmd.Output()

	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = workspaceDir
	statusOut, statusErr := statusCmd.Output()

	if diffErr != nil || statusErr != nil {
		if diffErr != nil {
			return "", "", diffErr
		}
		return "", "", statusErr
	}
	return strings.TrimSpace(string(diffOut)), strings.TrimRight(string(statusOut), "\n"), nil
}

func captureWorkspaceGitState() (*workspaceGitState, error) {
	_, statusOut, err := workspaceGitSnapshot()
	if err != nil {
		return nil, err
	}
	state := &workspaceGitState{
		Status:       statusOut,
		StatusByPath: parseGitStatusEntries(statusOut),
		Fingerprints: map[string]string{},
	}
	for path := range state.StatusByPath {
		state.Fingerprints[path] = workspacePathFingerprint(path)
	}
	return state, nil
}

func parseGitStatusEntries(status string) map[string]gitStatusEntry {
	entries := make(map[string]gitStatusEntry)
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		entry := gitStatusEntry{Raw: line}
		if len(line) >= 2 {
			entry.Code = line[:2]
		}
		if len(line) > 3 {
			entry.Path = strings.TrimSpace(line[3:])
		}
		if idx := strings.Index(entry.Path, " -> "); idx >= 0 {
			entry.Path = strings.TrimSpace(entry.Path[idx+4:])
		}
		if entry.Path == "" {
			continue
		}
		entries[entry.Path] = entry
	}
	return entries
}

func workspacePathFingerprint(path string) string {
	fullPath := filepath.Join(workspaceDir, path)
	info, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error:" + err.Error()
	}
	if info.IsDir() {
		return "dir"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return "symlink:error:" + err.Error()
		}
		sum := sha256.Sum256([]byte(target))
		return fmt.Sprintf("symlink:%x", sum)
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "error:" + err.Error()
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%s:%x", info.Mode().String(), sum)
}

func changedGitStatusEntries(before, after *workspaceGitState) []gitStatusEntry {
	if after == nil {
		return nil
	}
	pathSet := make(map[string]struct{})
	if before != nil {
		for path := range before.StatusByPath {
			pathSet[path] = struct{}{}
		}
	}
	for path := range after.StatusByPath {
		pathSet[path] = struct{}{}
	}

	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	changed := make([]gitStatusEntry, 0, len(paths))
	for _, path := range paths {
		var beforeEntry gitStatusEntry
		var beforeOK bool
		if before != nil {
			beforeEntry, beforeOK = before.StatusByPath[path]
		}
		afterEntry, afterOK := after.StatusByPath[path]
		beforeFP := ""
		if before != nil {
			beforeFP = before.Fingerprints[path]
		}
		afterFP := after.Fingerprints[path]
		if beforeOK == afterOK && beforeEntry.Raw == afterEntry.Raw && beforeFP == afterFP {
			continue
		}
		if afterOK {
			changed = append(changed, afterEntry)
			continue
		}
		changed = append(changed, gitStatusEntry{
			Raw:  "clean " + path,
			Code: "  ",
			Path: path,
		})
	}
	return changed
}

func buildVerificationDiff(entries []gitStatusEntry) string {
	if len(entries) == 0 {
		return ""
	}
	paths := make([]string, 0, len(entries))
	statusLines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Path != "" {
			paths = append(paths, entry.Path)
		}
		if strings.TrimSpace(entry.Raw) != "" {
			statusLines = append(statusLines, entry.Raw)
		}
	}

	diffParts := make([]string, 0, 3)
	if diffStat := workspaceGitDiffForPaths(paths); diffStat != "" {
		diffParts = append(diffParts, diffStat)
	}
	if len(statusLines) > 0 {
		diffParts = append(diffParts, strings.Join(statusLines, "\n"))
	}
	return truncate(strings.Join(diffParts, "\n"), 4000)
}

func workspaceGitDiffForPaths(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	args := append([]string{"diff", "--stat", "HEAD", "--"}, paths...)
	diffOut, _ := runWorkspaceGitCommand(args...)
	args = append([]string{"diff", "--stat", "--cached", "HEAD", "--"}, paths...)
	cachedOut, _ := runWorkspaceGitCommand(args...)

	parts := make([]string, 0, 2)
	if diffOut != "" {
		parts = append(parts, diffOut)
	}
	if cachedOut != "" && cachedOut != diffOut {
		parts = append(parts, cachedOut)
	}
	return strings.Join(parts, "\n")
}

func runWorkspaceGitCommand(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workspaceDir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func collectWorkspaceCompletion() *daemon.CompletionResult {
	if _, err := os.Stat(filepath.Join(workspaceDir, "go.mod")); err != nil {
		return &daemon.CompletionResult{
			Skipped:    true,
			SkipReason: "go.mod not found",
		}
	}

	start := time.Now()
	result := &daemon.CompletionResult{}

	buildOut, buildErr := runWorkspaceCommand("go", "build", "./...")
	if buildErr != nil {
		result.BuildOutput = truncate(buildOut, 2000)
	} else {
		result.BuildOK = true
		if strings.TrimSpace(buildOut) != "" {
			result.BuildOutput = truncate(buildOut, 2000)
		}
	}

	testOut, testErr := runWorkspaceCommand("go", "test", "./...")
	if testErr != nil {
		result.TestOutput = truncate(testOut, 2000)
	} else {
		result.TestOK = true
		if strings.TrimSpace(testOut) != "" {
			result.TestOutput = truncate(testOut, 2000)
		}
	}

	result.Duration = time.Since(start).Round(time.Millisecond).String()
	return result
}

func runWorkspaceCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = workspaceDir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func verificationSummaryLine(snapshot *taskVerificationSnapshot) string {
	if snapshot == nil {
		return ""
	}
	parts := []string{
		"검증:",
		fmt.Sprintf("verified=%s", snapshot.Verified),
		fmt.Sprintf("changes=%d", snapshot.GitChanges),
	}
	if snapshot.Completion != nil {
		if snapshot.Completion.Skipped {
			parts = append(parts, "build/test=skipped", fmt.Sprintf("reason=%s", snapshot.Completion.SkipReason))
		} else {
			parts = append(parts,
				fmt.Sprintf("build=%t", snapshot.Completion.BuildOK),
				fmt.Sprintf("test=%t", snapshot.Completion.TestOK),
				fmt.Sprintf("duration=%s", snapshot.Completion.Duration),
			)
		}
	}
	return strings.Join(parts, " ")
}

func trackedRunURL(taskID string) string {
	if taskID == "" {
		return ""
	}
	externalURL := strings.TrimRight(os.Getenv("DALCENTER_EXTERNAL_URL"), "/")
	if externalURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/runs/%s", externalURL, taskID)
}

func dalcenterClientOrFallback() (*daemon.Client, error) {
	if client, err := daemon.NewClient(); err == nil {
		return client, nil
	}
	if os.Getenv("DALCENTER_URL") == "" {
		_ = os.Setenv("DALCENTER_URL", "http://host.docker.internal:11190")
		return daemon.NewClient()
	}
	return nil, fmt.Errorf("DALCENTER_URL not set")
}

func recordDalActivity(dalName string) {
	client, err := dalcenterClientOrFallback()
	if err != nil {
		return
	}
	if _, err := client.Activity(dalName); err != nil {
		log.Printf("[agent] activity update failed: %v", err)
	}
}

func sendBridgeMessage(br bridge.Bridge, dalName string, cfg *agentConfig, msg bridge.Message) error {
	return br.Send(msg)
}

func runCmd(dalName string) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Start agent loop — poll bridge, execute tasks via Claude, report back",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentLoop(dalName)
		},
	}
}

func runAgentLoop(dalName string) error {
	log.Printf("[agent] starting agent loop for %s", dalName)

	cfg, err := fetchAgentConfig(dalName)
	bridgeAvailable := err == nil && cfg != nil && cfg.BridgeURL != ""

	// Fallback: 환경변수에서 bridge 정보 직접 읽기 (호스트 모드 — Docker 없이 실행 시)
	if !bridgeAvailable {
		if envURL := os.Getenv("DAL_BRIDGE_URL"); envURL != "" {
			cfg = &agentConfig{
				DalName:     dalName,
				BridgeURL:   envURL,
				Gateway:     os.Getenv("DAL_GATEWAY"),
				TeamMembers: os.Getenv("DAL_TEAM_MEMBERS"),
			}
			if cfg.Gateway == "" {
				cfg.Gateway = "dal-team"
			}
			bridgeAvailable = true
			log.Printf("[agent] using env-based bridge config (host mode)")
		}
	}

	// Auto-task-only mode: bridge 없어도 auto_task만 돌릴 수 있음 (scribe 등 백그라운드 dal)
	autoTask := os.Getenv("DAL_AUTO_TASK")
	if !bridgeAvailable && autoTask != "" {
		log.Printf("[agent] bridge not available — entering auto-task-only mode")
		return runAutoTaskOnly(dalName, autoTask)
	}

	if !bridgeAvailable {
		if err != nil {
			return fmt.Errorf("fetch agent config: %w", err)
		}
		return fmt.Errorf("incomplete agent config: bridge_url=%q", cfg.BridgeURL)
	}
	log.Printf("[agent] connected: bridge=%s gateway=%s", cfg.BridgeURL, cfg.Gateway)

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

	br := bridge.NewMatterbridgeBridge(cfg.BridgeURL, "", cfg.Gateway, dalName)
	if err := br.Connect(); err != nil {
		return fmt.Errorf("bridge connect: %w", err)
	}
	defer br.Close()

	log.Printf("[agent] listening...")

	uuidShort := os.Getenv("DAL_UUID_SHORT")
	stableMention := fmt.Sprintf("@dal-%s", dalName)
	mention := stableMention
	var legacyMention string
	if uuidShort != "" {
		legacyMention = fmt.Sprintf("@dal-%s-%s", dalName, uuidShort)
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

	msgC := br.Listen()
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
			recordDalActivity(dalName)
			output, err := executeTask(autoTask)
			if err != nil {
				log.Printf("[agent] auto-task failed: %v", err)
				br.Send(bridge.Message{Content: fmt.Sprintf("⚠️ 자동 검증 실패: %v\n```\n%s\n```", err, truncate(output, 500))})
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
				br.Send(bridge.Message{Content: result})
			} else {
				log.Printf("[agent] auto-task: all passed")
			}
			continue
		}

		// --- MM message handling (existing logic) ---
		if msg.From == br.BotID() {
			log.Printf("[agent] skipped own message: %s", truncate(msg.Content, 40))
			continue
		}

		isDirectMention := containsAnyMention(msg.Content, mention, stableMention, legacyMention, altMention)
		isThreadReply := msg.RootID != "" && isActiveThread(&activeThreads, msg.RootID)
		isDM := false // matterbridge doesn't have DM concept

		log.Printf("[agent] msg from=%s mention=%v(m=%q legacy=%q alt=%q) thread=%v dm=%v content=%s",
			truncate(msg.From, 30), isDirectMention, mention, legacyMention, altMention, isThreadReply, isDM, truncate(msg.Content, 60))

		if shouldIgnoreDalBotMessage(msg, br, isDirectMention, isThreadReply, isDM) {
			log.Printf("[agent] skipped dal-bot follow-up: %s", truncate(msg.Content, 60))
			continue
		}
		if shouldIgnoreOperationalDalBotMessage(msg, br) {
			log.Printf("[agent] skipped operational dal-bot message: %s", truncate(msg.Content, 60))
			continue
		}
		if isDM && isDirectedAtDifferentDal(msg.Content, mention, stableMention, legacyMention, altMention) {
			log.Printf("[agent] skipped DM for different dal: %s", truncate(msg.Content, 60))
			continue
		}
		if isDM && isOperationalNoticeMessage(msg.Content) {
			log.Printf("[agent] skipped operational notice DM: %s", truncate(msg.Content, 60))
			continue
		}

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
			task = stripMentions(msg.Content, mention, stableMention, legacyMention, altMention)
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

		spec := buildTaskSpec(dalName, br, msg, task, isDirectMention, isThreadReply, isDM)
		log.Printf("[agent] message: %s (intent=%s source=%s)", truncate(spec.UserTask, 80), spec.Intent, spec.Source)
		recordDalActivity(dalName)

		if handled, err := handleCredentialStatusQuery(dalName, credentialStatusQueryInput(spec.UserTask, spec.Prompt), spec.ThreadID, spec.Channel, br); err != nil {
			log.Printf("[agent] credential status reply failed: %v", err)
		} else if handled {
			appendHistoryBuffer(dalName, spec.Prompt, "credential status reply", "완료")
			continue
		}

		taskRunID := startTrackedRun(dalName, spec.UserTask)
		var statusMsg string
		runURL := trackedRunURL(taskRunID)
		if runURL != "" {
			statusMsg = fmt.Sprintf("💬 작업 중... ([실행 보기](%s))", runURL)
		} else if externalURL := strings.TrimRight(os.Getenv("DALCENTER_EXTERNAL_URL"), "/"); externalURL != "" {
			logsURL := fmt.Sprintf("%s/api/logs/%s", externalURL, dalName)
			statusMsg = fmt.Sprintf("💬 작업 중... ([로그](%s))", logsURL)
		} else {
			statusMsg = "💬 작업 중..."
		}
		if err := sendBridgeMessage(br, dalName, cfg, bridge.Message{
			Content: statusMsg,
			Channel: spec.Channel,
			ReplyTo: spec.ThreadID,
		}); err != nil {
			log.Printf("[agent] status send failed: %v", err)
		}

		beforeVerification, beforeErr := captureWorkspaceGitState()
		if beforeErr != nil {
			log.Printf("[agent] verification baseline skipped: %v", beforeErr)
		}

		result := runTaskSpec(spec)
		output, err := result.Output, result.Err
		if err != nil {
			log.Printf("[agent] failed: %v", err)

			// Self-repair: try to fix and retry once
			if shouldRetry, fix := selfRepair(spec.Prompt, output, err); shouldRetry {
				log.Printf("[agent] self-repair applied: %s, retrying", fix)
				appendTrackedRunEvent(taskRunID, "self_repair", fmt.Sprintf("Self-repair applied: %s", fix))
				if err := sendBridgeMessage(br, dalName, cfg, bridge.Message{
					Content: fmt.Sprintf("🔧 자가 수리: %s — 재시도 중...", fix),
					Channel: spec.Channel,
					ReplyTo: spec.ThreadID,
				}); err != nil {
					log.Printf("[agent] self-repair send failed: %v", err)
				}
				result = runTaskSpec(spec)
				output, err = result.Output, result.Err
			}

			if err != nil {
				class := classifyTaskError(output)
				result.State = TaskStateFailed
				if class == ErrClassEnv || class == ErrClassDeps {
					result.State = TaskStateBlocked
				}
				finishTrackedRun(taskRunID, string(result.State), truncate(output, 12000), err.Error())
				failureMsg := fmt.Sprintf("❌ 실패 (%s): %v\n```\n%s\n```", class, err, truncate(output, 500))
				if runURL != "" {
					failureMsg += fmt.Sprintf("\n\n[실행 보기](%s)", runURL)
				}
				if err := sendBridgeMessage(br, dalName, cfg, bridge.Message{
					Content: failureMsg,
					Channel: spec.Channel,
					ReplyTo: spec.ThreadID,
				}); err != nil {
					log.Printf("[agent] failure send failed: %v", err)
				}
				appendHistoryBuffer(dalName, spec.Prompt, err.Error(), string(result.State))
				escalateToHost(dalName, spec.Prompt, output, string(class))
				daemon.DispatchTaskFailed(dalName, truncate(spec.Prompt, 200), err.Error(), len(output))
				// Auto-claim for environment/blocked issues
				if class == ErrClassEnv || class == ErrClassDeps {
					autoFileClaim(dalName, class, spec.Prompt, output)
				}
				continue
			}
		}

		log.Printf("[agent] done (%d bytes)", len(output))

		verification := collectTaskVerification(beforeVerification)
		updateTrackedRunMetadata(taskRunID, verification)
		appendTrackedRunEvent(taskRunID, "verification", verificationSummaryLine(verification))

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
			appendTrackedRunEvent(taskRunID, "git", "Auto git workflow produced follow-up output")
		}

		// History buffer: record completed task
		appendHistoryBuffer(dalName, spec.Prompt, truncate(output, 200), string(result.State))

		// Webhook: task complete
		daemon.DispatchTaskComplete(dalName, truncate(spec.Prompt, 200), len(output), gitChanges, prURL)

		// Format response
		response := truncate(strings.TrimSpace(output), 3000)
		if gitResult != "" {
			response += "\n\n" + gitResult
		}
		if summary := verificationSummaryLine(verification); summary != "" {
			response += "\n\n" + summary
		}
		if result.State == TaskStateNoop {
			appendTrackedRunEvent(taskRunID, "result", "Task finished with no changes")
		}
		finishTrackedRun(taskRunID, string(result.State), truncate(response, 12000), "")
		if runURL != "" {
			response += fmt.Sprintf("\n\n[실행 보기](%s)", runURL)
		}

		if err := sendBridgeMessage(br, dalName, cfg, bridge.Message{
			Content: response,
			Channel: spec.Channel,
			ReplyTo: spec.ThreadID,
		}); err != nil {
			log.Printf("[agent] final send failed: %v", err)
		}

		// Report to leader: when a member dal receives a direct task from user (not from leader),
		// notify the leader so they stay in the loop
		role := os.Getenv("DAL_ROLE")
		if role == "member" && isDirectMention && !isFromLeader(msg.From, br) {
			reportToLeader(br, dalName, spec.UserTask, response, spec.ThreadID)
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
		"wisdom.md",        // root-level copy (not .dal/wisdom.md)
		"decisions.md",     // root-level copy
		".dal/data/",       // runtime data
		"now.md",           // state mount leak
		"decisions/inbox/", // state mount leak
		"history-buffer/",  // state mount leak
		"wisdom-inbox/",    // state mount leak
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

// notifyCredentialRefresh tells dalcenter daemon that credentials need refresh.
// It files a host-action claim and posts a short channel notice once per cooldown window.
func notifyCredentialRefresh(dalName string) {
	if !shouldNotifyCredentialRefresh(dalName) {
		log.Printf("[agent] credential refresh already requested recently for %s", dalName)
		return
	}

	client, err := dalcenterClientOrFallback()
	if err != nil {
		log.Printf("[agent] credential refresh request skipped for %s: %v", dalName, err)
		return
	}

	player := os.Getenv("DAL_PLAYER")
	title := "credential 만료로 호스트 sync 필요"
	detail := fmt.Sprintf("%s credential/auth failed; host에서 proxmox-host-setup ai sync --agent %s, pve-sync-creds 실행 필요", player, player)
	context := fmt.Sprintf("kind=credential_sync&player=%s&source=dalcli&source_dal=%s", player, dalName)
	claim, err := client.Claim(dalName, "blocked", title, detail, context)
	if err != nil {
		log.Printf("[agent] credential refresh claim failed for %s: %v", dalName, err)
	} else {
		log.Printf("[agent] credential refresh claim filed for %s: %s", dalName, claim.ID)
	}

	notice := fmt.Sprintf("[%s] ⚠️ credential 만료. 호스트에서 pve-sync-creds 실행 필요.", dalName)
	if claim != nil && claim.ID != "" {
		notice = fmt.Sprintf("%s claim=%s", notice, claim.ID)
	}
	if _, err := client.Message(dalName, notice); err != nil {
		log.Printf("[agent] credential refresh notice failed for %s: %v", dalName, err)
	}
	log.Printf("[agent] credential refresh requested for %s", dalName)
}

func handleCredentialStatusQuery(dalName, prompt, threadID, channel string, br bridge.Bridge) (bool, error) {
	if !isCredentialStatusQuery(prompt) {
		return false, nil
	}
	reply, err := buildCredentialStatusReply(dalName)
	if err != nil {
		reply = fmt.Sprintf("credential-ops 상태 조회 실패: %v\n일반 작업으로 넘기지 않고 여기서 중단합니다.", err)
	}
	if err := br.Send(bridge.Message{
		Content: reply,
		Channel: channel,
		ReplyTo: threadID,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func credentialStatusQueryInput(task, prompt string) string {
	// Only use the latest user-visible task text here.
	// Full thread context can contain stale credential notices and would
	// incorrectly force unrelated follow-up requests into credential mode.
	_ = prompt
	return task
}

func isCredentialStatusQuery(prompt string) bool {
	lower := strings.ToLower(prompt)
	keywords := []string{
		"credential", "크리덴셜", "sync-dal-creds", "pve-sync-creds",
		"auth login", "expiresat", "expires_at", "token exp", "token 만료",
		"credential 만료", "토큰 만료", "호스트 전용", "인증 갱신",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func buildCredentialStatusReply(dalName string) (string, error) {
	client, err := dalcenterClientOrFallback()
	if err != nil {
		return "", err
	}
	claims, err := client.Claims("")
	if err != nil {
		return "", err
	}

	var openCredentialClaims int
	var latestResolved *daemon.Claim
	for i := range claims {
		claim := claims[i]
		if !isCredentialClaimRecord(claim) {
			continue
		}
		if claim.Status == "open" || claim.Status == "acknowledged" {
			openCredentialClaims++
		}
		if claim.Status == "resolved" {
			if latestResolved == nil || claim.Timestamp.After(latestResolved.Timestamp) {
				copied := claim
				latestResolved = &copied
			}
		}
	}

	lines := []string{
		"자동 credential 상태만 기준으로 답합니다.",
		fmt.Sprintf("현재 daemon 기준 open credential claim: %d", openCredentialClaims),
	}
	if latestResolved != nil {
		lines = append(lines, fmt.Sprintf("최근 credential sync: %s %s", latestResolved.ID, latestResolved.Response))
	}
	if claudeExp := readCredentialExpiry(filepath.Join(userHomeDir(), ".claude", ".credentials.json")); claudeExp != "" {
		lines = append(lines, "claude expiresAt: "+claudeExp)
	}
	if codexExp := readCredentialExpiry(filepath.Join(userHomeDir(), ".codex", "auth.json")); codexExp != "" {
		lines = append(lines, "codex access token exp: "+codexExp)
	}
	lines = append(lines, "이 주제는 일반 문서 추론이 아니라 credential-ops 상태로만 안내합니다.")
	lines = append(lines, fmt.Sprintf("%s 기준으로는 daemon claim 상태(open/resolved/rejected)로만 자동 sync 진행 여부를 판단합니다.", dalName))
	return strings.Join(lines, "\n"), nil
}

func isCredentialClaimRecord(claim daemon.Claim) bool {
	if strings.Contains(claim.Context, "kind=credential_sync") {
		return true
	}
	if strings.Contains(claim.Title, "credential 만료") {
		return true
	}
	return strings.Contains(claim.Detail, "credential/auth failed") || strings.Contains(claim.Detail, "auth failed")
}

func readCredentialExpiry(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if strings.Contains(string(data), "claudeAiOauth") {
		var claude struct {
			ClaudeAiOauth struct {
				ExpiresAt int64 `json:"expiresAt"`
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(data, &claude) == nil && claude.ClaudeAiOauth.ExpiresAt > 0 {
			return time.UnixMilli(claude.ClaudeAiOauth.ExpiresAt).UTC().Format(time.RFC3339)
		}
	}
	if strings.Contains(string(data), "expires_at") {
		var codex struct {
			Tokens struct {
				ExpiresAt string `json:"expires_at"`
			} `json:"tokens"`
		}
		if json.Unmarshal(data, &codex) == nil && codex.Tokens.ExpiresAt != "" {
			return codex.Tokens.ExpiresAt
		}
	}
	return ""
}

func userHomeDir() string {
	home, _ := os.UserHomeDir()
	return home
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
	if centralPlayer := fetchCentralProvider(); shouldUseCentralOverride(player, fallbackPlayer, centralPlayer) {
		log.Printf("[central-circuit] using %s (central override)", centralPlayer)
		out, err := runProvider(centralPlayer, task)
		if err == nil {
			return out, nil
		}
		log.Printf("[central-circuit] %s failed: %v, falling through to local circuit", centralPlayer, err)
	} else if centralPlayer != "" && centralPlayer != player {
		log.Printf("[central-circuit] ignoring active provider %s for primary %s (fallback=%s)", centralPlayer, player, fallbackPlayer)
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

func shouldUseCentralOverride(player, fallbackPlayer, centralPlayer string) bool {
	if centralPlayer == "" || centralPlayer == player {
		return false
	}
	return fallbackPlayer != "" && centralPlayer == fallbackPlayer
}

// detectFallback returns the fallback player for the given primary.
// Priority: DAL_FALLBACK_PLAYER env (from dal.cue) → auto-detect by availability.
func detectFallback(primary string) string {
	if fp := os.Getenv("DAL_FALLBACK_PLAYER"); fp != "" {
		if fp != primary && providerexec.Exists(fp) {
			return fp
		}
		// fallback_player == primary or unavailable provider makes no sense; fall through.
	}
	switch primary {
	case "claude":
		if providerexec.Exists("codex") {
			return "codex"
		}
	case "codex":
		if providerexec.Exists("claude") {
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
		codexPath, err := providerexec.Resolve("codex")
		if err != nil {
			return "", err
		}
		workDir, _ := os.Getwd()
		cmd = exec.Command(codexPath, "exec",
			"--dangerously-bypass-approvals-and-sandbox",
			"-C", workDir,
			task)
	default: // claude
		claudePath, err := providerexec.Resolve("claude")
		if err != nil {
			return "", err
		}
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
		cmd = exec.Command(claudePath, claudeArgs...)
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

// runAutoTaskOnly runs only the auto-task loop without bridge.
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
		recordDalActivity(dalName)
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
		recordDalActivity(dalName)
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
func isFromLeader(senderID string, br bridge.Bridge) bool {
	username := br.GetUsername(senderID)
	return strings.Contains(username, "leader")
}

func isFromDalBot(senderID string, br bridge.Bridge) bool {
	username := br.GetUsername(senderID)
	return strings.HasPrefix(username, "dal-")
}

func containsAnyMention(content string, mentions ...string) bool {
	for _, mention := range mentions {
		if mention != "" && strings.Contains(content, mention) {
			return true
		}
	}
	return false
}

func stripMentions(content string, mentions ...string) string {
	trimmed := content
	for _, mention := range mentions {
		if mention == "" {
			continue
		}
		trimmed = strings.ReplaceAll(trimmed, mention, "")
	}
	return strings.TrimSpace(trimmed)
}

func isDirectedAtDifferentDal(content string, selfMentions ...string) bool {
	var seenDalMention bool
	for _, token := range strings.Fields(content) {
		if !strings.HasPrefix(token, "@") {
			continue
		}
		clean := strings.Trim(token, "@,.:;!?()[]{}<>\"'")
		if clean == "" {
			continue
		}
		mention := "@" + clean
		if equalsAnyMention(mention, selfMentions...) {
			return false
		}
		if strings.HasPrefix(clean, "dal-") {
			seenDalMention = true
			continue
		}
		switch clean {
		case "leader", "reviewer", "dev", "verifier":
			seenDalMention = true
		}
	}
	return seenDalMention
}

func equalsAnyMention(content string, mentions ...string) bool {
	for _, mention := range mentions {
		if mention != "" && content == mention {
			return true
		}
	}
	return false
}

func shouldIgnoreDalBotMessage(msg bridge.Message, br bridge.Bridge, isDirectMention, isThreadReply, isDM bool) bool {
	if !isFromDalBot(msg.From, br) {
		return false
	}
	if isDM {
		return true
	}
	if isThreadReply && !isDirectMention && !isFromLeader(msg.From, br) {
		return true
	}
	return false
}

func shouldIgnoreOperationalDalBotMessage(msg bridge.Message, br bridge.Bridge) bool {
	return isFromDalBot(msg.From, br) && isOperationalNoticeMessage(msg.Content)
}

func isOperationalNoticeMessage(content string) bool {
	lower := strings.ToLower(content)
	return (strings.Contains(content, "⚠️ credential 만료") ||
		strings.Contains(lower, "credential/auth failed")) &&
		(strings.Contains(content, "sync-dal-creds.sh") ||
			strings.Contains(content, "pve-sync-creds"))
}

// reportToLeader sends a summary to the leader bot in the same channel
func reportToLeader(br bridge.Bridge, dalName, task, result, threadID string) {
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
	br.Send(bridge.Message{Content: report})
}
