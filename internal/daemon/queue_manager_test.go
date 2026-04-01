package daemon

import (
	"testing"
	"time"
)

func newTestDaemon() *Daemon {
	d := &Daemon{
		containers:  make(map[string]*Container),
		tasks:       newTaskStore(),
		escalations: newEscalationStore(),
	}
	d.queueManager = newQueueManager(d)
	return d
}

func TestEnqueue_FIFO(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	qm.Enqueue("", "task-1", PriorityNormal, "api", 0)
	qm.Enqueue("", "task-2", PriorityNormal, "api", 0)
	qm.Enqueue("", "task-3", PriorityNormal, "api", 0)

	items := qm.PendingItems()
	if len(items) != 3 {
		t.Fatalf("pending = %d, want 3", len(items))
	}
	if items[0].Task != "task-1" {
		t.Fatalf("first item = %q, want task-1", items[0].Task)
	}
	if items[2].Task != "task-3" {
		t.Fatalf("last item = %q, want task-3", items[2].Task)
	}
}

func TestEnqueue_HighPriorityFirst(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	qm.Enqueue("", "normal-1", PriorityNormal, "api", 0)
	qm.Enqueue("", "normal-2", PriorityNormal, "api", 0)
	qm.Enqueue("", "high-1", PriorityHigh, "api", 0)

	items := qm.PendingItems()
	if len(items) != 3 {
		t.Fatalf("pending = %d, want 3", len(items))
	}
	// High priority should be first
	if items[0].Task != "high-1" {
		t.Fatalf("first item = %q, want high-1", items[0].Task)
	}
	if items[1].Task != "normal-1" {
		t.Fatalf("second item = %q, want normal-1", items[1].Task)
	}
}

func TestEnqueue_MultipleHighPriority(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	qm.Enqueue("", "normal-1", PriorityNormal, "api", 0)
	qm.Enqueue("", "high-1", PriorityHigh, "api", 0)
	qm.Enqueue("", "high-2", PriorityHigh, "api", 0)

	items := qm.PendingItems()
	if items[0].Task != "high-1" {
		t.Fatalf("first = %q, want high-1", items[0].Task)
	}
	if items[1].Task != "high-2" {
		t.Fatalf("second = %q, want high-2", items[1].Task)
	}
	if items[2].Task != "normal-1" {
		t.Fatalf("third = %q, want normal-1", items[2].Task)
	}
}

func TestPendingCount(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	if qm.PendingCount() != 0 {
		t.Fatalf("initial count = %d, want 0", qm.PendingCount())
	}

	qm.Enqueue("", "task-1", PriorityNormal, "api", 0)
	if qm.PendingCount() != 1 {
		t.Fatalf("count = %d, want 1", qm.PendingCount())
	}
}

func TestCancelStuckTasks(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	// Create a task that started long ago with no events
	tr := d.tasks.New("leader", "old task")
	tr.StartedAt = time.Now().UTC().Add(-2 * stuckTaskTimeout)

	qm.cancelStuckTasks()

	updated := d.tasks.Get(tr.ID)
	if updated.Status != "failed" {
		t.Fatalf("status = %q, want failed", updated.Status)
	}
	if updated.Error == "" {
		t.Fatal("expected error message for stuck task")
	}
}

func TestCancelStuckTasks_RecentEventKeepsAlive(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	// Create a task that started long ago but has a recent event
	tr := d.tasks.New("leader", "active task")
	tr.StartedAt = time.Now().UTC().Add(-2 * stuckTaskTimeout)
	d.tasks.AddEvent(tr.ID, "progress", "still working")

	qm.cancelStuckTasks()

	updated := d.tasks.Get(tr.ID)
	if updated.Status != "running" {
		t.Fatalf("status = %q, want running (recent event should keep alive)", updated.Status)
	}
}

func TestResolveTarget_LeaderDefault(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager
	d.containers["leader"] = &Container{DalName: "leader", Role: "leader", Status: "running"}
	d.containers["dev"] = &Container{DalName: "dev", Role: "member", Status: "running"}

	item := &queueItem{Dal: "", Priority: PriorityNormal}
	target := qm.resolveTarget(item)
	if target == nil {
		t.Fatal("expected leader as target, got nil")
	}
	if target.DalName != "leader" {
		t.Fatalf("target = %q, want leader", target.DalName)
	}
}

