package serve

import (
	"testing"
	"time"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(DalEntry{Name: "dal-test", IP: "10.0.0.1", Port: 10200, Role: "tester"})

	entry, ok := r.Get("dal-test")
	if !ok {
		t.Fatal("expected dal-test to exist")
	}
	if entry.Status != "online" {
		t.Fatalf("expected online, got %q", entry.Status)
	}
	if entry.IP != "10.0.0.1" {
		t.Fatalf("expected IP 10.0.0.1, got %q", entry.IP)
	}
}

func TestRegistryDeregister(t *testing.T) {
	r := NewRegistry()
	r.Register(DalEntry{Name: "dal-test"})
	r.Deregister("dal-test")

	_, ok := r.Get("dal-test")
	if ok {
		t.Fatal("expected dal-test to not exist after deregister")
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	r.Register(DalEntry{Name: "a"})
	r.Register(DalEntry{Name: "b"})

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 dals, got %d", len(list))
	}
}

func TestRegistryOverwrite(t *testing.T) {
	r := NewRegistry()
	r.Register(DalEntry{Name: "dal-test", Role: "v1"})
	r.Register(DalEntry{Name: "dal-test", Role: "v2"})

	entry, _ := r.Get("dal-test")
	if entry.Role != "v2" {
		t.Fatalf("expected role v2, got %q", entry.Role)
	}
}

func TestDalEntryTimestamps(t *testing.T) {
	r := NewRegistry()
	before := time.Now()
	r.Register(DalEntry{Name: "dal-test"})
	after := time.Now()

	entry, _ := r.Get("dal-test")
	if entry.RegisteredAt.Before(before) || entry.RegisteredAt.After(after) {
		t.Fatalf("registered_at out of range: %v", entry.RegisteredAt)
	}
}
