package runner

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"dalforge-hub/dalcenter/internal/state"
)

// Start launches a command in the background and records pid in state.json.
// workDir is the cwd for the process (typically repoRoot); state is written to instanceRoot.
func Start(instanceRoot, command, workDir string) (int, error) {
	hs, _ := state.Read(instanceRoot)
	if hs != nil && hs.Pid > 0 && IsAlive(hs.Pid) {
		return 0, fmt.Errorf("already running (pid %d)", hs.Pid)
	}

	cmd := exec.Command("sh", "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	} else {
		cmd.Dir = instanceRoot
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach from parent process group
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start: %w", err)
	}

	pid := cmd.Process.Pid
	// Release so dalcenter doesn't wait
	cmd.Process.Release()

	// Update state
	s := state.HealthState{
		Pid:       pid,
		RunStatus: "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	// Preserve existing health fields
	if hs != nil {
		s.Status = hs.Status
		s.HealthExit = hs.HealthExit
		s.HealthOutput = hs.HealthOutput
		s.CheckedAt = hs.CheckedAt
	}
	if err := state.Write(instanceRoot, s); err != nil {
		return pid, fmt.Errorf("write state: %w", err)
	}
	return pid, nil
}

// Stop kills the running process and updates state.
func Stop(instanceRoot string) error {
	hs, err := state.Read(instanceRoot)
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if hs.Pid == 0 {
		return fmt.Errorf("no running process")
	}
	if !IsAlive(hs.Pid) {
		// Already dead, just update state
		hs.Pid = 0
		hs.RunStatus = "stopped"
		return state.Write(instanceRoot, *hs)
	}

	proc, err := os.FindProcess(hs.Pid)
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Try SIGKILL as fallback
		proc.Kill()
	}

	hs.Pid = 0
	hs.RunStatus = "stopped"
	return state.Write(instanceRoot, *hs)
}

// IsAlive checks if a pid is still running via signal(0).
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// Check reads state and verifies pid liveness, correcting stale state.
func Check(instanceRoot string) (*state.HealthState, error) {
	hs, err := state.Read(instanceRoot)
	if err != nil {
		return nil, err
	}
	// Correct stale running state
	if hs.RunStatus == "running" && hs.Pid > 0 && !IsAlive(hs.Pid) {
		hs.RunStatus = "stopped"
		hs.Pid = 0
		state.Write(instanceRoot, *hs)
	}
	return hs, nil
}
