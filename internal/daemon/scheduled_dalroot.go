package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	scheduledDalrootInterval = 30 * time.Minute
	dailySummaryHour         = 9 // 09:00 KST daily summary
	reviewStaleThreshold     = 12 * time.Hour
)

// ghPullRequest represents a GitHub PR from `gh pr list --json`.
type ghPullRequest struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Author    ghAuthor  `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
	Labels    []ghLabel `json:"labels"`
	IsDraft   bool      `json:"isDraft"`
	Reviews   []ghReview `json:"reviews"`
}

// ghReview represents a review on a PR.
type ghReview struct {
	Author    ghAuthor  `json:"author"`
	State     string    `json:"state"`
	SubmittedAt time.Time `json:"submittedAt"`
}

// scheduledDalrootState tracks the last daily summary time.
type scheduledDalrootState struct {
	LastDailySummary time.Time `json:"last_daily_summary"`
}

// scheduledDalrootEnabled returns true when the env var is set.
func scheduledDalrootEnabled() bool {
	return os.Getenv("DALCENTER_SCHEDULED_DALROOT") == "1"
}

// startScheduledDalroot runs periodic pipeline surveillance.
//
// Every 30 minutes:
//   - Open issues without a linked PR → remind the team
//   - Open PRs with no review for 12h+ → remind reviewer
//   - LGTM PRs not merged → instruct leader to merge
//
// Daily at 09:00 KST:
//   - Post a summary to dal-control channel
func (d *Daemon) startScheduledDalroot(ctx context.Context, repo string) {
	if repo == "" {
		log.Printf("[scheduled-dalroot] DALCENTER_GITHUB_REPO not set, skipping")
		return
	}

	if _, err := exec.LookPath("gh"); err != nil {
		log.Printf("[scheduled-dalroot] gh CLI not found, skipping: %v", err)
		return
	}

	statePath := stateDir(d.serviceRepo) + "/scheduled-dalroot.json"
	var state scheduledDalrootState
	_ = loadJSON(statePath, &state)

	log.Printf("[scheduled-dalroot] started (interval=%s, repo=%s)", scheduledDalrootInterval, repo)

	// Initial delay to let daemon settle
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}

	d.runScheduledDalrootCycle(repo, &state, statePath)

	ticker := time.NewTicker(scheduledDalrootInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[scheduled-dalroot] stopped")
			return
		case <-ticker.C:
			d.runScheduledDalrootCycle(repo, &state, statePath)
		}
	}
}

// runScheduledDalrootCycle performs one full check cycle.
func (d *Daemon) runScheduledDalrootCycle(repo string, state *scheduledDalrootState, statePath string) {
	issues, err := fetchGitHubIssues(repo)
	if err != nil {
		log.Printf("[scheduled-dalroot] fetch issues failed: %v", err)
		return
	}

	prs, err := fetchGitHubPRs(repo)
	if err != nil {
		log.Printf("[scheduled-dalroot] fetch PRs failed: %v", err)
		return
	}

	// 1. Open issues without a linked PR
	d.checkOrphanIssues(repo, issues, prs)

	// 2. Open PRs without review for 12h+
	d.checkStalePRs(prs)

	// 3. LGTM PRs not yet merged
	d.checkApprovedPRs(prs)

	// 4. Daily summary at 09:00 KST
	kst := time.FixedZone("KST", 9*60*60)
	now := time.Now().In(kst)
	lastSummary := state.LastDailySummary.In(kst)

	if now.Hour() >= dailySummaryHour && lastSummary.Day() != now.Day() {
		d.postDailySummary(issues, prs)
		state.LastDailySummary = time.Now().UTC()
		persistJSON(statePath, state, nil)
	}
}

// checkOrphanIssues finds open issues that have no associated PR and reminds the team.
func (d *Daemon) checkOrphanIssues(repo string, issues []ghIssue, prs []ghPullRequest) {
	// Build set of issue numbers referenced by open PRs (from title/body "#NNN")
	linkedIssues := make(map[int]bool)
	for _, pr := range prs {
		for _, num := range extractIssueNumbers(pr.Title + " " + pr.URL) {
			linkedIssues[num] = true
		}
	}

	var orphans []ghIssue
	for _, issue := range issues {
		if isPullRequest(issue) {
			continue
		}
		if linkedIssues[issue.Number] {
			continue
		}
		// Only flag issues older than 1 hour to give time for PR creation
		if time.Since(issue.CreatedAt) < time.Hour {
			continue
		}
		orphans = append(orphans, issue)
	}

	if len(orphans) == 0 {
		return
	}

	var lines []string
	lines = append(lines, fmt.Sprintf(":mag: **PR 미연결 이슈 %d건** — 작업 확인 필요", len(orphans)))
	for _, issue := range orphans {
		elapsed := time.Since(issue.CreatedAt).Truncate(time.Minute)
		lines = append(lines, fmt.Sprintf("- #%d %s (%s 경과)", issue.Number, issue.Title, elapsed))
	}

	msg := strings.Join(lines, "\n")
	d.postScheduledAlert(msg)
	log.Printf("[scheduled-dalroot] orphan issues: %d", len(orphans))
}

// checkStalePRs finds PRs open for 12h+ without any review.
func (d *Daemon) checkStalePRs(prs []ghPullRequest) {
	var stale []ghPullRequest
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		if len(pr.Reviews) > 0 {
			continue
		}
		if time.Since(pr.CreatedAt) < reviewStaleThreshold {
			continue
		}
		stale = append(stale, pr)
	}

	if len(stale) == 0 {
		return
	}

	var lines []string
	lines = append(lines, fmt.Sprintf(":hourglass: **리뷰 대기 PR %d건** (12시간+)", len(stale)))
	for _, pr := range stale {
		elapsed := time.Since(pr.CreatedAt).Truncate(time.Hour)
		lines = append(lines, fmt.Sprintf("- #%d %s by %s (%s)", pr.Number, pr.Title, pr.Author.Login, elapsed))
	}

	msg := strings.Join(lines, "\n")
	d.postScheduledAlert(msg)
	log.Printf("[scheduled-dalroot] stale PRs: %d", len(stale))
}

// checkApprovedPRs finds PRs with APPROVED review that are not yet merged.
func (d *Daemon) checkApprovedPRs(prs []ghPullRequest) {
	var approved []ghPullRequest
	for _, pr := range prs {
		if hasApproval(pr.Reviews) {
			approved = append(approved, pr)
		}
	}

	if len(approved) == 0 {
		return
	}

	// Dispatch merge instruction to leader
	for _, pr := range approved {
		msg := fmt.Sprintf(":white_check_mark: **머지 대기 PR** #%d %s — LGTM 완료, leader 머지 필요",
			pr.Number, pr.Title)
		d.postScheduledAlert(msg)

		// Also notify dalroot to trigger merge
		notifyMsg := fmt.Sprintf("[@dalroot] PR #%d LGTM — 머지해주세요: %s", pr.Number, pr.URL)
		if err := NotifyDalroot(notifyMsg); err != nil {
			log.Printf("[scheduled-dalroot] dalroot notify failed for PR #%d: %v", pr.Number, err)
		}
	}

	log.Printf("[scheduled-dalroot] approved PRs awaiting merge: %d", len(approved))
}

// postDailySummary posts a daily pipeline status to the channel.
func (d *Daemon) postDailySummary(issues []ghIssue, prs []ghPullRequest) {
	// Count real issues (not PRs)
	var issueCount int
	for _, issue := range issues {
		if !isPullRequest(issue) {
			issueCount++
		}
	}

	// Count PR stats
	var draftCount, reviewWaiting, approvedCount int
	for _, pr := range prs {
		if pr.IsDraft {
			draftCount++
			continue
		}
		if hasApproval(pr.Reviews) {
			approvedCount++
		} else if len(pr.Reviews) == 0 && time.Since(pr.CreatedAt) >= reviewStaleThreshold {
			reviewWaiting++
		}
	}

	var lines []string
	lines = append(lines, ":clipboard: **일일 파이프라인 요약**")
	lines = append(lines, fmt.Sprintf("- 열린 이슈: %d건", issueCount))
	lines = append(lines, fmt.Sprintf("- 열린 PR: %d건 (draft %d)", len(prs), draftCount))
	if reviewWaiting > 0 {
		lines = append(lines, fmt.Sprintf("- :hourglass: 리뷰 대기 (12h+): %d건", reviewWaiting))
	}
	if approvedCount > 0 {
		lines = append(lines, fmt.Sprintf("- :white_check_mark: 머지 대기 (LGTM): %d건", approvedCount))
	}

	msg := strings.Join(lines, "\n")
	d.postScheduledAlert(msg)
	log.Printf("[scheduled-dalroot] daily summary posted")
}

// postScheduledAlert sends a message via bridge with the dalroot-scheduled username.
func (d *Daemon) postScheduledAlert(message string) {
	if d.bridgeURL == "" {
		log.Printf("[scheduled-dalroot] bridge not configured — logged only: %s", message)
		return
	}
	if err := d.bridgePost(message, "dalroot-scheduled"); err != nil {
		log.Printf("[scheduled-dalroot] bridge post failed: %v", err)
	}
}

// fetchGitHubPRs calls `gh pr list` to get open PRs with review info.
func fetchGitHubPRs(repo string) ([]ghPullRequest, error) {
	cmd := exec.Command("gh", "pr", "list",
		"--repo", repo,
		"--state", "open",
		"--limit", "30",
		"--json", "number,title,url,author,createdAt,labels,isDraft,reviews",
	)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh pr list: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh pr list: %w", err)
	}

	var prs []ghPullRequest
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse PRs: %w", err)
	}
	return prs, nil
}

// hasApproval checks if any review has APPROVED state.
func hasApproval(reviews []ghReview) bool {
	for _, r := range reviews {
		if r.State == "APPROVED" {
			return true
		}
	}
	return false
}

// extractIssueNumbers parses "#NNN" patterns from text.
func extractIssueNumbers(text string) []int {
	var nums []int
	for i := 0; i < len(text); i++ {
		if text[i] == '#' && i+1 < len(text) && text[i+1] >= '1' && text[i+1] <= '9' {
			j := i + 1
			num := 0
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				num = num*10 + int(text[j]-'0')
				j++
			}
			if num > 0 {
				nums = append(nums, num)
			}
			i = j - 1
		}
	}
	return nums
}
