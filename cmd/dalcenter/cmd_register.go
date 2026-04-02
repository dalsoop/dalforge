package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/dalsoop/dalcenter/internal/localdal"
	"github.com/dalsoop/dalcenter/internal/paths"
	"github.com/spf13/cobra"
)

func newRegisterCmd() *cobra.Command {
	var (
		bridgeURL string
		port      int
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
			serviceName := systemdInstanceName(repoName)
			if err := installSystemdService(serviceName, repoName, repoPath, port, bridgeURL); err != nil {
				return fmt.Errorf("systemd: %w", err)
			}
			fmt.Printf("[2/6] systemd service: %s (port %d)\n", serviceName, port)

			// Step 3: bridge URL 확인
			fmt.Printf("[3/6] bridge URL: %s\n", bridgeURL)

			// Step 4: 토큰 주입
			if err := injectTokensToService(repoName); err != nil {
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
	cmd.Flags().StringVar(&bridgeURL, "bridge-url", envOrDefault("DALCENTER_BRIDGE_URL", daemon.DefaultBridgeURL), "Matterbridge API URL")
	cmd.Flags().IntVar(&port, "port", 0, "Listen port (default: auto-assign next available)")
	return cmd
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// systemdInstanceName returns the systemd template instance name: dalcenter@<repo>.
func systemdInstanceName(repoName string) string {
	return "dalcenter@" + repoName
}

// nextAvailablePort scans /etc/dalcenter/*.env files to find the next port.
// Base port is 11190, increments sequentially.
func nextAvailablePort() int {
	const basePort = 11190
	used := make(map[int]bool)

	// Scan env files in config dir
	configDir := paths.ConfigDir()
	entries, err := filepath.Glob(filepath.Join(configDir, "*.env"))
	if err == nil {
		for _, entry := range entries {
			if filepath.Base(entry) == "common.env" {
				continue
			}
			data, err := os.ReadFile(entry)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "DALCENTER_PORT=") {
					portStr := strings.TrimPrefix(line, "DALCENTER_PORT=")
					if p, err := strconv.Atoi(strings.TrimSpace(portStr)); err == nil {
						used[p] = true
					}
				}
			}
		}
	}

	// Also scan legacy service files for backward compat
	legacyEntries, err := filepath.Glob("/etc/systemd/system/dalcenter*.service")
	if err == nil {
		for _, entry := range legacyEntries {
			// Skip the template file itself
			if filepath.Base(entry) == "dalcenter@.service" {
				continue
			}
			data, err := os.ReadFile(entry)
			if err != nil {
				continue
			}
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
	}

	for p := basePort; ; p++ {
		if !used[p] {
			return p
		}
	}
}

// installSystemdService installs the dalcenter@ template (if needed),
// writes /etc/dalcenter/<repo>.env, and enables the instance.
func installSystemdService(serviceName, repoName, repoPath string, port int, bridgeURL string) error {
	// Ensure template unit exists
	if err := installTemplateUnit(); err != nil {
		return fmt.Errorf("install template: %w", err)
	}

	// Write env file to /etc/dalcenter/<repo>.env
	configDir := paths.ConfigDir()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	envContent := fmt.Sprintf("DALCENTER_PORT=%d\nDALCENTER_REPO=%s\nDALCENTER_LOCALDAL_PATH=%s/.dal\nDALCENTER_BRIDGE_URL=%s\n",
		port, repoPath, repoPath, bridgeURL)

	envPath := filepath.Join(configDir, repoName+".env")
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		return fmt.Errorf("write %s: %w", envPath, err)
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

//go:embed dalcenter@.service
var templateUnitContent string

// installTemplateUnit writes the dalcenter@.service template to /etc/systemd/system/
// if it does not already exist or if the embedded version is newer.
func installTemplateUnit() error {
	templatePath := "/etc/systemd/system/dalcenter@.service"

	existing, err := os.ReadFile(templatePath)
	if err == nil && string(existing) == templateUnitContent {
		return nil // already up to date
	}

	if err := os.WriteFile(templatePath, []byte(templateUnitContent), 0644); err != nil {
		return fmt.Errorf("write template: %w", err)
	}
	return nil
}

func newUnregisterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unregister <repo-path>",
		Short: "Unregister a repository: sleep all dals, remove systemd service",
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

			// Step 1: sleep all dals
			fmt.Println("[1/2] sleeping all dals...")
			client, err := daemon.NewClient()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[1/2] warning: cannot connect to daemon: %v\n", err)
			} else {
				containers, err := client.Ps()
				if err != nil {
					fmt.Fprintf(os.Stderr, "[1/2] warning: list containers: %v\n", err)
				} else {
					for _, c := range containers {
						if _, err := client.Sleep(c.DalName); err != nil {
							fmt.Fprintf(os.Stderr, "  sleep %s: %v\n", c.DalName, err)
							continue
						}
						fmt.Printf("  sleep: %s\n", c.DalName)
					}
				}
			}
			fmt.Println("[1/2] done")

			// Step 2: remove systemd service
			fmt.Println("[2/2] removing systemd service...")
			serviceName := systemdInstanceName(repoName)
			exec.Command("systemctl", "stop", serviceName).Run()
			exec.Command("systemctl", "disable", serviceName).Run()

			// Remove env file
			envPath := filepath.Join(paths.ConfigDir(), repoName+".env")
			os.Remove(envPath)

			// Remove legacy non-template service files if present
			for _, legacy := range []string{
				filepath.Join("/etc/systemd/system", "dalcenter-"+repoName+".service"),
				filepath.Join("/etc/systemd/system", "dalcenter-"+repoName+".service.d"),
			} {
				os.RemoveAll(legacy)
			}

			exec.Command("systemctl", "daemon-reload").Run()
			fmt.Printf("[2/2] removed: %s\n", serviceName)

			fmt.Printf("\nunregistered: %s\n", repoName)
			return nil
		},
	}
	return cmd
}

// injectTokensToService appends token environment variables to the env file.
func injectTokensToService(repoName string) error {
	envPath := filepath.Join(paths.ConfigDir(), repoName+".env")

	var tokenLines []string
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		tokenLines = append(tokenLines, fmt.Sprintf("GITHUB_TOKEN=%s", token))
	}
	if token := os.Getenv("DALCENTER_TOKEN"); token != "" {
		tokenLines = append(tokenLines, fmt.Sprintf("DALCENTER_TOKEN=%s", token))
	}
	if url := os.Getenv("VEILKEY_LOCALVAULT_URL"); url != "" {
		tokenLines = append(tokenLines, fmt.Sprintf("VEILKEY_LOCALVAULT_URL=%s", url))
	}

	if len(tokenLines) == 0 {
		return nil
	}

	f, err := os.OpenFile(envPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open %s: %w", envPath, err)
	}
	defer f.Close()

	content := strings.Join(tokenLines, "\n") + "\n"
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("write tokens: %w", err)
	}

	// Restrict permissions since file now contains tokens
	if err := os.Chmod(envPath, 0600); err != nil {
		return fmt.Errorf("chmod %s: %w", envPath, err)
	}

	return nil
}
