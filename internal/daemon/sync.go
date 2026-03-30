package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dalsoop/dalcenter/internal/paths"
)

// SyncSubtrees iterates repos under the repos directory and syncs .dal/ subtree
// with soft-serve. Each repo is processed independently — a single failure
// does not stop the rest.
func SyncSubtrees(ctx context.Context) error {
	reposPath := paths.ReposDir()
	entries, err := os.ReadDir(reposPath)
	if err != nil {
		return fmt.Errorf("read repos dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		repoName := entry.Name()
		repoPath := filepath.Join(reposPath, repoName)

		if err := subtreePush(repoPath, repoName); err != nil {
			log.Printf("[sync] push failed for %s: %v", repoName, err)
		}
		if err := subtreePull(repoPath, repoName); err != nil {
			log.Printf("[sync] pull failed for %s: %v", repoName, err)
		}
	}

	return nil
}

// subtreePush pushes .dal/ subtree to soft-serve.
func subtreePush(repoPath, repoName string) error {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git not found: %w", err)
	}

	port := softServeSSHPort()
	remoteURL := fmt.Sprintf("ssh://localhost:%s/%s", port, repoName)

	cmd := exec.Command(gitBin, "subtree", "push", "--prefix=.dal", remoteURL, "main")
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=no")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("subtree push: %s: %w", string(out), err)
	}

	log.Printf("[sync] pushed %s/.dal → soft-serve", repoName)
	return nil
}

// subtreePull pulls .dal/ subtree from soft-serve.
func subtreePull(repoPath, repoName string) error {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git not found: %w", err)
	}

	port := softServeSSHPort()
	remoteURL := fmt.Sprintf("ssh://localhost:%s/%s", port, repoName)

	cmd := exec.Command(gitBin, "subtree", "pull", "--prefix=.dal", remoteURL, "main")
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=no")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("subtree pull: %s: %w", string(out), err)
	}

	log.Printf("[sync] pulled soft-serve → %s/.dal", repoName)
	return nil
}
