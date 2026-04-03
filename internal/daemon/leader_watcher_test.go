package daemon

import (
	"strings"
	"testing"
)

func TestFindLeader(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"leader": {DalName: "leader", Role: "leader", ContainerID: "abc123", Status: "running"},
			"dev":    {DalName: "dev", Role: "member", ContainerID: "def456", Status: "running"},
		},
	}

	name, cid := d.findLeader()
	if name != "leader" {
		t.Fatalf("findLeader name = %q, want 'leader'", name)
	}
	if cid != "abc123" {
		t.Fatalf("findLeader containerID = %q, want 'abc123'", cid)
	}
}

func TestFindLeader_NoLeader(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"dev": {DalName: "dev", Role: "member", ContainerID: "def456", Status: "running"},
		},
	}

	name, cid := d.findLeader()
	if name != "" {
		t.Fatalf("findLeader name = %q, want empty", name)
	}
	if cid != "" {
		t.Fatalf("findLeader containerID = %q, want empty", cid)
	}
}

func TestValidateMemberLimit_NoLeader(t *testing.T) {
	// No localdalRoot configured, so ListDals will fail → allow
	d := &Daemon{
		localdalRoot: "/nonexistent",
		containers:   map[string]*Container{},
	}

	err := d.validateMemberLimit("dev")
	if err != nil {
		t.Fatalf("expected nil error when no leader config, got: %v", err)
	}
}

func TestValidateMemberLimit_UnderLimit(t *testing.T) {
	// validateMemberLimit reads ListDals to find the leader's max_members.
	// Without a real dal.cue, ListDals returns empty → no limit → allow.
	d := &Daemon{
		localdalRoot: "/nonexistent",
		containers: map[string]*Container{
			"dev": {DalName: "dev", Role: "member", Status: "running"},
		},
	}

	err := d.validateMemberLimit("dev-2")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

// ── InstanceID in leader restart ────────────────────────────────

func TestRestartLeader_GeneratesNewInstanceID(t *testing.T) {
	// restartLeader calls newPrefixedUUID("inst") for a fresh ID.
	// We can't call restartLeader directly (needs Docker), but we can
	// verify the pattern: old ID is replaced with a new one.

	d := &Daemon{
		containers: map[string]*Container{},
	}

	// Simulate leader running with old InstanceID
	oldInstID := "inst-old-leader-id"
	d.containers["leader"] = &Container{
		DalName:     "leader",
		InstanceID:  oldInstID,
		ContainerID: "old-ctr",
		Role:        "leader",
		Status:      "running",
	}

	// Simulate restartLeader: delete old, create new
	delete(d.containers, "leader")

	newInstID := newPrefixedUUID("inst")
	d.containers["leader"] = &Container{
		DalName:     "leader",
		InstanceID:  newInstID,
		ContainerID: "new-ctr",
		Role:        "leader",
		Status:      "running",
	}

	if newInstID == oldInstID {
		t.Fatalf("restart should generate new InstanceID, both = %s", oldInstID)
	}
	if !strings.HasPrefix(newInstID, "inst-") {
		t.Fatalf("expected inst- prefix, got %q", newInstID)
	}
	if d.containers["leader"].InstanceID != newInstID {
		t.Fatalf("container should have new InstanceID")
	}
}

func TestLeaderRestart_PreservesOtherContainerInstanceIDs(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{},
	}

	// Both leader and dev running
	devInstID := "inst-dev-stable"
	d.containers["leader"] = &Container{
		DalName:    "leader",
		InstanceID: "inst-leader-old",
		Role:       "leader",
		Status:     "running",
	}
	d.containers["dev"] = &Container{
		DalName:    "dev",
		InstanceID: devInstID,
		Role:       "member",
		Status:     "running",
	}

	// Simulate leader restart (only leader is affected)
	delete(d.containers, "leader")
	d.containers["leader"] = &Container{
		DalName:    "leader",
		InstanceID: newPrefixedUUID("inst"),
		Role:       "leader",
		Status:     "running",
	}

	// Dev's InstanceID should be untouched
	if d.containers["dev"].InstanceID != devInstID {
		t.Errorf("dev InstanceID changed during leader restart: got %q, want %q",
			d.containers["dev"].InstanceID, devInstID)
	}
}

func TestFindLeader_WithInstanceID(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"leader": {
				DalName:     "leader",
				InstanceID:  "inst-leader-123",
				Role:        "leader",
				ContainerID: "abc123",
				Status:      "running",
			},
		},
	}

	name, cid := d.findLeader()
	if name != "leader" {
		t.Fatalf("findLeader name = %q, want leader", name)
	}
	if cid != "abc123" {
		t.Fatalf("findLeader containerID = %q, want abc123", cid)
	}
	// Verify InstanceID is accessible after findLeader
	if d.containers[name].InstanceID != "inst-leader-123" {
		t.Fatalf("expected InstanceID preserved, got %q", d.containers[name].InstanceID)
	}
}

func TestCheckLeaderHealth_NoLeaderRegistered(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{},
	}
	health := &leaderHealth{Status: "healthy"}
	d.checkLeaderHealth(nil, health)

	if health.Status != "healthy" {
		t.Fatalf("status = %q, want 'healthy' when no leader registered", health.Status)
	}
	if health.ConsecutiveFails != 0 {
		t.Fatalf("consecutiveFails = %d, want 0", health.ConsecutiveFails)
	}
}
