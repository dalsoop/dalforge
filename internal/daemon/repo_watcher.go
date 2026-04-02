package daemon

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

const repoWatchInterval = 2 * time.Minute

// pullResult describes what happened during a fetch-and-pull cycle.
type pullResult struct {
	dalChanged bool // .dal/ files were updated
	goChanged  bool // .go files were updated
	resetUsed  bool // git reset --hard was used (ff-only failed)
	err        error
}

// startRepoWatcher periodically fetches the remote and pulls if behind.
// When .dal/ files change, it triggers a sync to propagate updates to running containers.
// When .go files change, it rebuilds dalcenter and restarts all teams.
func (d *Daemon) startRepoWatcher(ctx context.Context, repoDir string, syncFn func()) {
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
			res := fetchAndPull(repoDir)
			if res.err != nil {
				msg := fmt.Sprintf("[repo-watcher] :warning: **pull 실패** (reset 포함 실패)\n> %s", res.err)
				log.Print(msg)
				d.repoWatcherAlert(msg)
				continue
			}
			if res.resetUsed {
				log.Printf("[repo-watcher] used git reset --hard origin/main (ff-only failed)")
			}
			if res.dalChanged {
				log.Printf("[repo-watcher] .dal/ changed, triggering sync")
				syncFn()
			}
			if res.goChanged {
				log.Printf("[repo-watcher] .go files changed, triggering build")
				d.buildAndRestart(repoDir)
			}
		}
	}
}

// fetchAndPull fetches origin and fast-forward pulls if behind.
// If ff-only fails, falls back to git reset --hard origin/main.
func fetchAndPull(repoDir string) pullResult {
	// Get current HEAD before pull
	beforeHash := gitRevParse(repoDir, "HEAD")

	// Fetch
	fetch := exec.Command("git", "fetch", "origin", "--quiet")
	fetch.Dir = repoDir
	if err := fetch.Run(); err != nil {
		log.Printf("[repo-watcher] fetch failed: %v", err)
		return pullResult{err: fmt.Errorf("fetch failed: %w", err)}
	}

	// Check if behind
	local := gitRevParse(repoDir, "HEAD")
	remote := gitRevParse(repoDir, "@{u}")
	if local == "" || remote == "" || local == remote {
		return pullResult{}
	}

	var resetUsed bool

	// Fast-forward pull only (no merge conflicts)
	pull := exec.Command("git", "pull", "--ff-only", "--quiet")
	pull.Dir = repoDir
	if out, err := pull.CombinedOutput(); err != nil {
		log.Printf("[repo-watcher] ff-only pull failed: %v: %s — falling back to reset", err, string(out))
		// main should have no local commits; force-reset to origin
		reset := exec.Command("git", "reset", "--hard", "origin/main")
		reset.Dir = repoDir
		if rout, rerr := reset.CombinedOutput(); rerr != nil {
			return pullResult{err: fmt.Errorf("ff-only failed (%w), reset also failed: %s", err, string(rout))}
		}
		resetUsed = true
	}

	afterHash := gitRevParse(repoDir, "HEAD")
	if beforeHash == afterHash {
		return pullResult{}
	}

	log.Printf("[repo-watcher] pulled %s → %s", short(beforeHash), short(afterHash))

	dalChanged := hasChangesIn(repoDir, beforeHash, afterHash, ".dal/")
	goChanged := hasGoChanges(repoDir, beforeHash, afterHash)

	if dalChanged {
		log.Printf("[repo-watcher] .dal/ changed")
	}
	if goChanged {
		log.Printf("[repo-watcher] .go files changed")
	}

	return pullResult{
		dalChanged: dalChanged,
		goChanged:  goChanged,
		resetUsed:  resetUsed,
	}
}

// hasChangesIn checks if any files under the given path prefix changed between two commits.
func hasChangesIn(repoDir, before, after, pathPrefix string) bool {
	diff := exec.Command("git", "diff", "--name-only", before, after, "--", pathPrefix)
	diff.Dir = repoDir
	out, err := diff.Output()
	if err != nil {
		return true // assume changed to be safe
	}
	return strings.TrimSpace(string(out)) != ""
}

// hasGoChanges checks if any .go files changed between two commits.
func hasGoChanges(repoDir, before, after string) bool {
	diff := exec.Command("git", "diff", "--name-only", before, after)
	diff.Dir = repoDir
	out, err := diff.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasSuffix(line, ".go") {
			return true
		}
	}
	return false
}

// buildAndRestart builds dalcenter and restarts all running teams.
func (d *Daemon) buildAndRestart(repoDir string) {
	build := exec.Command("go", "build", "-o", "/usr/local/bin/dalcenter", "./cmd/dalcenter/")
	build.Dir = repoDir
	if out, err := build.CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[repo-watcher] :x: **빌드 실패**\n```\n%s\n```", strings.TrimSpace(string(out)))
		log.Print(msg)
		d.repoWatcherAlert(msg)
		return
	}
	log.Printf("[repo-watcher] build succeeded")

	// Find running dalcenter@* teams
	teams := listRunningTeams()
	if len(teams) == 0 {
		log.Printf("[repo-watcher] no running teams to restart")
		return
	}

	for _, unit := range teams {
		restart := exec.Command("systemctl", "restart", unit)
		if out, err := restart.CombinedOutput(); err != nil {
			msg := fmt.Sprintf("[repo-watcher] :warning: **재시작 실패** unit=%s\n> %s", unit, strings.TrimSpace(string(out)))
			log.Print(msg)
			d.repoWatcherAlert(msg)
		} else {
			log.Printf("[repo-watcher] restarted %s", unit)
		}
	}
}

// listRunningTeams returns running dalcenter@* unit names.
func listRunningTeams() []string {
	cmd := exec.Command("systemctl", "list-units", "dalcenter@*", "--no-legend")
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[repo-watcher] list-units failed: %v", err)
		return nil
	}
	var units []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// First field is the unit name
		fields := strings.Fields(line)
		if len(fields) > 0 {
			units = append(units, fields[0])
		}
	}
	return units
}

// repoWatcherAlert posts a warning message to dal-control channel via bridge.
func (d *Daemon) repoWatcherAlert(message string) {
	if d.bridgeURL == "" {
		log.Printf("[repo-watcher] bridge not configured — alert logged only: %s", message)
		return
	}
	if err := d.bridgePost(message, "dalcenter"); err != nil {
		log.Printf("[repo-watcher] bridge post failed: %v", err)
	}
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	return cmd.Run() == nil
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
