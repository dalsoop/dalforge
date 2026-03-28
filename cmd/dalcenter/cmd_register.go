package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/dalsoop/dalcenter/internal/localdal"
	"github.com/dalsoop/dalcenter/internal/talk"
	"github.com/spf13/cobra"
)

func newRegisterCmd() *cobra.Command {
	var (
		mmURL   string
		mmToken string
		mmTeam  string
		port    int
	)
	cmd := &cobra.Command{
		Use:   "register <repo-path>",
		Short: "Register a repository: init .dal/, systemd, MM channel, tokens, port, soft-serve",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			if _, err := os.Stat(repoPath); err != nil {
				return fmt.Errorf("repo path not found: %w", err)
			}

			repoName := filepath.Base(repoPath)

			// Step 1: .dal/ 초기화
			dalRoot := filepath.Join(repoPath, ".dal")
			if err := localdal.Init(dalRoot); err != nil {
				return fmt.Errorf("init .dal/: %w", err)
			}
			fmt.Printf("[1/6] .dal/ initialized: %s\n", dalRoot)

			// Step 2: systemd 서비스 등록
			if port == 0 {
				port = nextAvailablePort()
			}
			addr := fmt.Sprintf(":%d", port)
			serviceName := systemdServiceName(repoName)
			if err := installSystemdService(serviceName, repoPath, addr, mmURL, mmToken, mmTeam); err != nil {
				return fmt.Errorf("systemd: %w", err)
			}
			fmt.Printf("[2/6] systemd service: %s (port %d)\n", serviceName, port)

			// Step 3: MM 채널 설정
			if mmURL != "" && mmToken != "" && mmTeam != "" {
				teamID, channelID, err := talk.GetTeamAndChannel(mmURL, mmToken, mmTeam, repoName)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[3/6] warning: MM channel setup: %v\n", err)
				} else {
					fmt.Printf("[3/6] MM channel: %s (team=%s channel=%s)\n", repoName, teamID[:8], channelID[:8])
				}
			} else {
				fmt.Println("[3/6] MM channel: skipped (no MM config)")
			}

			// Step 4: 토큰 주입
			if err := injectTokensToService(serviceName, repoPath); err != nil {
				fmt.Fprintf(os.Stderr, "[4/6] warning: token injection: %v\n", err)
			} else {
				fmt.Println("[4/6] tokens injected")
			}

			// Step 5: 포트 할당 확인
			fmt.Printf("[5/6] port allocated: %d (DALCENTER_URL=http://localhost:%d)\n", port, port)

			// Step 6: soft-serve subtree 연결
			ssRepoName := repoName + "-localdal"
			if err := daemon.EnsureSoftServeRepo(ssRepoName); err != nil {
				fmt.Fprintf(os.Stderr, "[6/6] warning: soft-serve repo: %v\n", err)
			} else if err := daemon.SetupSubtree(repoPath, ssRepoName); err != nil {
				fmt.Fprintf(os.Stderr, "[6/6] warning: subtree: %v\n", err)
			} else {
				fmt.Printf("[6/6] soft-serve subtree: %s/.dal → %s\n", repoPath, ssRepoName)
			}

			fmt.Printf("\nregistered: %s\n", repoName)
			fmt.Printf("  start: systemctl start %s\n", serviceName)
			fmt.Printf("  url:   http://localhost:%d\n", port)
			return nil
		},
	}
	cmd.Flags().StringVar(&mmURL, "mm-url", os.Getenv("DALCENTER_MM_URL"), "Mattermost URL")
	cmd.Flags().StringVar(&mmToken, "mm-token", os.Getenv("DALCENTER_MM_TOKEN"), "Mattermost admin token")
	cmd.Flags().StringVar(&mmTeam, "mm-team", os.Getenv("DALCENTER_MM_TEAM"), "Mattermost team name")
	cmd.Flags().IntVar(&port, "port", 0, "Listen port (default: auto-assign next available)")
	return cmd
}

// systemdServiceName returns the systemd service name for a project.
// First project uses "dalcenter", others use "dalcenter-<name>".
func systemdServiceName(repoName string) string {
	base := "/etc/systemd/system/dalcenter.service"
	if _, err := os.Stat(base); err != nil {
		return "dalcenter"
	}
	return "dalcenter-" + repoName
}

