package daemon

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// HA role constants.
const (
	RolePrimary = "primary"
	RoleStandby = "standby"
)

// HA state constants.
const (
	StatePrimary  = "primary"
	StateStandby  = "standby"
	StatePromoted = "promoted"
)

// HAState tracks the HA role and promotion state of this dalcenter instance.
type HAState struct {
	mu          sync.RWMutex
	role        string    // configured role: "primary" or "standby"
	state       string    // current state: "primary", "standby", or "promoted"
	promotedAt  time.Time // zero if not promoted
	peerURL     string    // peer dalcenter URL
}

// newHAState reads DALCENTER_ROLE and DALCENTER_PEER_URL to initialize HA state.
// Default role is "primary" (backwards-compatible single-instance).
func newHAState() *HAState {
	role := os.Getenv("DALCENTER_ROLE")
	if role == "" {
		role = RolePrimary
	}
	if role != RolePrimary && role != RoleStandby {
		log.Printf("[ha] invalid DALCENTER_ROLE=%q — defaulting to primary", role)
		role = RolePrimary
	}

	state := StatePrimary
	if role == RoleStandby {
		state = StateStandby
	}

	peerURL := os.Getenv("DALCENTER_PEER_URL")

	return &HAState{
		role:    role,
		state:   state,
		peerURL: peerURL,
	}
}

// IsPrimary returns true if this instance should manage containers.
// Returns true when h is nil (backwards-compatible with tests).
func (h *HAState) IsPrimary() bool {
	if h == nil {
		return true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state == StatePrimary || h.state == StatePromoted
}

// IsStandby returns true if this instance is in standby mode.
// Returns false when h is nil (backwards-compatible with tests).
func (h *HAState) IsStandby() bool {
	if h == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state == StateStandby
}

// State returns the current HA state string.
// Returns "primary" when h is nil.
func (h *HAState) State() string {
	if h == nil {
		return StatePrimary
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state
}

// Role returns the configured role.
// Returns "primary" when h is nil.
func (h *HAState) Role() string {
	if h == nil {
		return RolePrimary
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.role
}

// PeerURL returns the configured peer URL.
// Returns empty string when h is nil.
func (h *HAState) PeerURL() string {
	if h == nil {
		return ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.peerURL
}

// Info returns a snapshot of the HA state for API responses.
// Returns minimal info when h is nil.
func (h *HAState) Info() map[string]any {
	if h == nil {
		return map[string]any{"role": RolePrimary, "state": StatePrimary}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	info := map[string]any{
		"role":  h.role,
		"state": h.state,
	}
	if !h.promotedAt.IsZero() {
		info["promoted_at"] = h.promotedAt.Format(time.RFC3339)
	}
	if h.peerURL != "" {
		info["peer_url"] = h.peerURL
	}
	return info
}

// Promote transitions a standby instance to promoted state.
// Returns an error if the instance is not in standby state.
func (h *HAState) Promote() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != StateStandby {
		return fmt.Errorf("cannot promote: current state is %q, expected %q", h.state, StateStandby)
	}
	h.state = StatePromoted
	h.promotedAt = time.Now()
	log.Printf("[ha] promoted from standby to active (was standby since startup)")
	return nil
}

// Demote transitions a promoted instance back to standby state.
// Used when the original primary recovers.
func (h *HAState) Demote() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != StatePromoted {
		return fmt.Errorf("cannot demote: current state is %q, expected %q", h.state, StatePromoted)
	}
	h.state = StateStandby
	h.promotedAt = time.Time{}
	log.Printf("[ha] demoted back to standby")
	return nil
}

