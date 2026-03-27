package daemon

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

const repoWatchInterval = 2 * time.Minute

// startRepoWatcher periodically fetches the remote and pulls if behind.
// When .dal/ files change, it triggers a sync to propagate updates to running containers.
func startRepoWatcher(ctx context.Context, repoDir string, syncFn func()) {
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
			if changed := fetchAndPull(repoDir); changed {
				log.Printf("[repo-watcher] changes pulled, triggering sync")
				syncFn()
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
// Returns true if .dal/ files were updated.
func fetchAndPull(repoDir string) bool {
	// Get current HEAD before pull
	beforeHash := gitRevParse(repoDir, "HEAD")

	// Fetch
	fetch := exec.Command("git", "fetch", "origin", "--quiet")
	fetch.Dir = repoDir
	if err := fetch.Run(); err != nil {
		log.Printf("[repo-watcher] fetch failed: %v", err)
		return false
	}

	// Check if behind
	local := gitRevParse(repoDir, "HEAD")
	remote := gitRevParse(repoDir, "@{u}")
	if local == "" || remote == "" || local == remote {
		return false
	}

	// Fast-forward pull only (no merge conflicts)
	pull := exec.Command("git", "pull", "--ff-only", "--quiet")
	pull.Dir = repoDir
	if out, err := pull.CombinedOutput(); err != nil {
		log.Printf("[repo-watcher] pull failed: %v: %s", err, string(out))
		return false
	}

	afterHash := gitRevParse(repoDir, "HEAD")
	if beforeHash == afterHash {
		return false
	}

	log.Printf("[repo-watcher] pulled %s → %s", short(beforeHash), short(afterHash))

	// Check if .dal/ was affected
	diff := exec.Command("git", "diff", "--name-only", beforeHash, afterHash, "--", ".dal/")
	diff.Dir = repoDir
	out, err := diff.Output()
	if err != nil {
		// If diff fails, assume changed to be safe
		return true
	}

	changed := strings.TrimSpace(string(out))
	if changed != "" {
		log.Printf("[repo-watcher] .dal/ changed:\n%s", changed)
		return true
	}

	log.Printf("[repo-watcher] pulled but .dal/ not affected")
	return false
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
