package daemon

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// HA roles for dalcenter instances.
const (
	RolePrimary   = "primary"
	RoleStandby   = "standby"
	RoleStandalone = "" // default — no HA, backwards compatible
)

// haState tracks the high-availability state of this dalcenter instance.
type haState struct {
	role      string       // configured role: primary, standby, or standalone
	promoted  atomic.Bool  // true if standby has auto-promoted to active
	promotedAt time.Time
}

// initHA reads DALCENTER_ROLE and initialises HA state.
// Returns the configured role. An invalid value is treated as standalone.
func initHA() *haState {
	role := strings.ToLower(strings.TrimSpace(os.Getenv("DALCENTER_ROLE")))
	switch role {
	case RolePrimary, RoleStandby:
		log.Printf("[ha] role=%s", role)
	default:
		if role != "" {
			log.Printf("[ha] unknown DALCENTER_ROLE %q — running as standalone", role)
		}
		role = RoleStandalone
	}
	return &haState{role: role}
}

// isStandby returns true when this instance is configured as standby
// and has NOT been promoted.
func (h *haState) isStandby() bool {
	return h.role == RoleStandby && !h.promoted.Load()
}

// isActive returns true when this instance should manage dal containers.
// Standalone and primary are always active. Standby is active only after promotion.
func (h *haState) isActive() bool {
	switch h.role {
	case RoleStandalone, RolePrimary:
		return true
	case RoleStandby:
		return h.promoted.Load()
	default:
		return true
	}
}

// promote transitions a standby instance to active.
// Returns an error if not in standby role or already promoted.
func (h *haState) promote() error {
	if h.role != RoleStandby {
		return fmt.Errorf("cannot promote: role is %q, not standby", h.role)
	}
	if h.promoted.Load() {
		return fmt.Errorf("already promoted")
	}
	h.promoted.Store(true)
	h.promotedAt = time.Now()
	log.Printf("[ha] standby promoted to active")
	return nil
}

// demote transitions a promoted standby back to passive.
// Returns an error if not promoted.
func (h *haState) demote() error {
	if h.role != RoleStandby {
		return fmt.Errorf("cannot demote: role is %q, not standby", h.role)
	}
	if !h.promoted.Load() {
		return fmt.Errorf("not promoted")
	}
	h.promoted.Store(false)
	h.promotedAt = time.Time{}
	log.Printf("[ha] standby demoted back to passive")
	return nil
}

// effectiveRole returns the role string for external reporting.
func (h *haState) effectiveRole() string {
	if h.role == RoleStandalone {
		return "standalone"
	}
	if h.role == RoleStandby && h.promoted.Load() {
		return "standby-promoted"
	}
	return h.role
}
