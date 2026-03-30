package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/dalsoop/dalcenter/internal/paths"
)

// dataDir returns the persistent data directory for a service repo.
// Creates .dal/data/ if it doesn't exist.
func dataDir(serviceRepo string) string {
	dir := filepath.Join(serviceRepo, ".dal", "data")
	os.MkdirAll(dir, 0o755)
	return dir
}

// persistJSON writes data to a JSON file atomically.
func persistJSON(path string, data any, mu *sync.RWMutex) {
	if mu != nil {
		mu.RLock()
		defer mu.RUnlock()
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		log.Printf("[persist] marshal error for %s: %v", filepath.Base(path), err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("[persist] write error for %s: %v", filepath.Base(path), err)
		return
	}
	os.Rename(tmp, path)
}

// stateDir returns the git-external state directory for a service repo.
// Path: /var/lib/dalcenter/state/{repo-name}/
func stateDir(serviceRepo string) string {
	base := paths.StateBaseDir()
	// Use basename + short hash to avoid collisions between same-named repos
	baseName := filepath.Base(serviceRepo)
	if baseName == "" || baseName == "." {
		baseName = "default"
	}
	absPath, _ := filepath.Abs(serviceRepo)
	h := sha256.Sum256([]byte(absPath))
	repoName := baseName + "-" + hex.EncodeToString(h[:])[:6]
	dir := filepath.Join(base, repoName)
	os.MkdirAll(dir, 0o755)
	return dir
}

// inboxDir returns the decisions inbox directory (git-external).
func inboxDir(serviceRepo string) string {
	dir := filepath.Join(stateDir(serviceRepo), "decisions", "inbox")
	os.MkdirAll(dir, 0o755)
	return dir
}

// historyBufferDir returns the history buffer directory (git-external).
func historyBufferDir(serviceRepo string) string {
	dir := filepath.Join(stateDir(serviceRepo), "history-buffer")
	os.MkdirAll(dir, 0o755)
	return dir
}

// wisdomInboxDir returns the wisdom proposals inbox (git-external).
func wisdomInboxDir(serviceRepo string) string {
	dir := filepath.Join(stateDir(serviceRepo), "wisdom", "inbox")
	os.MkdirAll(dir, 0o755)
	return dir
}

// nowPath returns the path to now.md (git-external).
func nowPath(serviceRepo string) string {
	dir := stateDir(serviceRepo)
	return filepath.Join(dir, "now.md")
}

// loadJSON reads data from a JSON file.
func loadJSON(path string, target any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