func TestResolveTarget_SpecificDal(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager
	d.containers["dev"] = &Container{DalName: "dev", Role: "member", Status: "running"}

	item := &queueItem{Dal: "dev", Priority: PriorityNormal}
	target := qm.resolveTarget(item)
	if target == nil {
		t.Fatal("expected dev as target, got nil")
	}
	if target.DalName != "dev" {
		t.Fatalf("target = %q, want dev", target.DalName)
	}
}

func TestResolveTarget_SpecificDal_NotRunning(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager
	d.containers["dev"] = &Container{DalName: "dev", Role: "member", Status: "stopped"}

	item := &queueItem{Dal: "dev", Priority: PriorityNormal}
	target := qm.resolveTarget(item)
	if target != nil {
		t.Fatalf("expected nil for stopped container, got %q", target.DalName)
	}
}

func TestResolveTarget_LeaderBypass(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager
	d.containers["leader"] = &Container{DalName: "leader", Role: "leader", Status: "running"}
	d.containers["dev"] = &Container{DalName: "dev", Role: "member", Status: "running"}

	// Simulate leader having many running tasks
	for i := 0; i < leaderTaskThreshold; i++ {
		d.tasks.New("leader", "busy task")
	}

	item := &queueItem{Dal: "", Priority: PriorityNormal}
	target := qm.resolveTarget(item)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.DalName != "dev" {
		t.Fatalf("target = %q, want dev (leader bypass)", target.DalName)
	}
}

func TestResolveTarget_LeaderBypass_NoIdleMember(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager
	d.containers["leader"] = &Container{DalName: "leader", Role: "leader", Status: "running"}

	// Leader overloaded, no members available
	for i := 0; i < leaderTaskThreshold; i++ {
		d.tasks.New("leader", "busy task")
	}

	item := &queueItem{Dal: "", Priority: PriorityNormal}
	target := qm.resolveTarget(item)
	if target == nil {
		t.Fatal("expected leader as fallback, got nil")
	}
	if target.DalName != "leader" {
		t.Fatalf("target = %q, want leader (fallback when no members)", target.DalName)
	}
}

func TestResolveTarget_NoLeader(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager
	d.containers["dev"] = &Container{DalName: "dev", Role: "member", Status: "running"}

	item := &queueItem{Dal: "", Priority: PriorityNormal}
	target := qm.resolveTarget(item)
	if target != nil {
		t.Fatalf("expected nil when no leader, got %q", target.DalName)
	}
}

func TestRemoveItem(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	id1 := qm.Enqueue("", "task-1", PriorityNormal, "api", 0)
	qm.Enqueue("", "task-2", PriorityNormal, "api", 0)

	qm.mu.Lock()
	qm.removeItem(id1)
	qm.mu.Unlock()

	if qm.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1 after removal", qm.PendingCount())
	}
	items := qm.PendingItems()
	if items[0].Task != "task-2" {
		t.Fatalf("remaining item = %q, want task-2", items[0].Task)
	}
}

func TestCountRunningTasks(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	d.tasks.New("leader", "task-1")
	d.tasks.New("leader", "task-2")
	d.tasks.New("dev", "task-3")

	if count := qm.countRunningTasks("leader"); count != 2 {
		t.Fatalf("leader running = %d, want 2", count)
	}
	if count := qm.countRunningTasks("dev"); count != 1 {
		t.Fatalf("dev running = %d, want 1", count)
	}
	if count := qm.countRunningTasks("ghost"); count != 0 {
		t.Fatalf("ghost running = %d, want 0", count)
	}
}

func TestFindIdleMember(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	members := []*Container{
		{DalName: "dev1", Role: "member", Status: "running"},
		{DalName: "dev2", Role: "member", Status: "running"},
	}

	// dev1 has 2 tasks, dev2 has 0
	d.tasks.New("dev1", "task-1")
	d.tasks.New("dev1", "task-2")

	idle := qm.findIdleMember(members)
	if idle == nil {
		t.Fatal("expected idle member, got nil")
	}
	if idle.DalName != "dev2" {
		t.Fatalf("idle = %q, want dev2", idle.DalName)
	}
}

func TestStartStop(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	go qm.Start()
	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)
	qm.Stop()
	// If Stop blocks until the goroutine exits, this test passes
}

func TestEnqueue_ReturnsID(t *testing.T) {
	d := newTestDaemon()
	qm := d.queueManager

	id := qm.Enqueue("", "task", PriorityNormal, "api", 0)
	if id == "" {
		t.Fatal("expected non-empty queue ID")
	}
	if len(id) < 3 {
		t.Fatalf("queue ID too short: %q", id)
	}
}
