package daemon

import (
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	// queueCheckInterval is how often the queue manager checks for stuck tasks and dispatches queued items.
	queueCheckInterval = 30 * time.Second

	// stuckTaskTimeout is the maximum duration a task can be "running" without events before being cancelled.
	stuckTaskTimeout = 30 * time.Minute

	// leaderTaskThreshold is the max number of running tasks on the leader before bypass dispatch kicks in.
	leaderTaskThreshold = 10

	// queueAlertThreshold triggers a dalroot alert when pending queue size exceeds this.
	queueAlertThreshold = 20

	// maxQueueSize is the maximum number of items in the pending queue.
	maxQueueSize = 100
)

// Priority levels for queued tasks.
const (
	PriorityHigh   = "high"
	PriorityNormal = "normal"
)

// queueItem represents a pending task waiting for dispatch.
type queueItem struct {
	ID        string    `json:"id"`
	Dal       string    `json:"dal"`       // target dal (empty = leader)
	Task      string    `json:"task"`      // task prompt
	Priority  string    `json:"priority"`  // "high" or "normal"
	Source    string    `json:"source"`    // "issue", "api", "bypass"
	IssueNum  int       `json:"issue_num,omitempty"`
	EnqueueAt time.Time `json:"enqueue_at"`
}

// QueueManager manages task dispatch with priority queueing, stuck task detection,
// and leader bypass when the leader is overloaded.
type QueueManager struct {
	mu      sync.Mutex
	queue   []*queueItem
	daemon  *Daemon
	stopCh  chan struct{}
	stopped chan struct{}
}

