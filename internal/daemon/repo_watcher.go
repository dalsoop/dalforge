package daemon

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

const repoWatchInterval = 2 * time.Minute

// pullResult describes what changed after a git pull.
type pullResult struct {
	pulled      bool
	dalChanged  bool
	goChanged   bool
	beforeHash  string
	afterHash   string
	changedFiles []string
}

// startRepoWatcher periodically fetches the remote and pulls if behind.
// When .dal/ files change, it triggers a sync to propagate updates to running containers.
// When Go files change, it broadcasts a rebuild notification via pipeline.
func startRepoWatcher(ctx context.Context, d *Daemon) {
	repoDir := d.serviceRepo
	if repoDir == "" {
		return
	}

	// Verify it's a git repo
	if !isGitRepo(repoDir) {
		log.Printf("[repo-watcher] %s is not a git repo, skipping", repoDir)
		return
	}

	log.Printf("[repo-watcher] started (interval=%s, repo=%s)", repoWatchInterval, repoDir)

	ticker := time.NewTicker(repoWatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[repo-watcher] stopped")
			return
		case <-ticker.C:
			result := fetchAndPull(repoDir)
			if !result.pulled {
				continue
			}

			log.Printf("[repo-watcher] pulled %s → %s", short(result.beforeHash), short(result.afterHash))

			if result.dalChanged {
				log.Printf("[repo-watcher] .dal/ changed, triggering sync")
				d.runSync()
			}

			if result.goChanged {
				log.Printf("[repo-watcher] Go files changed, broadcasting rebuild notification")
				d.pipeline.Broadcast("[repo-watcher] Go files updated on main — rebuild recommended: " + short(result.beforeHash) + " → " + short(result.afterHash))
			}

			if !result.dalChanged && !result.goChanged {
				log.Printf("[repo-watcher] pulled but no .dal/ or Go changes")
			}
		}
	}
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// fetchAndPull fetches origin and fast-forward pulls if behind.
// Returns a pullResult describing what changed.
func fetchAndPull(repoDir string) pullResult {
	// Get current HEAD before pull
	beforeHash := gitRevParse(repoDir, "HEAD")

	// Fetch
	fetch := exec.Command("git", "fetch", "origin", "--quiet")
	fetch.Dir = repoDir
	if err := fetch.Run(); err != nil {
		log.Printf("[repo-watcher] fetch failed: %v", err)
		return pullResult{}
	}

	// Check if behind
	local := gitRevParse(repoDir, "HEAD")
	remote := gitRevParse(repoDir, "@{u}")
	if local == "" || remote == "" || local == remote {
		return pullResult{}
	}

	// Fast-forward pull only (no merge conflicts)
	pull := exec.Command("git", "pull", "--ff-only", "--quiet")
	pull.Dir = repoDir
	if out, err := pull.CombinedOutput(); err != nil {
		log.Printf("[repo-watcher] pull failed: %v: %s", err, string(out))
		return pullResult{}
	}

	afterHash := gitRevParse(repoDir, "HEAD")
	if beforeHash == afterHash {
		return pullResult{}
	}

	// Get all changed files
	diff := exec.Command("git", "diff", "--name-only", beforeHash, afterHash)
	diff.Dir = repoDir
	out, err := diff.Output()
	if err != nil {
		// If diff fails, assume both changed to be safe
		return pullResult{pulled: true, dalChanged: true, goChanged: true, beforeHash: beforeHash, afterHash: afterHash}
	}

	files := strings.Split(strings.TrimSpace(string(out)), "\n")
	var dalChanged, goChanged bool
	for _, f := range files {
		if strings.HasPrefix(f, ".dal/") {
			dalChanged = true
		}
		if strings.HasSuffix(f, ".go") {
			goChanged = true
		}
	}

	return pullResult{
		pulled:       true,
		dalChanged:   dalChanged,
		goChanged:    goChanged,
		beforeHash:   beforeHash,
		afterHash:    afterHash,
		changedFiles: files,
	}
}

func gitRevParse(dir, ref string) string {
	cmd := exec.Command("git", "rev-parse", ref)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func short(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}
