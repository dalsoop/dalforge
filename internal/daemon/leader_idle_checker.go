package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

const (
	leaderIdleCheckInterval = 5 * time.Minute
	leaderIdleThreshold     = 30 * time.Minute
	leaderIdleCheckTimeout  = 10 * time.Second
)

// startLeaderIdleChecker polls all team dalcenter instances and detects
// leaders that have been idle longer than leaderIdleThreshold.
// Unlike ops_watcher (which detects 0-dal teams), this checks leader idle time
// and performs sleep→wake restart when the leader is stuck.
func (d *Daemon) startLeaderIdleChecker(ctx context.Context) {
	log.Printf("[leader-idle] started (interval=%s, threshold=%s)", leaderIdleCheckInterval, leaderIdleThreshold)

	ticker := time.NewTicker(leaderIdleCheckInterval)
	defer ticker.Stop()

	client := &http.Client{Timeout: leaderIdleCheckTimeout}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[leader-idle] stopped")
			return
		case <-ticker.C:
			d.checkLeaderIdle(client)
		}
	}
}

// checkLeaderIdle discovers all teams and checks each leader's idle time.
func (d *Daemon) checkLeaderIdle(client *http.Client) {
	teams := discoverTeams()
	if len(teams) == 0 {
		log.Printf("[leader-idle] no teams discovered — skipping")
		return
	}

	for _, team := range teams {
		if err := d.checkTeamLeaderIdle(client, team); err != nil {
			log.Printf("[leader-idle] %s: %v", team.Name, err)
		}
	}
}

// checkTeamLeaderIdle checks a single team's leader idle status and restarts if needed.
func (d *Daemon) checkTeamLeaderIdle(client *http.Client, team teamEndpoint) error {
	containers, err := fetchPs(client, team.URL)
	if err != nil {
		return fmt.Errorf("fetch ps: %w", err)
	}

	// Find the leader container
	var leader *psContainer
	for i := range containers {
		if containers[i].Role == "leader" {
			leader = &containers[i]
			break
		}
	}

	if leader == nil {
		// No leader container running — wake the leader and alert
		if err := wakeLeaderRemote(client, team.URL); err != nil {
			d.postAlert(fmt.Sprintf(":warning: **doctor**: %s팀 leader 없음 — wake 실패: %v", team.Name, err))
			return fmt.Errorf("wake absent leader: %w", err)
		}
		d.postAlert(fmt.Sprintf(":hospital: **doctor**: %s팀 leader 없음 → 자동 wake", team.Name))
		log.Printf("[leader-idle] %s: no leader container — waked", team.Name)
		return nil
	}

	// Parse idle duration
	if leader.IdleFor == "" {
		return nil // no idle info — recently active
	}

	idle, err := time.ParseDuration(leader.IdleFor)
	if err != nil {
		return fmt.Errorf("parse idle duration %q: %w", leader.IdleFor, err)
	}

	if idle <= leaderIdleThreshold {
		return nil
	}

	// Leader is idle beyond threshold — sleep then wake (restart)
	idleStr := formatDuration(idle)
	log.Printf("[leader-idle] %s: leader %q idle %s — restarting", team.Name, leader.DalName, idleStr)

	if err := sleepLeaderRemote(client, team.URL, leader.DalName); err != nil {
		d.postAlert(fmt.Sprintf(":warning: **doctor**: %s팀 leader idle %s — sleep 실패: %v", team.Name, idleStr, err))
		return fmt.Errorf("sleep leader: %w", err)
	}

	if err := wakeLeaderRemote(client, team.URL); err != nil {
		d.postAlert(fmt.Sprintf(":warning: **doctor**: %s팀 leader idle %s — wake 실패: %v", team.Name, idleStr, err))
		return fmt.Errorf("wake leader after sleep: %w", err)
	}

	d.postAlert(fmt.Sprintf(":hospital: **doctor**: %s팀 leader idle %s → 자동 재시작", team.Name, idleStr))
	log.Printf("[leader-idle] %s: leader restarted", team.Name)
	return nil
}

// psContainer is the JSON shape returned by /api/ps for each container.
type psContainer struct {
	DalName    string `json:"dal_name"`
	Role       string `json:"role"`
	Status     string `json:"status"`
	IdleFor    string `json:"idle_for"`
	LastSeenAt string `json:"last_seen_at"`
}

// fetchPs calls GET /api/ps on a dalcenter instance.
func fetchPs(client *http.Client, baseURL string) ([]psContainer, error) {
	resp, err := client.Get(baseURL + "/api/ps")
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var containers []psContainer
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return containers, nil
}

// sleepLeaderRemote sends POST /api/sleep/{name} to a remote dalcenter.
func sleepLeaderRemote(client *http.Client, baseURL, name string) error {
	token := os.Getenv("DALCENTER_TOKEN")
	req, err := http.NewRequest("POST", baseURL+"/api/sleep/"+name, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("sleep returned %d", resp.StatusCode)
	}
	return nil
}

// formatDuration formats a duration as a human-readable string (e.g., "3h35m").
func formatDuration(d time.Duration) string {
	d = d.Truncate(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// leaderIdleCheckerEnabled returns true when the leader idle checker is enabled.
func leaderIdleCheckerEnabled() bool {
	return os.Getenv("DALCENTER_LEADER_IDLE_CHECK") == "1"
}
