package daemon

import (
	"os"
	"testing"
)

func TestNewHAState_DefaultPrimary(t *testing.T) {
	os.Unsetenv("DALCENTER_ROLE")
	os.Unsetenv("DALCENTER_PEER_URL")
	defer os.Unsetenv("DALCENTER_ROLE")

	h := newHAState()
	if h.Role() != RolePrimary {
		t.Errorf("expected role %q, got %q", RolePrimary, h.Role())
	}
	if h.State() != StatePrimary {
		t.Errorf("expected state %q, got %q", StatePrimary, h.State())
	}
	if !h.IsPrimary() {
		t.Error("expected IsPrimary() == true")
	}
	if h.IsStandby() {
		t.Error("expected IsStandby() == false")
	}
}

func TestNewHAState_Standby(t *testing.T) {
	os.Setenv("DALCENTER_ROLE", "standby")
	os.Setenv("DALCENTER_PEER_URL", "http://primary:11190")
	defer os.Unsetenv("DALCENTER_ROLE")
	defer os.Unsetenv("DALCENTER_PEER_URL")

	h := newHAState()
	if h.Role() != RoleStandby {
		t.Errorf("expected role %q, got %q", RoleStandby, h.Role())
	}
	if h.State() != StateStandby {
		t.Errorf("expected state %q, got %q", StateStandby, h.State())
	}
	if h.IsPrimary() {
		t.Error("expected IsPrimary() == false")
	}
	if !h.IsStandby() {
		t.Error("expected IsStandby() == true")
	}
	if h.PeerURL() != "http://primary:11190" {
		t.Errorf("expected peer URL %q, got %q", "http://primary:11190", h.PeerURL())
	}
}

func TestNewHAState_InvalidRoleDefaultsPrimary(t *testing.T) {
	os.Setenv("DALCENTER_ROLE", "invalid")
	defer os.Unsetenv("DALCENTER_ROLE")

	h := newHAState()
	if h.Role() != RolePrimary {
		t.Errorf("expected role %q for invalid input, got %q", RolePrimary, h.Role())
	}
}

func TestHAState_Promote(t *testing.T) {
	os.Setenv("DALCENTER_ROLE", "standby")
	defer os.Unsetenv("DALCENTER_ROLE")

	h := newHAState()

	if err := h.Promote(); err != nil {
		t.Fatalf("Promote() failed: %v", err)
	}
	if h.State() != StatePromoted {
		t.Errorf("expected state %q after promote, got %q", StatePromoted, h.State())
	}
	if !h.IsPrimary() {
		t.Error("expected IsPrimary() == true after promote")
	}
	if h.IsStandby() {
		t.Error("expected IsStandby() == false after promote")
	}

	info := h.Info()
	if info["state"] != StatePromoted {
		t.Errorf("Info() state: expected %q, got %v", StatePromoted, info["state"])
	}
	if _, ok := info["promoted_at"]; !ok {
		t.Error("Info() should include promoted_at after promotion")
	}
}

func TestHAState_PromoteRejectsPrimary(t *testing.T) {
	os.Unsetenv("DALCENTER_ROLE")
	defer os.Unsetenv("DALCENTER_ROLE")

	h := newHAState()
	if err := h.Promote(); err == nil {
		t.Error("expected error promoting a primary instance")
	}
}

func TestHAState_Demote(t *testing.T) {
	os.Setenv("DALCENTER_ROLE", "standby")
	defer os.Unsetenv("DALCENTER_ROLE")

	h := newHAState()
	_ = h.Promote()

	if err := h.Demote(); err != nil {
		t.Fatalf("Demote() failed: %v", err)
	}
	if h.State() != StateStandby {
		t.Errorf("expected state %q after demote, got %q", StateStandby, h.State())
	}
	if h.IsPrimary() {
		t.Error("expected IsPrimary() == false after demote")
	}
}

func TestHAState_DemoteRejectsStandby(t *testing.T) {
	os.Setenv("DALCENTER_ROLE", "standby")
	defer os.Unsetenv("DALCENTER_ROLE")

	h := newHAState()
	if err := h.Demote(); err == nil {
		t.Error("expected error demoting a standby instance")
	}
}

func TestHAState_PromoteDemoteCycle(t *testing.T) {
	os.Setenv("DALCENTER_ROLE", "standby")
	defer os.Unsetenv("DALCENTER_ROLE")

	h := newHAState()

	// Standby → Promoted → Standby
	if err := h.Promote(); err != nil {
		t.Fatalf("first Promote() failed: %v", err)
	}
	if err := h.Demote(); err != nil {
		t.Fatalf("Demote() failed: %v", err)
	}
	if h.State() != StateStandby {
		t.Errorf("expected state %q, got %q", StateStandby, h.State())
	}

	// Can promote again
	if err := h.Promote(); err != nil {
		t.Fatalf("second Promote() failed: %v", err)
	}
	if h.State() != StatePromoted {
		t.Errorf("expected state %q, got %q", StatePromoted, h.State())
	}
}

func TestHAState_Info(t *testing.T) {
	os.Setenv("DALCENTER_ROLE", "standby")
	os.Setenv("DALCENTER_PEER_URL", "http://peer:11190")
	defer os.Unsetenv("DALCENTER_ROLE")
	defer os.Unsetenv("DALCENTER_PEER_URL")

	h := newHAState()
	info := h.Info()

	if info["role"] != RoleStandby {
		t.Errorf("Info() role: expected %q, got %v", RoleStandby, info["role"])
	}
	if info["state"] != StateStandby {
		t.Errorf("Info() state: expected %q, got %v", StateStandby, info["state"])
	}
	if info["peer_url"] != "http://peer:11190" {
		t.Errorf("Info() peer_url: expected %q, got %v", "http://peer:11190", info["peer_url"])
	}
	if _, ok := info["promoted_at"]; ok {
		t.Error("Info() should not include promoted_at before promotion")
	}
}