// newQueueManager creates a new QueueManager bound to the given Daemon.
func newQueueManager(d *Daemon) *QueueManager {
	return &QueueManager{
		queue:   make([]*queueItem, 0),
		daemon:  d,
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// Enqueue adds a task to the priority queue.
// High priority items are inserted before normal priority items.
func (qm *QueueManager) Enqueue(dal, task, priority, source string, issueNum int) string {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	item := &queueItem{
		ID:        newPrefixedUUID("q"),
		Dal:       dal,
		Task:      task,
		Priority:  priority,
		Source:    source,
		IssueNum:  issueNum,
		EnqueueAt: time.Now().UTC(),
	}

	if priority == PriorityHigh {
		// Insert before the first normal-priority item
		inserted := false
		for i, q := range qm.queue {
			if q.Priority != PriorityHigh {
				qm.queue = append(qm.queue[:i+1], qm.queue[i:]...)
				qm.queue[i] = item
				inserted = true
				break
			}
		}
		if !inserted {
			qm.queue = append(qm.queue, item)
		}
	} else {
		qm.queue = append(qm.queue, item)
	}

	// Evict oldest normal-priority items if over limit
	for len(qm.queue) > maxQueueSize {
		// Remove last item (lowest priority, oldest)
		qm.queue = qm.queue[:len(qm.queue)-1]
		log.Printf("[queue-manager] queue overflow, evicted oldest item")
	}

	log.Printf("[queue-manager] enqueued %s (priority=%s, source=%s, dal=%s, pending=%d)",
		item.ID, priority, source, dal, len(qm.queue))

	return item.ID
}

// PendingCount returns the number of items waiting in the queue.
func (qm *QueueManager) PendingCount() int {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	return len(qm.queue)
}

// PendingItems returns a snapshot of items in the queue.
func (qm *QueueManager) PendingItems() []*queueItem {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	items := make([]*queueItem, len(qm.queue))
	copy(items, qm.queue)
	return items
}

// Start begins the queue manager loop. It should be called as a goroutine.
func (qm *QueueManager) Start() {
	defer close(qm.stopped)
	log.Printf("[queue-manager] started (check_interval=%s, stuck_timeout=%s, leader_threshold=%d)",
		queueCheckInterval, stuckTaskTimeout, leaderTaskThreshold)

	ticker := time.NewTicker(queueCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-qm.stopCh:
			log.Printf("[queue-manager] stopped")
			return
		case <-ticker.C:
			qm.cancelStuckTasks()
			qm.dispatchPending()
			qm.checkQueueAlert()
		}
	}
}

// Stop signals the queue manager to stop.
func (qm *QueueManager) Stop() {
	close(qm.stopCh)
	<-qm.stopped
}

// cancelStuckTasks finds running tasks that have exceeded stuckTaskTimeout
// with no recent events, and marks them as failed.
func (qm *QueueManager) cancelStuckTasks() {
	now := time.Now().UTC()
	tasks := qm.daemon.tasks.List()

	for _, tr := range tasks {
		if tr.Status != "running" {
			continue
		}

		// Check last activity: most recent event, or StartedAt if no events
		lastActivity := tr.StartedAt
		if len(tr.Events) > 0 {
			lastActivity = tr.Events[len(tr.Events)-1].At
		}

		if now.Sub(lastActivity) <= stuckTaskTimeout {
			continue
		}

		log.Printf("[queue-manager] stuck task detected: %s (dal=%s, idle=%s)",
			tr.ID, tr.Dal, now.Sub(lastActivity).Round(time.Second))

		qm.daemon.tasks.Complete(tr.ID, "failed", tr.Output,
			fmt.Sprintf("queue-manager: cancelled after %s with no activity", stuckTaskTimeout))

		dispatchWebhook(WebhookEvent{
			Event:     "task_stuck_cancelled",
			Dal:       tr.Dal,
			Task:      truncateStr(tr.Task, 200),
			Timestamp: now.Format(time.RFC3339),
		})
	}
}

// dispatchPending dequeues items and dispatches them to available containers.
func (qm *QueueManager) dispatchPending() {
	qm.mu.Lock()
	if len(qm.queue) == 0 {
		qm.mu.Unlock()
		return
	}
	// Take a snapshot and work outside the lock
	pending := make([]*queueItem, len(qm.queue))
	copy(pending, qm.queue)
	qm.mu.Unlock()

	dispatched := 0
	for _, item := range pending {
		target := qm.resolveTarget(item)
		if target == nil {
			// No available container; stop dispatching
			break
		}

		// Remove from queue
		qm.mu.Lock()
		qm.removeItem(item.ID)
		qm.mu.Unlock()

		// Create task and execute
		tr := qm.daemon.tasks.New(target.DalName, item.Task)
		go qm.daemon.execTaskInContainer(target, tr)

		log.Printf("[queue-manager] dispatched %s → %s (task=%s, source=%s)",
			item.ID, target.DalName, tr.ID, item.Source)
		dispatched++
	}

	if dispatched > 0 {
		log.Printf("[queue-manager] dispatched %d tasks, remaining=%d", dispatched, qm.PendingCount())
	}
}

// resolveTarget determines which container should receive the task.
// It implements leader bypass: if the leader has too many running tasks,
// it routes to an idle member instead.
func (qm *QueueManager) resolveTarget(item *queueItem) *Container {
	d := qm.daemon

	// If a specific dal is requested, use it directly
	if item.Dal != "" {
		d.mu.RLock()
		c := d.containers[item.Dal]
		d.mu.RUnlock()
		if c != nil && c.Status == "running" {
			return c
		}
		return nil
	}

	// Default: route to leader
	d.mu.RLock()
	var leader *Container
	var members []*Container
	for _, c := range d.containers {
		if c.Status != "running" {
			continue
		}
		if c.Role == "leader" {
			leader = c
		} else if c.Role == "member" {
			members = append(members, c)
		}
	}
	d.mu.RUnlock()

	if leader == nil {
		return nil
	}

	// Check if leader is overloaded
	leaderRunning := qm.countRunningTasks(leader.DalName)
	if leaderRunning < leaderTaskThreshold {
		return leader
	}

	// Leader bypass: find idle member
	idle := qm.findIdleMember(members)
	if idle != nil {
		log.Printf("[queue-manager] leader bypass: %s has %d running tasks, routing to idle member %s",
			leader.DalName, leaderRunning, idle.DalName)
		return idle
	}

	// All members busy, still route to leader
	return leader
}

// countRunningTasks counts how many tasks are currently running for a given dal.
func (qm *QueueManager) countRunningTasks(dalName string) int {
	count := 0
	for _, tr := range qm.daemon.tasks.List() {
		if tr.Dal == dalName && tr.Status == "running" {
			count++
		}
	}
	return count
}

// findIdleMember returns the member with the fewest running tasks (0 preferred).
func (qm *QueueManager) findIdleMember(members []*Container) *Container {
	var best *Container
	bestCount := -1

	for _, m := range members {
		count := qm.countRunningTasks(m.DalName)
		if bestCount < 0 || count < bestCount {
			best = m
			bestCount = count
		}
	}

	// Only return if the member actually has capacity (fewer tasks than leader threshold)
	if best != nil && bestCount < leaderTaskThreshold {
		return best
	}
	return nil
}

// removeItem removes an item from the queue by ID. Caller must hold qm.mu.
func (qm *QueueManager) removeItem(id string) {
	for i, item := range qm.queue {
		if item.ID == id {
			qm.queue = append(qm.queue[:i], qm.queue[i+1:]...)
			return
		}
	}
}

// checkQueueAlert sends a dalroot alert if the queue size exceeds the threshold.
func (qm *QueueManager) checkQueueAlert() {
	pending := qm.PendingCount()
	if pending < queueAlertThreshold {
		return
	}

	msg := fmt.Sprintf(":warning: **큐 임계치 초과** — 대기 중인 작업 %d개 (임계치: %d). dalroot 확인 필요.", pending, queueAlertThreshold)
	qm.daemon.postAlert(msg)

	qm.daemon.escalations.Add("queue-manager", "queue-overflow",
		"queue_threshold_exceeded",
		fmt.Sprintf("pending=%d, threshold=%d", pending, queueAlertThreshold))

	log.Printf("[queue-manager] queue alert: %d pending (threshold=%d)", pending, queueAlertThreshold)
}
