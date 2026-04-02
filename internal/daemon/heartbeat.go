package daemon

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

const (
	// heartbeatInterval is the period between heartbeat pings to leader containers.
	// Set to 4 minutes to stay under Claude Code's 5-minute idle timeout.
	heartbeatInterval = 4 * time.Minute

	// heartbeatPayload is the lightweight message sent via stdin to keep
	// the session alive. Leaders should ignore this silently.
	heartbeatPayload = "/dev/null heartbeat"
)

// startHeartbeat periodically pings leader containers to prevent idle timeout.
// Claude Code has a ~300s idle timeout (anthropics/claude-code#23092); this
// goroutine keeps leaders alive by sending a lightweight stdin message.
func (d *Daemon) startHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	log.Printf("[heartbeat] started (interval=%s)", heartbeatInterval)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[heartbeat] stopped")
			return
		case <-ticker.C:
			d.mu.RLock()
			var leaders []*Container
			for _, c := range d.containers {
				if c.Role == "leader" && c.Status == "running" {
					leaders = append(leaders, c)
				}
			}
			d.mu.RUnlock()

			for _, c := range leaders {
				if err := d.sendHeartbeat(ctx, c); err != nil {
					log.Printf("[heartbeat] %s: %v", c.DalName, err)
				}
			}
		}
	}
}

// sendHeartbeat sends a lightweight stdin message to a container to keep it alive.
func (d *Daemon) sendHeartbeat(ctx context.Context, c *Container) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", c.ContainerID,
		"bash", "-c", "echo heartbeat > /dev/null")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ping container %s: %w", c.ContainerID, err)
	}

	d.markActivity(c.DalName, time.Now().UTC())
	log.Printf("[heartbeat] pinged %s (container=%s)", c.DalName, containerShort(c.ContainerID))
	return nil
}

// containerShort returns a short form of a container ID for logging.
func containerShort(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
