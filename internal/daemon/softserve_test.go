package daemon

import (
	"net"
	"os"
	"testing"
	"time"
)

func TestSoftServeSSHPort_UsesEnvOverride(t *testing.T) {
	t.Setenv("SOFT_SERVE_SSH_PORT", "24567")
	if got := softServeSSHPort(); got != "24567" {
		t.Fatalf("softServeSSHPort() = %q, want %q", got, "24567")
	}
}

func TestSoftServeAlreadyRunning_DetectsListener(t *testing.T) {
	t.Setenv("SOFT_SERVE_SSH_PORT", "24568")

	ln, err := net.Listen("tcp", "127.0.0.1:24568")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	if !softServeAlreadyRunning(200 * time.Millisecond) {
		t.Fatal("softServeAlreadyRunning() = false, want true")
	}
}

func TestSoftServeAlreadyRunning_ReturnsFalseWithoutListener(t *testing.T) {
	port := "24569"
	t.Setenv("SOFT_SERVE_SSH_PORT", port)

	// Ensure the test port is currently free before checking.
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	ln.Close()

	if softServeAlreadyRunning(200 * time.Millisecond) {
		t.Fatal("softServeAlreadyRunning() = true, want false")
	}
}

func TestStartSoftServe_SkipsWhenPortAlreadyInUse(t *testing.T) {
	port := "24570"
	t.Setenv("SOFT_SERVE_SSH_PORT", port)

	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	cmd, err := startSoftServe(t.Context())
	if err != nil {
		t.Fatalf("startSoftServe() error = %v", err)
	}
	if cmd != nil {
		t.Fatal("startSoftServe() should skip child start when port is already in use")
	}
}

func TestSoftServeDataPath_DefaultsUnderHome(t *testing.T) {
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() {
		if oldHome == "" {
			_ = os.Unsetenv("HOME")
			return
		}
		_ = os.Setenv("HOME", oldHome)
	})

	tmp := t.TempDir()
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("set HOME: %v", err)
	}

	got := softServeDataPath()
	want := tmp + "/.dalcenter/soft-serve"
	if got != want {
		t.Fatalf("softServeDataPath() = %q, want %q", got, want)
	}
}
