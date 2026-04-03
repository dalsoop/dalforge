package daemon

import (
	"context"
	"log"
	"time"
)

const (
	queueCheckInterval = 60 * time.Second
	taskExpireTimeout  = 20 * time.Minute

	// maxRunningLeader is the max concurrent running tasks for a leader dal.
	maxRunningLeader = 1
	// maxRunningMember is the max concurrent running tasks for a member dal.
	maxRunningMember = 2
)

// startQueueManager runs a goroutine that periodically:
// 1. Expires running tasks that have exceeded taskExpireTimeout.
// 2. Enforces per-dal concurrency limits based on role.
func (d *Daemon) startQueueManager(ctx context.Context) {
	log.Printf("[queue-manager] started (interval=%s, expire=%s, leader=%d, member=%d)",
		queueCheckInterval, taskExpireTimeout, maxRunningLeader, maxRunningMember)

	ticker := time.NewTicker(queueCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[queue-manager] stopped")
			return
		case <-ticker.C:
			d.expireStaleTasks()
		}
	}
}

// expireStaleTasks finds running tasks older than taskExpireTimeout and marks them failed.
func (d *Daemon) expireStaleTasks() {
	now := time.Now().UTC()
	for _, tr := range d.tasks.List() {
		if tr.Status != "running" {
			continue
		}
		if now.Sub(tr.StartedAt) <= taskExpireTimeout {
			continue
		}
		expired := d.tasks.Complete(tr.ID, "failed", "", "expired: running longer than "+taskExpireTimeout.String())
		if expired != nil {
			log.Printf("[queue-manager] expired task %s (dal=%s, age=%s)", tr.ID, tr.Dal, now.Sub(tr.StartedAt).Truncate(time.Second))

			dispatchWebhook(WebhookEvent{
				Event:     "task_expired",
				Dal:       tr.Dal,
				Task:      truncateStr(tr.Task, 200),
				Error:     "expired after " + taskExpireTimeout.String(),
				Timestamp: now.Format(time.RFC3339),
			})
		}
	}
}

// runningTaskCount returns the number of currently running tasks for a given dal.
func (d *Daemon) runningTaskCount(dalName string) int {
	count := 0
	for _, tr := range d.tasks.List() {
		if tr.Dal == dalName && tr.Status == "running" {
			count++
		}
	}
	return count
}

// maxRunningForDal returns the concurrency limit based on the dal's role.
func (d *Daemon) maxRunningForDal(dalName string) int {
	d.mu.RLock()
	c, ok := d.containers[dalName]
	d.mu.RUnlock()
	if !ok {
		return maxRunningMember // default to member limit
	}
	if c.Role == "leader" {
		return maxRunningLeader
	}
	return maxRunningMember
}

// canAcceptTask checks whether a dal can accept a new task based on concurrency limits.
func (d *Daemon) canAcceptTask(dalName string) bool {
	return d.runningTaskCount(dalName) < d.maxRunningForDal(dalName)
}
