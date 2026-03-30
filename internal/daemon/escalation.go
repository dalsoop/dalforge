package daemon

import (
	"encoding/json"
	"fmt"
	"log"
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

// escalationStore is a persistent store for escalations.
type escalationStore struct {
	mu       sync.RWMutex
	items    []Escalation
	seq      int
	filePath string
}

const maxEscalations = 100

func newEscalationStore() *escalationStore {
	return &escalationStore{items: make([]Escalation, 0)}
}

func newEscalationStoreWithFile(path string) *escalationStore {
	s := &escalationStore{items: make([]Escalation, 0), filePath: path}
	var items []Escalation
	if err := loadJSON(path, &items); err == nil {
		s.items = items
		for _, e := range items {
			var n int
			fmt.Sscanf(e.ID, "esc-%d", &n)
			if n > s.seq {
				s.seq = n
			}
		}
	}
	return s
}

func (s *escalationStore) save() {
	if s.filePath == "" {
		return
	}
	persistJSON(s.filePath, s.items, nil)
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
	s.save()
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
			s.save()
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
	// Dispatch webhook for escalation
	dispatchWebhook(WebhookEvent{
		Event:     "escalation",
		Dal:       req.Dal,
		Task:      req.Task,
		Error:     req.ErrorClass + ": " + req.Output,
		Timestamp: esc.Timestamp.Format(time.RFC3339),
	})

	// Post escalation notice directly to project channel.
	// This is critical for credential errors: when tokens expire,
	// the leader dal cannot call its AI provider to process the
	// escalation, so the daemon must notify the channel directly.
	d.postEscalationNotice(esc)

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

const escalationNoticeCooldown = 10 * time.Minute

// postEscalationNotice posts an escalation notice directly to the project
// Mattermost channel from the daemon process. This bypasses the leader dal,
// which is essential when credentials are expired and the leader cannot
// call its AI provider.
func (d *Daemon) postEscalationNotice(esc Escalation) {
	if d.mm == nil || d.channelID == "" {
		return
	}

	// Deduplicate: suppress repeated notices for the same dal+error class.
	key := esc.Dal + ":" + esc.ErrorClass
	d.credSyncMu.Lock()
	if d.escNoticeLast == nil {
		d.escNoticeLast = make(map[string]time.Time)
	}
	if last, ok := d.escNoticeLast[key]; ok && time.Since(last) < escalationNoticeCooldown {
		d.credSyncMu.Unlock()
		return
	}
	d.escNoticeLast[key] = time.Now()
	d.credSyncMu.Unlock()

	msg := fmt.Sprintf("[dalcenter] :warning: **에스컬레이션** dal=%s class=%s\n> %s",
		esc.Dal, esc.ErrorClass, truncateEscalationOutput(esc.Output, 300))

	body := fmt.Sprintf(`{"channel_id":%q,"message":%q}`, d.channelID, msg)
	if _, err := mmPost(d.mm.URL, d.mm.AdminToken, "/api/v4/posts", body); err != nil {
		log.Printf("[escalation] notice post failed: %v", err)
	}
}

func truncateEscalationOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
