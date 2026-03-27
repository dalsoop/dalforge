package daemon

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
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
	base := os.Getenv("DALCENTER_STATE_DIR")
	if base == "" {
		base = "/var/lib/dalcenter/state"
	}
	repoName := filepath.Base(serviceRepo)
	if repoName == "" || repoName == "." {
		repoName = "default"
	}
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

// loadJSON reads data from a JSON file.
func loadJSON(path string, target any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
