package daemon

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	peerCheckInterval = 30 * time.Second
	peerCheckTimeout  = 10 * time.Second
	peerFailThreshold = 3
)

// startPeerWatcher periodically checks the peer dalcenter's health endpoint.
// For standby instances: after peerFailThreshold consecutive failures, triggers
// automatic promotion (active-standby failover).
// For primary instances: sends a bridge alert on peer failure.
func (d *Daemon) startPeerWatcher(ctx context.Context) {
	peerURL := d.ha.PeerURL()
	if peerURL == "" {
		log.Printf("[peer-watcher] DALCENTER_PEER_URL not set — skipping")
		return
	}
	peerURL = strings.TrimRight(peerURL, "/")

	log.Printf("[peer-watcher] started (peer=%s, interval=%s, threshold=%d, role=%s)",
		peerURL, peerCheckInterval, peerFailThreshold, d.ha.Role())

	client := &http.Client{Timeout: peerCheckTimeout}
	healthURL := peerURL + "/api/health"

	ticker := time.NewTicker(peerCheckInterval)
	defer ticker.Stop()

	var consecutiveFails int
	var alerted bool

	for {
		select {
		case <-ctx.Done():
			log.Printf("[peer-watcher] stopped")
			return
		case <-ticker.C:
			if err := checkPeerHealth(client, healthURL); err != nil {
				consecutiveFails++
				log.Printf("[peer-watcher] health check failed (%d/%d): %v",
					consecutiveFails, peerFailThreshold, err)

				if consecutiveFails >= peerFailThreshold && !alerted {
					d.notifyPeerDown(peerURL, err)
					alerted = true

					// Standby auto-promotion: take over when primary is down
					if d.ha.IsStandby() {
						d.promoteToActive(ctx)
					}
				}
			} else {
				if alerted {
					log.Printf("[peer-watcher] peer recovered after alert")
					d.notifyPeerRecovered(peerURL)

					// If we were promoted because peer was down, demote back
					if d.ha.State() == StatePromoted {
						d.demoteToStandby(ctx)
					}
				}
				consecutiveFails = 0
				alerted = false
			}
		}
	}
}

// promoteToActive transitions this standby instance to active, taking over
// container management from the failed primary.
func (d *Daemon) promoteToActive(ctx context.Context) {
	if err := d.ha.Promote(); err != nil {
		log.Printf("[peer-watcher] promotion failed: %v", err)
		return
	}

	msg := ":arrow_up: **dalcenter 승격** — standby가 primary 역할을 인수합니다"
	d.postAlert(msg)

	dispatchWebhook(WebhookEvent{
		Event:     "ha_promoted",
		Dal:       "dalcenter",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	// Reconcile existing containers (takeover from failed primary)
	d.reconcile()

	// Start background watchers that were skipped in standby mode
	go d.startLeaderWatcher(ctx)
	go d.startHeartbeat(ctx)
	go d.startMessageWatchdog(ctx)

	log.Printf("[peer-watcher] promotion complete — now managing containers")
}

// demoteToStandby transitions this promoted instance back to standby when
// the original primary recovers.
func (d *Daemon) demoteToStandby(ctx context.Context) {
	if err := d.ha.Demote(); err != nil {
		log.Printf("[peer-watcher] demotion failed: %v", err)
		return
	}

	msg := ":arrow_down: **dalcenter 강등** — primary 복구 감지, standby로 전환합니다"
	d.postAlert(msg)

	dispatchWebhook(WebhookEvent{
		Event:     "ha_demoted",
		Dal:       "dalcenter",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	// Release containers — primary will reconcile them
	d.mu.Lock()
	d.containers = make(map[string]*Container)
	d.mu.Unlock()

	log.Printf("[peer-watcher] demotion complete — released container management")
}

// checkPeerHealth sends a GET to the peer's /api/health and expects 200.
func checkPeerHealth(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// notifyPeerDown posts a bridge alert about peer failure.
func (d *Daemon) notifyPeerDown(peerURL string, lastErr error) {
	msg := fmt.Sprintf(":warning: **dalcenter peer down** — `%s` failed %d consecutive health checks. Last error: %s",
		peerURL, peerFailThreshold, lastErr)
	d.postAlert(msg)
	log.Printf("[peer-watcher] alert sent: peer %s down", peerURL)
}

// notifyPeerRecovered posts a bridge recovery notice.
func (d *Daemon) notifyPeerRecovered(peerURL string) {
	msg := fmt.Sprintf(":white_check_mark: **dalcenter peer recovered** — `%s` is healthy again", peerURL)
	d.postAlert(msg)
	log.Printf("[peer-watcher] recovery notice sent: peer %s up", peerURL)
}

// postAlert sends a message via matterbridge.
func (d *Daemon) postAlert(message string) {
	if d.bridgeURL == "" {
		log.Printf("[peer-watcher] bridge not configured — alert logged only: %s", message)
		return
	}
	if err := d.bridgePost(message, "dalcenter"); err != nil {
		log.Printf("[peer-watcher] failed to post alert: %v", err)
	}
}
