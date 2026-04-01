package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dalsoop/dalcenter/internal/paths"
)

const (
	opsCheckInterval = 2 * time.Minute
	opsCheckTimeout  = 10 * time.Second
)

// teamEndpoint holds the team name and its dalcenter API URL.
type teamEndpoint struct {
	Name string
	URL  string
}

// healthResponse is the JSON shape returned by /api/health.
type healthResponse struct {
	Status       string `json:"status"`
	DalsRunning  int    `json:"dals_running"`
	RepoCount    int    `json:"repo_count"`
	LeaderStatus string `json:"leader_status"`
}

// startOpsWatcher polls all team dalcenter instances and auto-wakes
// leaders for teams that have zero running dals.
func (d *Daemon) startOpsWatcher(ctx context.Context) {
	log.Printf("[ops-watcher] started (interval=%s)", opsCheckInterval)

	ticker := time.NewTicker(opsCheckInterval)
	defer ticker.Stop()

	client := &http.Client{Timeout: opsCheckTimeout}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[ops-watcher] stopped")
			return
		case <-ticker.C:
			d.opsCheck(client)
		}
	}
}

// opsCheck discovers all teams and checks each one's health.
func (d *Daemon) opsCheck(client *http.Client) {
	teams := discoverTeams()
	if len(teams) == 0 {
		log.Printf("[ops-watcher] no teams discovered — skipping")
		return
	}

	for _, team := range teams {
		if err := d.opsCheckTeam(client, team); err != nil {
			log.Printf("[ops-watcher] %s: %v", team.Name, err)
		}
	}
}

// opsCheckTeam checks a single team's health and auto-wakes leader if needed.
func (d *Daemon) opsCheckTeam(client *http.Client, team teamEndpoint) error {
	health, err := fetchHealth(client, team.URL)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	if health.DalsRunning > 0 {
		return nil
	}

	// Zero dals running — check if this team has a leader to wake
	if health.LeaderStatus == "not_configured" {
		log.Printf("[ops-watcher] %s: no leader configured — skipping", team.Name)
		return nil
	}

	log.Printf("[ops-watcher] %s: 0 dals running, leader_status=%s — attempting wake",
		team.Name, health.LeaderStatus)

	if err := wakeLeaderRemote(client, team.URL); err != nil {
		d.postAlert(fmt.Sprintf(":warning: **ops-watcher**: team `%s` has 0 dals and leader wake failed: %v", team.Name, err))
		return fmt.Errorf("wake leader: %w", err)
	}

	log.Printf("[ops-watcher] %s: leader wake request sent", team.Name)
	d.postAlert(fmt.Sprintf(":rocket: **ops-watcher**: team `%s` had 0 dals — leader auto-waked", team.Name))
	return nil
}

// fetchHealth calls GET /api/health on a dalcenter instance.
func fetchHealth(client *http.Client, baseURL string) (*healthResponse, error) {
	resp, err := client.Get(baseURL + "/api/health")
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var h healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &h, nil
}

// wakeLeaderRemote sends POST /api/wake/{leader} to a remote dalcenter.
// It first discovers the leader name from /api/ps or /api/health context,
// then calls the wake endpoint.
func wakeLeaderRemote(client *http.Client, baseURL string) error {
	// Discover leader name by calling GET /api/status
	leaderName, err := findRemoteLeader(client, baseURL)
	if err != nil {
		return fmt.Errorf("find leader: %w", err)
	}

	token := os.Getenv("DALCENTER_TOKEN")
	req, err := http.NewRequest("POST", baseURL+"/api/wake/"+leaderName, nil)
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
		return fmt.Errorf("wake returned %d", resp.StatusCode)
	}
	return nil
}

// findRemoteLeader queries a dalcenter's /api/status to find the leader dal name.
func findRemoteLeader(client *http.Client, baseURL string) (string, error) {
	resp, err := client.Get(baseURL + "/api/status")
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	var statuses []struct {
		Name string `json:"Name"`
		Role string `json:"Role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}

	for _, s := range statuses {
		if s.Role == "leader" {
			return s.Name, nil
		}
	}
	return "", fmt.Errorf("no leader found in status response")
}

// discoverTeams reads /etc/dalcenter/*.env to build a list of team endpoints.
// Same discovery pattern as the TUI's discoverAddrs.
func discoverTeams() []teamEndpoint {
	hostIP := "localhost"
	commonData, err := os.ReadFile(filepath.Join(paths.ConfigDir(), "common.env"))
	if err == nil {
		for _, line := range strings.Split(string(commonData), "\n") {
			if strings.HasPrefix(line, "DALCENTER_HOST_IP=") {
				hostIP = strings.TrimPrefix(line, "DALCENTER_HOST_IP=")
				break
			}
		}
	}

	entries, err := os.ReadDir(paths.ConfigDir())
	if err != nil {
		return nil
	}

	var teams []teamEndpoint
	for _, e := range entries {
		if e.Name() == "common.env" || !strings.HasSuffix(e.Name(), ".env") {
			continue
		}
		teamName := strings.TrimSuffix(e.Name(), ".env")
		data, err := os.ReadFile(filepath.Join(paths.ConfigDir(), e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "DALCENTER_PORT=") {
				port := strings.TrimPrefix(line, "DALCENTER_PORT=")
				teams = append(teams, teamEndpoint{
					Name: teamName,
					URL:  "http://" + hostIP + ":" + port,
				})
				break
			}
		}
	}
	return teams
}

// opsWatcherEnabled returns true when the ops watcher env var is set.
func opsWatcherEnabled() bool {
	return os.Getenv("DALCENTER_OPS_WATCHER") == "1"
}

