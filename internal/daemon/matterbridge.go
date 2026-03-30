package daemon

import (
	"context"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// startMatterbridge starts matterbridge as a child process.
// Returns nil if binary not found or config not present (non-fatal).
func startMatterbridge(ctx context.Context, confPath string) (*exec.Cmd, error) {
	if confPath == "" {
		return nil, nil
	}
	if _, err := os.Stat(confPath); err != nil {
		log.Printf("[matterbridge] config not found: %s (skipping)", confPath)
		return nil, nil
	}

	bin, err := exec.LookPath("matterbridge")
	if err != nil {
		log.Printf("[matterbridge] binary not found (skipping)")
		return nil, nil
	}

	if matterbridgeAlreadyRunning() {
		log.Printf("[matterbridge] existing instance detected, skipping")
		return nil, nil
	}

	cmd := exec.CommandContext(ctx, bin, "-conf", confPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	time.Sleep(2 * time.Second)
	log.Printf("[matterbridge] started (pid=%d, conf=%s)", cmd.Process.Pid, confPath)

	return cmd, nil
}

func matterbridgeAlreadyRunning() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:" + DefaultBridgePort, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// parseBridgePort reads the API BindAddress port from a matterbridge TOML config.
func parseBridgePort(confPath string) string {
	data, err := os.ReadFile(confPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "BindAddress") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				port := strings.Trim(parts[len(parts)-1], "\" ")
				return port
			}
		}
	}
	return ""
}
