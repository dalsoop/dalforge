package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	defaultIssuePollInterval = 5 * time.Minute
	maxTrackedIssues         = 200
)

// ghIssue represents a GitHub issue from `gh issue list --json`.
type ghIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	Labels    []ghLabel `json:"labels"`
	CreatedAt time.Time `json:"createdAt"`
	Author    ghAuthor  `json:"author"`
	URL       string    `json:"url"`
}

type ghLabel struct {
	Name string `json:"name"`
}

type ghAuthor struct {
	Login string `json:"login"`
}

// trackedIssue records a dispatched issue and its processing state.
type trackedIssue struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Author     string    `json:"author"`
	Labels     []string  `json:"labels,omitempty"`
	DetectedAt time.Time `json:"detected_at"`
	TaskID     string    `json:"task_id,omitempty"` // task ID dispatched to leader
	Status     string    `json:"status"`            // "dispatched", "skipped", "error"
	Error      string    `json:"error,omitempty"`
}

// issueStore tracks which issues have been seen and dispatched.
type issueStore struct {
	mu       sync.RWMutex
	issues   map[int]*trackedIssue // issue number -> tracked issue
	filePath string
}

func newIssueStore(path string) *issueStore {
	s := &issueStore{issues: make(map[int]*trackedIssue), filePath: path}
	var items []*trackedIssue
	if err := loadJSON(path, &items); err == nil {
		for _, issue := range items {
			s.issues[issue.Number] = issue
		}
	}
	return s
}

func (s *issueStore) Seen(number int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.issues[number]
	return ok
}

func (s *issueStore) Track(issue *trackedIssue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issues[issue.Number] = issue
	s.save()
}

func (s *issueStore) List() []*trackedIssue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*trackedIssue, 0, len(s.issues))
	for _, issue := range s.issues {
		result = append(result, issue)
	}
	return result
}

func (s *issueStore) save() {
	if s.filePath == "" {
		return
	}
	items := make([]*trackedIssue, 0, len(s.issues))
	for _, issue := range s.issues {
		items = append(items, issue)
	}
	// Evict oldest if over limit
	if len(items) > maxTrackedIssues {
		oldest := items[0]
		for _, item := range items[1:] {
			if item.DetectedAt.Before(oldest.DetectedAt) {
				oldest = item
			}
		}
		delete(s.issues, oldest.Number)
		items = make([]*trackedIssue, 0, len(s.issues))
		for _, issue := range s.issues {
			items = append(items, issue)
		}
	}
	persistJSON(s.filePath, items, nil)
}

// startIssueWatcher periodically polls GitHub issues and dispatches new ones to the leader.
func (d *Daemon) startIssueWatcher(ctx context.Context, repo string, interval time.Duration) {
	if repo == "" {
		log.Printf("[issue-watcher] DALCENTER_GITHUB_REPO not set, skipping")
		return
	}

	// Verify gh CLI is available
	if _, err := exec.LookPath("gh"); err != nil {
		log.Printf("[issue-watcher] gh CLI not found, skipping: %v", err)
		return
	}

	if interval <= 0 {
		interval = defaultIssuePollInterval
	}

	log.Printf("[issue-watcher] started (interval=%s, repo=%s)", interval, repo)

	// Initial poll after short delay to let daemon finish startup
	initialDelay := 30 * time.Second
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	d.pollGitHubIssues(repo)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[issue-watcher] stopped")
			return
		case <-ticker.C:
			d.pollGitHubIssues(repo)
		}
	}
}

