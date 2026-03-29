package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RegistryEntry tracks a dal instance across daemon restarts.
type RegistryEntry struct {
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	ContainerID string `json:"container_id"`
	BotToken    string `json:"bot_token"`
	Status      string `json:"status"`
}

// Registry is a central store for all dal instances, keyed by UUID.
type Registry struct {
	path    string
	entries map[string]*RegistryEntry // uuid -> entry
	mu      sync.RWMutex
}

func newRegistry(serviceRepo string) *Registry {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".dalcenter")
	os.MkdirAll(dir, 0700)

	h := sha256.Sum256([]byte(serviceRepo))
	hash := hex.EncodeToString(h[:])[:8]

	return &Registry{
		path:    filepath.Join(dir, "registry-"+hash+".json"),
		entries: make(map[string]*RegistryEntry),
	}
}

// Load reads the registry from disk.
func (r *Registry) Load() {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.path)
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, &r.entries); err != nil {
		log.Printf("[registry] failed to parse %s: %v", r.path, err)
		r.entries = make(map[string]*RegistryEntry)
	}
}

// Set stores or updates a registry entry and persists to disk.
func (r *Registry) Set(uuid string, entry RegistryEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries[uuid] = &entry
	r.save()
}

// Get returns a registry entry by UUID.
func (r *Registry) Get(uuid string) *RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entries[uuid]
}

// GetByContainerID finds a registry entry by Docker container ID.
func (r *Registry) GetByContainerID(containerID string) *RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		if e.ContainerID == containerID ||
			strings.HasPrefix(e.ContainerID, containerID) ||
			strings.HasPrefix(containerID, e.ContainerID) {
			return e
		}
	}
	return nil
}

func (r *Registry) save() {
	data, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		log.Printf("[registry] marshal error: %v", err)
		return
	}
	if err := os.WriteFile(r.path, data, 0600); err != nil {
		log.Printf("[registry] write error: %v", err)
	}
}
