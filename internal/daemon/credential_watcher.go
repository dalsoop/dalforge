package daemon

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// credCheckInterval is how often to check credential expiry.
	credCheckInterval = 5 * time.Minute
	// credRefreshThreshold is how soon before expiry to trigger refresh.
	credRefreshThreshold = 1 * time.Hour
	// credGitPullInterval is how often to pull from the credential git repo.
	credGitPullInterval = 5 * time.Minute
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
	// Try pulling fresh credentials from git repo first.
	pullCredentialsFromGit(credPaths)

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

// credGitRepoEnv is the env var pointing to the local clone of the credential git repo.
const credGitRepoEnv = "DALCENTER_CRED_GIT_REPO"

// pullCredentialsFromGit pulls the latest credentials from a local git repo
// that is kept in sync with the host via soft-serve. If the repo has newer
// credential files, they are copied to the active credential paths using
// tee to preserve inode (for bind mounts).
func pullCredentialsFromGit(credPaths map[string]string) {
	repoDir := os.Getenv(credGitRepoEnv)
	if repoDir == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		return
	}

	out, err := exec.Command("git", "-C", repoDir, "pull", "--ff-only", "--quiet").CombinedOutput()
	if err != nil {
		log.Printf("[cred-watcher] git pull failed: %v %s", err, strings.TrimSpace(string(out)))
		return
	}

	gitFiles := map[string]string{
		"claude": filepath.Join(repoDir, "claude", ".credentials.json"),
		"codex":  filepath.Join(repoDir, "codex", "auth.json"),
	}

	for player, gitPath := range gitFiles {
		activePath, ok := credPaths[player]
		if !ok {
			continue
		}
		gitInfo, err := os.Stat(gitPath)
		if err != nil {
			continue
		}
		activeInfo, err := os.Stat(activePath)
		if err != nil || gitInfo.ModTime().After(activeInfo.ModTime()) {
			data, err := os.ReadFile(gitPath)
			if err != nil || len(data) < 10 {
				continue
			}
			// Use WriteFile to update content while preserving path.
			// Bind mounts share the inode, so in-place write is needed.
			if err := os.WriteFile(activePath, data, 0600); err != nil {
				log.Printf("[cred-watcher] failed to update %s credential from git: %v", player, err)
				continue
			}
			log.Printf("[cred-watcher] %s credential updated from git repo (mtime=%s)", player, gitInfo.ModTime().Format(time.RFC3339))
		}
	}
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
