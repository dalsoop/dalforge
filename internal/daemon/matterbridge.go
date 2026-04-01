package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ensureMatterbridge checks that the matterbridge systemd service is running
// for the given team. It does not start matterbridge as a child process;
// lifecycle is managed by systemd (matterbridge@<team>.service).
// Returns true if matterbridge is reachable.
func ensureMatterbridge(confPath string) bool {
	if confPath == "" {
		return matterbridgeAlreadyRunning()
	}
	if _, err := os.Stat(confPath); err != nil {
		log.Printf("[matterbridge] config not found: %s (skipping)", confPath)
		return false
	}

	if matterbridgeAlreadyRunning() {
		log.Printf("[matterbridge] service is running (port reachable)")
		return true
	}

	log.Printf("[matterbridge] service not reachable — check matterbridge@ systemd service")
	return false
}

func matterbridgeAlreadyRunning() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:" + DefaultBridgePort, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// parseBridgePort reads the API BindAddress port from a matterbridge TOML config.
func parseBridgePort(confPath string) string {
	data, err := os.ReadFile(confPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "BindAddress") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				port := strings.Trim(parts[len(parts)-1], "\" ")
				return port
			}
		}
	}
	return ""
}

// mmPost posts a message directly to MM API, bypassing matterbridge.
// This avoids the self-skip issue where matterbridge ignores its own messages.
func (d *Daemon) mmPost(text string) error {
	mmURL := os.Getenv("DALCENTER_MM_URL")
	mmToken := os.Getenv("DALCENTER_MM_TOKEN")
	if mmURL == "" || mmToken == "" {
		return fmt.Errorf("DALCENTER_MM_URL or DALCENTER_MM_TOKEN not set")
	}

	// Resolve channel ID from matterbridge config
	channelID := d.resolveMMChannelID(mmURL, mmToken)
	if channelID == "" {
		return fmt.Errorf("could not resolve MM channel ID")
	}

	body := fmt.Sprintf(`{"channel_id":%q,"message":%q}`, channelID, text)
	req, _ := http.NewRequest("POST", strings.TrimRight(mmURL, "/")+"/api/v4/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mmToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mm post %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// resolveMMChannelID finds the MM channel ID from the matterbridge config.
func (d *Daemon) resolveMMChannelID(mmURL, mmToken string) string {
	// Parse channel name from matterbridge config
	channelName := ""
	if d.bridgeConf != "" {
		data, err := os.ReadFile(d.bridgeConf)
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "channel = ") && !strings.Contains(line, "api") {
					channelName = strings.Trim(strings.TrimPrefix(line, "channel = "), "\"")
					break
				}
			}
		}
	}
	if channelName == "" {
		// Fallback: use repo name
		channelName = filepath.Base(d.serviceRepo)
	}

	// Resolve team ID first
	mmTeam := os.Getenv("DALCENTER_MM_TEAM")
	if mmTeam == "" {
		mmTeam = "prelik"
	}

	url := fmt.Sprintf("%s/api/v4/teams/name/%s/channels/name/%s", strings.TrimRight(mmURL, "/"), mmTeam, channelName)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+mmToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.ID
}
