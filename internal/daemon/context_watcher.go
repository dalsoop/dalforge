package daemon

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const contextSyncInterval = 10 * time.Minute

// startContextWatcher periodically extracts the host Claude Code session
// to markdown and stores it in .dal/context/ for dal team members to read.
func startContextWatcher(ctx context.Context, serviceRepo string) {
	syncScript := filepath.Join(serviceRepo, "tools", "context-sync.sh")
	if _, err := os.Stat(syncScript); err != nil {
		log.Printf("[context-watcher] sync script not found at %s, skipping", syncScript)
		return
	}

	// Check if claude-extract is available
	if _, err := exec.LookPath("claude-extract"); err != nil {
		log.Printf("[context-watcher] claude-extract not installed, skipping")
		return
	}

	log.Printf("[context-watcher] started (interval=%s)", contextSyncInterval)

	ticker := time.NewTicker(contextSyncInterval)
	defer ticker.Stop()

	// Initial sync
	runContextSync(syncScript, serviceRepo)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runContextSync(syncScript, serviceRepo)
		}
	}
}

func runContextSync(script, repoDir string) {
	cmd := exec.Command("bash", script)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		"DALCENTER_REPO="+repoDir,
		"DALCENTER_CONTEXT_DIR="+filepath.Join(repoDir, ".dal", "context"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[context-watcher] sync failed: %v", err)
		return
	}
	log.Printf("[context-watcher] %s", string(out))
}
