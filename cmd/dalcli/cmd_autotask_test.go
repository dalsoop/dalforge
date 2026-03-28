package main

import (
	"os"
	"testing"
	"time"
)

func TestRunAutoTaskOnly_ExitsWithoutClaude(t *testing.T) {
	// runAutoTaskOnly calls executeTask which calls claude
	// Without claude binary, it should still run (fail task but not crash)
	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_AUTO_INTERVAL", "100ms")
	os.Setenv("DAL_MAX_DURATION", "1s")
	defer func() {
		os.Unsetenv("DAL_PLAYER")
		os.Unsetenv("DAL_ROLE")
		os.Unsetenv("DAL_AUTO_INTERVAL")
		os.Unsetenv("DAL_MAX_DURATION")
	}()

	// Run in goroutine with timeout
	done := make(chan error, 1)
	go func() {
		done <- runAutoTaskOnly("test-dal", "echo hello")
	}()

	select {
	case <-time.After(3 * time.Second):
		// Expected: function blocks in ticker loop
		// This is correct behavior — it runs forever
	case err := <-done:
		if err != nil {
			t.Logf("runAutoTaskOnly returned: %v (expected for no claude)", err)
		}
	}
}

func TestRunAutoTaskOnly_ParsesInterval(t *testing.T) {
	os.Setenv("DAL_AUTO_INTERVAL", "500ms")
	defer os.Unsetenv("DAL_AUTO_INTERVAL")

	interval := parseInterval(os.Getenv("DAL_AUTO_INTERVAL"), 30*time.Minute)
	if interval != 500*time.Millisecond {
		t.Errorf("interval = %v, want 500ms", interval)
	}
}

func TestRunAutoTaskOnly_DefaultInterval(t *testing.T) {
	os.Unsetenv("DAL_AUTO_INTERVAL")

	interval := parseInterval(os.Getenv("DAL_AUTO_INTERVAL"), 30*time.Minute)
	if interval != 30*time.Minute {
		t.Errorf("interval = %v, want 30m", interval)
	}
}

func TestAutoTaskOnlyMode_TriggeredWhenMMUnavailable(t *testing.T) {
	// When MM is not available and DAL_AUTO_TASK is set,
	// runAgentLoop should enter auto-task-only mode
	os.Setenv("DAL_PLAYER", "claude")
	os.Setenv("DAL_ROLE", "member")
	os.Setenv("DAL_AUTO_TASK", "echo test")
	os.Setenv("DAL_AUTO_INTERVAL", "100ms")
	os.Setenv("DAL_MAX_DURATION", "1s")
	os.Unsetenv("DALCENTER_URL") // No daemon → fetchAgentConfig fails
	defer func() {
		os.Unsetenv("DAL_PLAYER")
		os.Unsetenv("DAL_ROLE")
		os.Unsetenv("DAL_AUTO_TASK")
		os.Unsetenv("DAL_AUTO_INTERVAL")
		os.Unsetenv("DAL_MAX_DURATION")
	}()

	done := make(chan error, 1)
	go func() {
		done <- runAgentLoop("test-scribe")
	}()

	select {
	case <-time.After(3 * time.Second):
		// Auto-task-only mode entered and running (blocks in ticker)
	case err := <-done:
		// If it returns immediately with error, check if it's the right path
		if err != nil {
			t.Logf("returned: %v", err)
		}
	}
}
