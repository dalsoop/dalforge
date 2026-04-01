package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultNotifyPollInterval = 2 * time.Minute
	reminderInitialDelay      = 5 * time.Minute
	reminderMaxDelay          = 30 * time.Minute
	notifyFilePrefix          = "notify-"
)

// dalrootPending tracks an action awaiting dalroot completion.
type dalrootPending struct {
	IssueNumber int       `json:"issue_number"`
	Title       string    `json:"title"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at"`
	LastRemind  time.Time `json:"last_remind"`
	RemindCount int       `json:"remind_count"`
	Resolved    bool      `json:"resolved"`
}

// dalrootNotifier monitors issue state changes and sends notifications to dalroot.
type dalrootNotifier struct {
	mu       sync.Mutex
	pending  map[int]*dalrootPending
	notified map[int]bool // issue number -> already notified (survives restart)
	filePath string
}

func newDalrootNotifier(path string) *dalrootNotifier {
	n := &dalrootNotifier{
		pending:  make(map[int]*dalrootPending),
		notified: make(map[int]bool),
		filePath: path,
	}
	// Load pending items
	var items []*dalrootPending
	if err := loadJSON(path, &items); err == nil {
		for _, p := range items {
			if !p.Resolved {
				n.pending[p.IssueNumber] = p
			}
		}
	}
	// Load notified set
	var notifiedList []int
	notifiedPath := strings.TrimSuffix(path, ".json") + "-notified.json"
	if err := loadJSON(notifiedPath, &notifiedList); err == nil {
		for _, num := range notifiedList {
			n.notified[num] = true
		}
	}
	return n
}

// saveLocked persists state. Caller MUST NOT hold mu (no lock inside).
func (n *dalrootNotifier) saveLocked() {
	items := make([]*dalrootPending, 0, len(n.pending))
	for _, p := range n.pending {
		items = append(items, p)
	}
	persistJSON(n.filePath, items, nil)

	// Save notified set separately
	notifiedPath := strings.TrimSuffix(n.filePath, ".json") + "-notified.json"
	notifiedList := make([]int, 0, len(n.notified))
	for num := range n.notified {
		notifiedList = append(notifiedList, num)
	}
	// Keep only last 500 to avoid unbounded growth
	if len(notifiedList) > 500 {
		notifiedList = notifiedList[len(notifiedList)-500:]
	}
	persistJSON(notifiedPath, notifiedList, nil)
}

// NotifyDalroot sends a message to dalroot. Writes to a file that the host polls.
func NotifyDalroot(msg string) error {
	notifDir := "/workspace/dalroot-notifications"
	if err := os.MkdirAll(notifDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", notifDir, err)
	}

	filename := fmt.Sprintf("%s%d.txt", notifyFilePrefix, time.Now().UnixMilli())
	path := filepath.Join(notifDir, filename)

	if err := os.WriteFile(path, []byte(msg+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	log.Printf("[dalroot-notifier] sent via file: %s", msg)
	return nil
}

// startDalrootNotifier watches for issue state changes and dalroot pending items.
func (d *Daemon) startDalrootNotifier(ctx context.Context, repo string, interval time.Duration) {
	if repo == "" {
		return
	}
	if interval <= 0 {
		interval = defaultNotifyPollInterval
	}

	n := newDalrootNotifier(filepath.Join(dataDir(d.serviceRepo), "dalroot-pending.json"))
	d.dalrootNotifier = n

	log.Printf("[dalroot-notifier] started (interval=%s, repo=%s)", interval, repo)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[dalroot-notifier] stopped")
			return
		case <-ticker.C:
			d.checkClosedIssues(repo, n)
			d.checkReminders(n)
		}
	}
}

// checkClosedIssues polls recently closed issues and notifies dalroot.
func (d *Daemon) checkClosedIssues(repo string, n *dalrootNotifier) {
	cmd := exec.Command("gh", "issue", "list",
		"--repo", repo,
		"--state", "closed",
		"--limit", "10",
		"--json", "number,title,state,author,closedAt",
	)
	out, err := cmd.Output()
	if err != nil {
		return
	}

	var issues []struct {
		Number   int       `json:"number"`
		Title    string    `json:"title"`
		State    string    `json:"state"`
		Author   ghAuthor  `json:"author"`
		ClosedAt time.Time `json:"closedAt"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	for _, issue := range issues {
		// Only notify for recently closed issues (within last 10 minutes)
		if time.Since(issue.ClosedAt) > 10*time.Minute {
			continue
		}

		// Skip if already notified (persisted across restarts)
		if n.notified[issue.Number] {
			continue
		}

		msg := fmt.Sprintf("[@dalroot] #%d closed: %s (by %s)",
			issue.Number, issue.Title, issue.Author.Login)

		if err := NotifyDalroot(msg); err != nil {
			log.Printf("[dalroot-notifier] failed to notify: %v", err)
			continue
		}

		n.notified[issue.Number] = true
		log.Printf("[dalroot-notifier] notified: #%d closed", issue.Number)
	}

	n.saveLocked()
}

// AddDalrootPending registers a pending action for dalroot to complete.
func (d *Daemon) AddDalrootPending(issueNumber int, title, message string) {
	if d.dalrootNotifier == nil {
		return
	}
	n := d.dalrootNotifier
	n.mu.Lock()
	n.pending[issueNumber] = &dalrootPending{
		IssueNumber: issueNumber,
		Title:       title,
		Message:     message,
		CreatedAt:   time.Now().UTC(),
		LastRemind:  time.Now().UTC(),
	}
	n.saveLocked()
	n.mu.Unlock()
}

// checkReminders sends reminders for pending dalroot actions with exponential backoff.
func (d *Daemon) checkReminders(n *dalrootNotifier) {
	n.mu.Lock()
	defer n.mu.Unlock()

	dirty := false
	for num, p := range n.pending {
		if p.Resolved {
			continue
		}

		// Backoff: 5m, 10m, 20m, 30m, 30m, ...
		delay := reminderInitialDelay
		for i := 0; i < p.RemindCount; i++ {
			delay = delay * 2
			if delay > reminderMaxDelay {
				delay = reminderMaxDelay
				break
			}
		}

		if time.Since(p.LastRemind) < delay {
			continue
		}

		elapsed := time.Since(p.CreatedAt).Round(time.Minute)
		msg := fmt.Sprintf("[@dalroot] 리마인드: #%d %s (%s 경과)",
			num, p.Title, elapsed)

		if err := NotifyDalroot(msg); err != nil {
			log.Printf("[dalroot-notifier] reminder failed: %v", err)
			continue
		}

		p.RemindCount++
		p.LastRemind = time.Now().UTC()
		dirty = true
		log.Printf("[dalroot-notifier] reminder #%d sent for issue #%d", p.RemindCount, num)
	}

	if dirty {
		n.saveLocked()
	}
}
