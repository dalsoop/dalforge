package daemon

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// credCheckInterval is how often to check credential expiry.
	credCheckInterval = 5 * time.Minute
	// credRefreshThreshold is how soon before expiry to trigger refresh.
	credRefreshThreshold = 1 * time.Hour
)

// startCredentialWatcher periodically checks credential expiry
// and refreshes tokens before they expire.
func startCredentialWatcher(ctx context.Context, d *Daemon) {
	home, _ := os.UserHomeDir()
	credPaths := map[string]string{
		"claude": filepath.Join(home, ".claude", ".credentials.json"),
		"codex":  filepath.Join(home, ".codex", "auth.json"),
	}

	log.Printf("[cred-watcher] started (interval=%s, threshold=%s)", credCheckInterval, credRefreshThreshold)

	ticker := time.NewTicker(credCheckInterval)
	defer ticker.Stop()

	checkAndRefresh(d, credPaths)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[cred-watcher] stopped")
			return
		case <-ticker.C:
			checkAndRefresh(d, credPaths)
		}
	}
}

func checkAndRefresh(d *Daemon, credPaths map[string]string) {
	for player, path := range credPaths {
		if _, err := os.Stat(path); err != nil {
			continue
		}

		expired, err := isCredentialExpired(path)
		if err != nil {
			continue
		}
		if expired {
			log.Printf("[cred-watcher] %s credential expired — requesting sync", player)
			requestCredentialSyncFromWatcher(d, player)
			continue
		}

		approaching, err := isApproachingExpiry(path, credRefreshThreshold)
		if err != nil {
			continue
		}
		if approaching {
			log.Printf("[cred-watcher] %s credential expiring within %s — requesting sync", player, credRefreshThreshold)
			requestCredentialSyncFromWatcher(d, player)
		}
	}
}

// isApproachingExpiry returns true if the credential expires within the threshold.
func isApproachingExpiry(path string, threshold time.Duration) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	// Claude
	if strings.Contains(string(data), "claudeAiOauth") {
		var claude struct {
			ClaudeAiOauth struct {
				ExpiresAt int64 `json:"expiresAt"`
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(data, &claude) == nil && claude.ClaudeAiOauth.ExpiresAt > 0 {
			return time.Until(time.UnixMilli(claude.ClaudeAiOauth.ExpiresAt)) < threshold, nil
		}
	}

	// Codex
	if strings.Contains(string(data), "expires_at") {
		var codex struct {
			Tokens struct {
				ExpiresAt string `json:"expires_at"`
			} `json:"tokens"`
		}
		if json.Unmarshal(data, &codex) == nil && codex.Tokens.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, codex.Tokens.ExpiresAt); err == nil {
				return time.Until(t) < threshold, nil
			}
		}
	}

	return false, nil
}

// refreshCredential requests the documented host sync flow instead of invoking provider CLIs directly.
func refreshCredential(player string) {
	requestCredentialSyncFromWatcher(nil, player)
}

func requestCredentialSyncFromWatcher(d *Daemon, player string) {
	if d == nil {
		log.Printf("[cred-watcher] %s sync skipped: daemon unavailable", player)
		return
	}
	if !d.requestCredentialSync(credentialSyncRequest{
		Player: player,
		Source: "watcher",
		Repo:   filepath.Base(d.serviceRepo),
	}) {
		log.Printf("[cred-watcher] %s sync already requested recently", player)
	}
}
