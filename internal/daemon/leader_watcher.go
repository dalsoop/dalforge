package daemon

import (
	"context"
	"fmt"
	"log"
	"time"

	sdkcontainer "github.com/docker/go-sdk/container"

	"github.com/dalsoop/dalcenter/internal/localdal"
)

const (
	leaderCheckInterval   = 30 * time.Second
	leaderCheckTimeout    = 10 * time.Second
	leaderFailThreshold   = 3
	leaderMaxRestartRetry = 3
)

// leaderHealth tracks the health state of the leader container.
type leaderHealth struct {
	Status           string    `json:"status"`    // "healthy", "unhealthy", "recovering", "dead"
	ConsecutiveFails int       `json:"consecutive_fails"`
	LastCheckAt      time.Time `json:"last_check_at,omitempty"`
	RestartCount     int       `json:"restart_count"`
}

// startLeaderWatcher periodically checks the leader container health.
// If the leader is unresponsive, it attempts auto-recovery.
// On recovery failure, it escalates to dalroot via the escalation system.
func (d *Daemon) startLeaderWatcher(ctx context.Context) {
	log.Printf("[leader-watcher] started (interval=%s, threshold=%d)", leaderCheckInterval, leaderFailThreshold)

	ticker := time.NewTicker(leaderCheckInterval)
	defer ticker.Stop()

	health := &leaderHealth{Status: "healthy"}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[leader-watcher] stopped")
			return
		case <-ticker.C:
			d.checkLeaderHealth(ctx, health)
		}
	}
}

// checkLeaderHealth inspects the leader container and triggers recovery if needed.
func (d *Daemon) checkLeaderHealth(ctx context.Context, health *leaderHealth) {
	health.LastCheckAt = time.Now()

	leaderName, containerID := d.findLeader()
	if leaderName == "" {
		// No leader registered — nothing to watch
		health.Status = "healthy"
		health.ConsecutiveFails = 0
		return
	}

	running, err := isContainerRunning(ctx, containerID)
	if err != nil {
		log.Printf("[leader-watcher] inspect error for %s: %v", leaderName, err)
		health.ConsecutiveFails++
	} else if !running {
		health.ConsecutiveFails++
		log.Printf("[leader-watcher] leader %s not running (%d/%d)",
			leaderName, health.ConsecutiveFails, leaderFailThreshold)
	} else {
		// Leader is healthy
		if health.Status != "healthy" {
			log.Printf("[leader-watcher] leader %s recovered", leaderName)
		}
		health.Status = "healthy"
		health.ConsecutiveFails = 0
		health.RestartCount = 0
		return
	}

	if health.ConsecutiveFails < leaderFailThreshold {
		health.Status = "unhealthy"
		return
	}

	// Threshold reached — attempt recovery
	health.Status = "recovering"
	log.Printf("[leader-watcher] leader %s failed %d checks — attempting restart",
		leaderName, health.ConsecutiveFails)

	if health.RestartCount >= leaderMaxRestartRetry {
		health.Status = "dead"
		log.Printf("[leader-watcher] leader %s restart retries exhausted — escalating to dalroot", leaderName)
		d.escalateLeaderFailure(leaderName)
		// Reset to allow future retry cycles
		health.RestartCount = 0
		health.ConsecutiveFails = 0
		return
	}

	if err := d.restartLeader(leaderName); err != nil {
		health.RestartCount++
		log.Printf("[leader-watcher] leader restart failed (%d/%d): %v",
			health.RestartCount, leaderMaxRestartRetry, err)
	} else {
		health.Status = "healthy"
		health.ConsecutiveFails = 0
		health.RestartCount++
		d.notifyLeaderRestarted(leaderName)
		log.Printf("[leader-watcher] leader %s restarted successfully", leaderName)
	}
}

// findLeader returns the name and container ID of the leader dal, if any.
func (d *Daemon) findLeader() (name, containerID string) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for n, c := range d.containers {
		if c.Role == "leader" {
			return n, c.ContainerID
		}
	}
	return "", ""
}

// isContainerRunning checks if a Docker container is in running state.
func isContainerRunning(ctx context.Context, containerID string) (bool, error) {
	cli, err := getDockerClient()
	if err != nil {
		return false, fmt.Errorf("docker client: %w", err)
	}

	ctr, err := sdkcontainer.FromID(ctx, cli, containerID)
	if err != nil {
		return false, fmt.Errorf("container from ID: %w", err)
	}

	info, err := ctr.Inspect(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect: %w", err)
	}

	return info.Container.State.Running, nil
}

// restartLeader performs a clean restart of the leader container.
func (d *Daemon) restartLeader(name string) error {
	d.mu.RLock()
	c, ok := d.containers[name]
	d.mu.RUnlock()
	if !ok {
		return fmt.Errorf("leader %q not in containers map", name)
	}

	// Stop existing container
	if err := dockerStop(c.ContainerID); err != nil {
		log.Printf("[leader-watcher] stop failed for %s: %v (continuing with fresh wake)", name, err)
	}

	d.mu.Lock()
	delete(d.containers, name)
	d.mu.Unlock()

	// Fresh wake
	dal, err := d.readDalProfile(name)
	if err != nil {
		return fmt.Errorf("read dal.cue for %s: %w", name, err)
	}

	// Leader watcher always restarts the base instance, so instanceID = UUID
	instanceID := dal.UUID
	containerID, _, err := dockerRun(d.localdalRoot, d.serviceRepo, name, d.addr, d.bridgeURL, dal, instanceID)
	if err != nil {
		return fmt.Errorf("wake %s: %w", name, err)
	}

	ws := dal.Workspace
	if ws == "" {
		ws = "shared"
	}
	d.mu.Lock()
	d.containers[name] = &Container{
		DalName:     name,
		UUID:        dal.UUID,
		InstanceID:  instanceID,
		Player:      dal.Player,
		Role:        dal.Role,
		ContainerID: containerID,
		Status:      "running",
		Workspace:   ws,
		Skills:      len(dal.Skills),
		LastSeenAt:  time.Now().UTC(),
	}
	d.mu.Unlock()

	d.registry.Set(instanceID, RegistryEntry{
		Name:        name,
		Repo:        d.serviceRepo,
		ContainerID: containerID,
		Status:      "running",
	})

	return nil
}

// readDalProfile reads the dal.cue for a given dal name.
func (d *Daemon) readDalProfile(name string) (*localdal.DalProfile, error) {
	return localdal.ReadDalCue(d.dalCuePath(name), name)
}

// notifyLeaderRestarted posts a recovery notice to the team channel.
func (d *Daemon) notifyLeaderRestarted(name string) {
	msg := fmt.Sprintf(":arrows_counterclockwise: **leader 재시작됨** — `%s` 자동 복구 완료", name)
	d.postAlert(msg)

	dispatchWebhook(WebhookEvent{
		Event:     "leader_restarted",
		Dal:       name,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// escalateLeaderFailure sends an escalation when leader recovery fails.
func (d *Daemon) escalateLeaderFailure(name string) {
	msg := fmt.Sprintf(":rotating_light: **leader 복구 실패** — `%s` %d회 재시작 시도 후 실패. dalroot 확인 필요.",
		name, leaderMaxRestartRetry)
	d.postAlert(msg)

	d.escalations.Add(name, "leader-health-check", "leader_unrecoverable",
		fmt.Sprintf("leader %s failed health checks and %d restart attempts", name, leaderMaxRestartRetry))

	dispatchWebhook(WebhookEvent{
		Event:     "escalation",
		Dal:       name,
		Task:      "leader-health-check",
		Error:     "leader unrecoverable after restart retries",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}
