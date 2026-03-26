package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Escalation represents a dal task failure that needs human intervention.
type Escalation struct {
	ID           string    `json:"id"`
	Dal          string    `json:"dal"`
	Task         string    `json:"task"`
	ErrorClass   string    `json:"error_class"`
	Output       string    `json:"output"`
	Timestamp    time.Time `json:"timestamp"`
	Resolved     bool      `json:"resolved"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

// escalationStore is an in-memory store for escalations.
type escalationStore struct {
	mu    sync.RWMutex
	items []Escalation
	seq   int
}

const maxEscalations = 100

func newEscalationStore() *escalationStore {
	return &escalationStore{items: make([]Escalation, 0)}
}

func (s *escalationStore) Add(dal, task, errorClass, output string) Escalation {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++

	// Truncate output
	if len(output) > 1024 {
		output = output[:1024]
	}
	// Truncate task
	if len(task) > 500 {
		task = task[:500]
	}

	esc := Escalation{
		ID:         fmt.Sprintf("esc-%04d", s.seq),
		Dal:        dal,
		Task:       task,
		ErrorClass: errorClass,
		Output:     output,
		Timestamp:  time.Now().UTC(),
	}
	s.items = append(s.items, esc)

	// FIFO eviction
	if len(s.items) > maxEscalations {
		s.items = s.items[len(s.items)-maxEscalations:]
	}
	return esc
}

func (s *escalationStore) Unresolved() []Escalation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Escalation
	for _, e := range s.items {
		if !e.Resolved {
			result = append(result, e)
		}
	}
	return result
}

func (s *escalationStore) Resolve(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id && !s.items[i].Resolved {
			s.items[i].Resolved = true
			now := time.Now().UTC()
			s.items[i].ResolvedAt = &now
			return true
		}
	}
	return false
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// HTTP handlers

func (d *Daemon) handleEscalate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Dal        string `json:"dal"`
		Task       string `json:"task"`
		ErrorClass string `json:"error_class"`
		Output     string `json:"output"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	esc := d.escalations.Add(req.Dal, req.Task, req.ErrorClass, req.Output)
	respondJSON(w, http.StatusOK, esc)
}

func (d *Daemon) handleEscalations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items := d.escalations.Unresolved()
	if items == nil {
		items = []Escalation{}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"escalations": items,
		"count":       len(items),
	})
}

func (d *Daemon) handleResolveEscalation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if d.escalations.Resolve(id) {
		respondJSON(w, http.StatusOK, map[string]any{"resolved": id})
	} else {
		http.Error(w, "escalation not found or already resolved", http.StatusNotFound)
	}
}