// nextAvailablePort scans existing dalcenter systemd services to find the next port.
// Base port is 11190, increments sequentially.
func nextAvailablePort() int {
	const basePort = 11190
	used := make(map[int]bool)

	entries, err := filepath.Glob("/etc/systemd/system/dalcenter*.service")
	if err != nil || len(entries) == 0 {
		return basePort
	}

	for _, entry := range entries {
		data, err := os.ReadFile(entry)
		if err != nil {
			continue
		}
		// Parse --addr :<port> from ExecStart line
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "ExecStart=") {
				continue
			}
			idx := strings.Index(line, "--addr :")
			if idx < 0 {
				continue
			}
			portStr := line[idx+len("--addr :"):]
			if sp := strings.IndexByte(portStr, ' '); sp > 0 {
				portStr = portStr[:sp]
			}
			if p, err := strconv.Atoi(portStr); err == nil {
				used[p] = true
			}
		}
	}

	for p := basePort; ; p++ {
		if !used[p] {
			return p
		}
	}
}

// installSystemdService writes and enables a dalcenter systemd service file.
func installSystemdService(serviceName, repoPath, addr, mmURL, mmToken, mmTeam string) error {
	repoName := filepath.Base(repoPath)
	unitPath := filepath.Join("/etc/systemd/system", serviceName+".service")

	// Build ExecStart
	execStart := fmt.Sprintf("/usr/local/bin/dalcenter serve --addr %s --repo %s", addr, repoPath)
	if mmURL != "" {
		execStart += fmt.Sprintf(" --mm-url %s", mmURL)
	}
	if mmToken != "" {
		execStart += fmt.Sprintf(" --mm-token %s", mmToken)
	}
	if mmTeam != "" {
		execStart += fmt.Sprintf(" --mm-team %s", mmTeam)
	}

	unit := fmt.Sprintf(`[Unit]
Description=DalCenter Daemon — %s
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
Environment=PATH=/usr/local/bin:/usr/local/go/bin:/root/go/bin:/usr/bin:/bin
Environment=HOME=/root
Environment=DALCENTER_LOCALDAL_PATH=%s/.dal
ExecStart=%s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, repoName, repoPath, execStart)

	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("write %s: %w", unitPath, err)
	}

	// systemctl daemon-reload && enable
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload: %s: %w", string(out), err)
	}
	if out, err := exec.Command("systemctl", "enable", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("enable %s: %s: %w", serviceName, string(out), err)
	}

	return nil
}

// injectTokensToService writes token environment overrides for the systemd service.
// Uses systemd drop-in to avoid modifying the main unit file.
func injectTokensToService(serviceName, repoPath string) error {
	dropInDir := filepath.Join("/etc/systemd/system", serviceName+".service.d")
	if err := os.MkdirAll(dropInDir, 0755); err != nil {
		return fmt.Errorf("create drop-in dir: %w", err)
	}

	var envLines []string

	// GITHUB_TOKEN: from host environment or resolve from env
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		envLines = append(envLines, fmt.Sprintf("Environment=GITHUB_TOKEN=%s", token))
	}

	// DALCENTER_TOKEN: generate or reuse
	if token := os.Getenv("DALCENTER_TOKEN"); token != "" {
		envLines = append(envLines, fmt.Sprintf("Environment=DALCENTER_TOKEN=%s", token))
	}

	// VEILKEY_LOCALVAULT_URL: pass through if available
	if url := os.Getenv("VEILKEY_LOCALVAULT_URL"); url != "" {
		envLines = append(envLines, fmt.Sprintf("Environment=VEILKEY_LOCALVAULT_URL=%s", url))
	}

	if len(envLines) == 0 {
		return nil // nothing to inject
	}

	content := "[Service]\n" + strings.Join(envLines, "\n") + "\n"
	dropInPath := filepath.Join(dropInDir, "tokens.conf")
	if err := os.WriteFile(dropInPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("write tokens.conf: %w", err)
	}

	// Reload after drop-in
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload: %s: %w", string(out), err)
	}

	return nil
}