// pollGitHubIssues fetches open issues and dispatches new ones to the leader.
func (d *Daemon) pollGitHubIssues(repo string) {
	issues, err := fetchGitHubIssues(repo)
	if err != nil {
		log.Printf("[issue-watcher] fetch failed: %v", err)
		return
	}

	var newCount int
	for _, issue := range issues {
		if d.issues.Seen(issue.Number) {
			continue
		}

		// Skip pull requests (gh issue list may include them)
		if isPullRequest(issue) {
			continue
		}

		newCount++
		tracked := &trackedIssue{
			Number:     issue.Number,
			Title:      issue.Title,
			URL:        issue.URL,
			Author:     issue.Author.Login,
			DetectedAt: time.Now().UTC(),
		}
		for _, l := range issue.Labels {
			tracked.Labels = append(tracked.Labels, l.Name)
		}

		// Dispatch to leader
		taskID, err := d.dispatchIssueToLeader(issue)
		if err != nil {
			log.Printf("[issue-watcher] dispatch #%d failed: %v", issue.Number, err)
			tracked.Status = "error"
			tracked.Error = err.Error()
		} else {
			tracked.Status = "dispatched"
			tracked.TaskID = taskID
			log.Printf("[issue-watcher] dispatched #%d → leader (task=%s)", issue.Number, taskID)
		}

		d.issues.Track(tracked)
	}

	if newCount > 0 {
		log.Printf("[issue-watcher] poll: %d new issues dispatched", newCount)
	}
}

// fetchGitHubIssues calls `gh issue list` to get open issues.
func fetchGitHubIssues(repo string) ([]ghIssue, error) {
	cmd := exec.Command("gh", "issue", "list",
		"--repo", repo,
		"--state", "open",
		"--limit", "30",
		"--json", "number,title,body,state,labels,createdAt,author,url",
	)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh issue list: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh issue list: %w", err)
	}

	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parse issues: %w", err)
	}
	return issues, nil
}

// isPullRequest checks if a gh issue is actually a PR (gh may include PRs).
func isPullRequest(issue ghIssue) bool {
	return strings.Contains(issue.URL, "/pull/")
}

// dispatchIssueToLeader enqueues an issue task via the queue manager.
// The queue manager handles priority dispatch and leader bypass.
func (d *Daemon) dispatchIssueToLeader(issue ghIssue) (string, error) {
	// Build task prompt for leader
	labels := make([]string, 0, len(issue.Labels))
	for _, l := range issue.Labels {
		labels = append(labels, l.Name)
	}

	body := issue.Body
	if len(body) > 2000 {
		body = body[:2000] + "\n...(truncated)"
	}

	prompt := fmt.Sprintf(`새 GitHub 이슈가 등록되었습니다. 분석하고 적절한 member에게 작업을 할당하세요.

## Issue #%d: %s
- Author: %s
- Labels: %s
- URL: %s

### Body
%s

## 지시사항
1. 이슈를 분석하여 작업 범위를 파악하세요
2. 적절한 member dal에게 assign하세요 (dalcli wake <member> --issue %d)
3. member가 깨어나면 dalcli assign <member> "이슈 #%d 작업 지시 내용"으로 작업을 전달하세요
4. 작업 완료 후 PR이 올라오면 dalroot에게 알려주세요`,
		issue.Number, issue.Title,
		issue.Author.Login,
		strings.Join(labels, ", "),
		issue.URL,
		body,
		issue.Number, issue.Number,
	)

	// Determine priority from labels
	priority := PriorityNormal
	for _, l := range issue.Labels {
		if l.Name == "priority:high" || l.Name == "urgent" {
			priority = PriorityHigh
			break
		}
	}

	// Enqueue via queue manager (dal="" means route to leader, with bypass support)
	queueID := d.queueManager.Enqueue("", prompt, priority, "issue", issue.Number)

	// Dispatch webhook notification
	dispatchWebhook(WebhookEvent{
		Event:     "issue_detected",
		Dal:       "queue-manager",
		Task:      fmt.Sprintf("Issue #%d: %s", issue.Number, issue.Title),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	return queueID, nil
}

// handleIssues returns tracked GitHub issues.
// GET /api/issues
func (d *Daemon) handleIssues(w http.ResponseWriter, r *http.Request) {
	issues := d.issues.List()
	respondJSON(w, http.StatusOK, map[string]any{
		"issues": issues,
		"total":  len(issues),
	})
}
