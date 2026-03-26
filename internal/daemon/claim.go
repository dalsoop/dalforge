package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ClaimType categorizes what kind of feedback a dal is providing.
type ClaimType string

const (
	ClaimBug         ClaimType = "bug"         // something broken
	ClaimImprovement ClaimType = "improvement" // suggestion for better tooling
	ClaimBlocked     ClaimType = "blocked"     // can't proceed without host action
	ClaimEnv         ClaimType = "env"         // environment issue (missing tool, auth, disk)
)

// Claim represents feedback from a dal to the host.
type Claim struct {
	ID        string    `json:"id"`
	Dal       string    `json:"dal"`
	Type      ClaimType `json:"type"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	Context   string    `json:"context,omitempty"` // task/repo context
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"` // "open", "acknowledged", "resolved", "rejected"
	Response  string    `json:"response,omitempty"`
	RespondAt *time.Time `json:"responded_at,omitempty"`
}

type claimStore struct {
	mu       sync.RWMutex
	items    []Claim
	seq      int
	filePath string
}

const maxClaims = 200

func newClaimStore() *claimStore {
	return &claimStore{items: make([]Claim, 0)}
}

// newClaimStoreWithFile creates a persistent claim store.
func newClaimStoreWithFile(path string) *claimStore {
	s := &claimStore{items: make([]Claim, 0), filePath: path}
	s.load()
	return s
}

func (s *claimStore) load() {
	if s.filePath == "" {
		return
	}
	var items []Claim
	if err := loadJSON(s.filePath, &items); err != nil {
		return // first run or corrupt — start fresh
	}
	s.items = items
	// Restore seq from highest ID
	for _, c := range items {
		var n int
		fmt.Sscanf(c.ID, "claim-%d", &n)
		if n > s.seq {
			s.seq = n
		}
	}
}

func (s *claimStore) save() {
	if s.filePath == "" {
		return
	}
	// caller holds lock
	persistJSON(s.filePath, s.items, nil)
}

func (s *claimStore) Add(dal string, claimType ClaimType, title, detail, context string) Claim {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++

	if len(detail) > 2048 {
		detail = detail[:2048]
	}
	if len(title) > 200 {
		title = title[:200]
	}

	c := Claim{
		ID:        fmt.Sprintf("claim-%04d", s.seq),
		Dal:       dal,
		Type:      claimType,
		Title:     title,
		Detail:    detail,
		Context:   context,
		Timestamp: time.Now().UTC(),
		Status:    "open",
	}
	s.items = append(s.items, c)

	if len(s.items) > maxClaims {
		s.items = s.items[len(s.items)-maxClaims:]
	}
	s.save()
	return c
}

func (s *claimStore) List(status string) []Claim {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Claim
	for _, c := range s.items {
		if status == "" || c.Status == status {
			result = append(result, c)
		}
	}
	return result
}

func (s *claimStore) Respond(id, status, response string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].Status = status
			s.items[i].Response = response
			now := time.Now().UTC()
			s.items[i].RespondAt = &now
			s.save()
			return true
		}
	}
	return false
}

func (s *claimStore) Get(id string) *Claim {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.items {
		if c.ID == id {
			return &c
		}
	}
	return nil
}

// --- HTTP handlers ---

// POST /api/claim — dal submits a claim
func (d *Daemon) handleClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Dal     string `json:"dal"`
		Type    string `json:"type"`
		Title   string `json:"title"`
		Detail  string `json:"detail"`
		Context string `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Dal == "" || req.Title == "" {
		http.Error(w, "dal and title are required", http.StatusBadRequest)
		return
	}

	claimType := ClaimType(req.Type)
	switch claimType {
	case ClaimBug, ClaimImprovement, ClaimBlocked, ClaimEnv:
	default:
		claimType = ClaimImprovement
	}

	claim := d.claims.Add(req.Dal, claimType, req.Title, req.Detail, req.Context)

	// Webhook notification
	dispatchWebhook(WebhookEvent{
		Event:     "claim",
		Dal:       req.Dal,
		Task:      req.Title,
		Error:     string(claimType) + ": " + req.Detail,
		Timestamp: claim.Timestamp.Format(time.RFC3339),
	})

	respondJSON(w, http.StatusOK, claim)
}

// GET /api/claims?status=open — list claims
func (d *Daemon) handleClaims(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	items := d.claims.List(status)
	if items == nil {
		items = []Claim{}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"claims": items,
		"count":  len(items),
	})
}

// GET /api/claims/{id} — get single claim
func (d *Daemon) handleClaimGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	claim := d.claims.Get(id)
	if claim == nil {
		http.Error(w, "claim not found", http.StatusNotFound)
		return
	}
	respondJSON(w, http.StatusOK, claim)
}

// POST /api/claims/{id}/respond — host responds to a claim
func (d *Daemon) handleClaimRespond(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	var req struct {
		Status   string `json:"status"`   // "acknowledged", "resolved", "rejected"
		Response string `json:"response"` // human response text
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	switch req.Status {
	case "acknowledged", "resolved", "rejected":
	default:
		http.Error(w, "status must be acknowledged/resolved/rejected", http.StatusBadRequest)
		return
	}

	if d.claims.Respond(id, req.Status, req.Response) {
		respondJSON(w, http.StatusOK, map[string]string{"id": id, "status": req.Status})
	} else {
		http.Error(w, "claim not found", http.StatusNotFound)
	}
}
