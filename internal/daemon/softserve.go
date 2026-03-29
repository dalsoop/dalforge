package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// startSoftServe starts soft-serve as a child process.
// Returns nil cmd if soft-serve binary not found (non-fatal).
func startSoftServe(ctx context.Context) (*exec.Cmd, error) {
	if softServeAlreadyRunning(300 * time.Millisecond) {
		log.Printf("[soft-serve] port %s already in use; assuming an existing instance and skipping child start", softServeSSHPort())
		return nil, nil
	}

	softBin, err := exec.LookPath("soft")
	if err != nil {
		return nil, nil // not installed, skip
	}

	dataPath := softServeDataPath()
	if err := os.MkdirAll(dataPath, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, softBin, "serve")
	cmd.Env = append(os.Environ(), "SOFT_SERVE_DATA_PATH="+dataPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start soft-serve: %w", err)
	}

	// Wait briefly for soft-serve to be ready
	time.Sleep(2 * time.Second)
	log.Printf("[soft-serve] data=%s", dataPath)

	return cmd, nil
}

func softServeAlreadyRunning(timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", softServeSSHPort()), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// softServeSSHPort returns the SSH port for soft-serve.
func softServeSSHPort() string {
	if p := os.Getenv("SOFT_SERVE_SSH_PORT"); p != "" {
		return p
	}
	return "23231"
}

// softServeDataPath returns the data directory for soft-serve.
func softServeDataPath() string {
	if p := os.Getenv("SOFT_SERVE_DATA_PATH"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dalcenter", "soft-serve")
}

// EnsureSoftServeRepo creates a repository in soft-serve if it doesn't exist.
func EnsureSoftServeRepo(repoName string) error {
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found: %w", err)
	}

	port := softServeSSHPort()
	cmd := exec.Command(sshBin, "-p", port, "-o", "StrictHostKeyChecking=no", "localhost", "repo", "create", repoName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Ignore "already exists" errors
		log.Printf("[soft-serve] repo create %s: %s", repoName, string(out))
	}
	return nil
}

// SetupSubtree adds a git subtree for the localdal repo in the service repo.
func SetupSubtree(serviceRepo, repoName string) error {
	dalDir := filepath.Join(serviceRepo, ".dal")
	if _, err := os.Stat(filepath.Join(dalDir, ".git")); err == nil {
		// Already a git repo or subtree
		return nil
	}

	gitBin, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git not found: %w", err)
	}

	port := softServeSSHPort()
	remoteURL := fmt.Sprintf("ssh://localhost:%s/%s", port, repoName)

	// Check if remote exists
	cmd := exec.Command(gitBin, "-C", serviceRepo, "remote", "get-url", "localdal")
	if err := cmd.Run(); err != nil {
		// Add remote
		add := exec.Command(gitBin, "-C", serviceRepo, "remote", "add", "localdal", remoteURL)
		if out, err := add.CombinedOutput(); err != nil {
			return fmt.Errorf("remote add: %s: %w", string(out), err)
		}
	}

	// subtree add (if .dal/ doesn't exist yet)
	if _, err := os.Stat(dalDir); err != nil {
		sub := exec.Command(gitBin, "-C", serviceRepo, "subtree", "add", "--prefix=.dal", "localdal", "main")
		sub.Env = append(os.Environ(), "GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=no")
		if out, err := sub.CombinedOutput(); err != nil {
			log.Printf("[soft-serve] subtree add: %s", string(out))
			// Non-fatal — .dal/ might already exist from init
		}
	}

	return nil
}
