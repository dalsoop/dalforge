package daemon

import (
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
