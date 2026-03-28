package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ProviderCircuit is a centralized circuit breaker for AI provider rate limits.
// When any dal reports a rate limit, ALL dals switch to fallback for the cooldown period.
type ProviderCircuit struct {
	mu           sync.RWMutex
	primary      string        // default provider (e.g. "claude")
	fallback     string        // fallback provider (e.g. "codex")
	activePlayer string        // currently active provider
	trippedAt    time.Time     // when circuit was tripped
	cooldown     time.Duration // how long to stay on fallback
	trippedBy    string        // which dal triggered it
	reason       string        // error message
}

var globalCircuit = &ProviderCircuit{
	primary:      "claude",
	fallback:     "codex",
	activePlayer: "claude",
	cooldown:     4 * time.Hour,
}

// Trip switches all dals to fallback provider.
func (pc *ProviderCircuit) Trip(dalName, reason string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.activePlayer == pc.fallback {
		// Already on fallback
		return
	}

	pc.activePlayer = pc.fallback
	pc.trippedAt = time.Now()
	pc.trippedBy = dalName
	pc.reason = reason
	log.Printf("[provider-circuit] TRIPPED by %s: %s → %s for %s (reason: %s)",
		dalName, pc.primary, pc.fallback, pc.cooldown, reason)
}

// ActiveProvider returns the currently active provider, resetting if cooldown elapsed.
func (pc *ProviderCircuit) ActiveProvider() string {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.activePlayer == pc.fallback && !pc.trippedAt.IsZero() {
		if time.Since(pc.trippedAt) > pc.cooldown {
			pc.activePlayer = pc.primary
			log.Printf("[provider-circuit] cooldown elapsed → back to %s", pc.primary)
		}
	}
	return pc.activePlayer
}

// Status returns circuit state for API response.
func (pc *ProviderCircuit) Status() map[string]interface{} {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	result := map[string]interface{}{
		"active_provider": pc.activePlayer,
		"primary":         pc.primary,
		"fallback":        pc.fallback,
		"cooldown":        pc.cooldown.String(),
	}
	if pc.activePlayer == pc.fallback && !pc.trippedAt.IsZero() {
		remaining := pc.cooldown - time.Since(pc.trippedAt)
		if remaining < 0 {
			remaining = 0
		}
		result["tripped_at"] = pc.trippedAt.Format(time.RFC3339)
		result["tripped_by"] = pc.trippedBy
		result["reason"] = pc.reason
		result["resets_in"] = remaining.String()
	}
	return result
}

// handleProviderStatus returns the current provider circuit state.
func (d *Daemon) handleProviderStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(globalCircuit.Status())
}

// handleProviderTrip allows a dal to report a rate limit, tripping the circuit.
func (d *Daemon) handleProviderTrip(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DalName string `json:"dal_name"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.DalName == "" {
		http.Error(w, "dal_name required", 400)
		return
	}

	globalCircuit.Trip(req.DalName, req.Reason)

	// Notify via Mattermost if available
	if d.mm != nil && d.channelID != "" {
		msg := fmt.Sprintf("⚡ **Provider Circuit Tripped** by %s\n전체 dal이 **%s → %s**로 전환 (4시간)\n사유: %s",
			req.DalName, globalCircuit.primary, globalCircuit.fallback, req.Reason)
		body := fmt.Sprintf(`{"channel_id":%q,"message":%q}`, d.channelID, msg)
		r2, _ := http.NewRequest("POST", d.mm.URL+"/api/v4/posts", strings.NewReader(body))
		r2.Header.Set("Authorization", "Bearer "+d.mm.AdminToken)
		r2.Header.Set("Content-Type", "application/json")
		http.DefaultClient.Do(r2)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(globalCircuit.Status())
}
