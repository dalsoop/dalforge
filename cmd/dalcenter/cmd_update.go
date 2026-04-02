package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dalsoop/dalcenter/internal/paths"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update dalcenter binary from GitHub release with graceful restart and rollback",
		Aliases: []string{"update"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSelfUpdate(force)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force update even if already at latest version")
	return cmd
}

func runSelfUpdate(force bool) error {
	// Step 1: Fetch latest release
	fmt.Println("[self-update] fetching latest release...")
	release, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}
	fmt.Printf("[self-update] latest release: %s\n", release.TagName)

	// Step 2: Resolve current binary path
	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return fmt.Errorf("resolve symlink: %w", err)
	}

	// Step 3: Version check
	if !force {
		currentVersion := getCurrentVersion(currentPath)
		if currentVersion != "" && currentVersion == release.TagName {
			fmt.Printf("[self-update] already at version %s (use --force to re-install)\n", currentVersion)
			return nil
		}
		if currentVersion != "" {
			fmt.Printf("[self-update] current: %s → target: %s\n", currentVersion, release.TagName)
		}
	}

	// Step 4: Find matching asset
	assetName := fmt.Sprintf("dalcenter-%s-%s", runtime.GOOS, runtime.GOARCH)
	var downloadURL string
	for _, a := range release.Assets {
		if a.Name == assetName {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no asset found matching %q in release %s", assetName, release.TagName)
	}

	// Step 5: Download new binary to temp file
	fmt.Printf("[self-update] downloading %s...\n", assetName)
	tmpFile, err := downloadAsset(downloadURL, filepath.Dir(currentPath))
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	defer os.Remove(tmpFile)

	if err := os.Chmod(tmpFile, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// Step 6: Discover all dalcenter systemd services
	services := discoverDalcenterServices()
	if len(services) == 0 {
		fmt.Println("[self-update] no systemd services found, replacing binary only")
		return replaceBinary(tmpFile, currentPath)
	}
	fmt.Printf("[self-update] found %d service(s): %s\n", len(services), strings.Join(services, ", "))

	// Step 7: Backup current binary
	backupPath := currentPath + ".bak"
	fmt.Printf("[self-update] backing up %s → %s\n", currentPath, backupPath)
	if err := copyFile(currentPath, backupPath); err != nil {
		return fmt.Errorf("backup binary: %w", err)
	}
	defer os.Remove(backupPath) // clean up backup on success

	// Step 8: Graceful stop all services
	fmt.Println("[self-update] stopping all services...")
	if err := stopServices(services); err != nil {
		return fmt.Errorf("stop services: %w", err)
	}

	// Step 9: Replace binary
	fmt.Println("[self-update] replacing binary...")
	if err := replaceBinary(tmpFile, currentPath); err != nil {
		fmt.Fprintf(os.Stderr, "[self-update] replace failed: %v\n", err)
		fmt.Println("[self-update] rolling back...")
		rollback(backupPath, currentPath, services)
		return fmt.Errorf("replace binary failed, rolled back: %w", err)
	}

	// Step 10: Restart all services
	fmt.Println("[self-update] starting all services...")
	if err := startServices(services); err != nil {
		fmt.Fprintf(os.Stderr, "[self-update] start failed: %v\n", err)
		fmt.Println("[self-update] rolling back...")
		rollback(backupPath, currentPath, services)
		return fmt.Errorf("restart failed, rolled back: %w", err)
	}

	// Step 11: Health check
	fmt.Println("[self-update] verifying services...")
	time.Sleep(3 * time.Second)
	if failed := checkServicesActive(services); len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "[self-update] services not active: %s\n", strings.Join(failed, ", "))
		fmt.Println("[self-update] rolling back...")
		rollback(backupPath, currentPath, services)
		return fmt.Errorf("health check failed for %s, rolled back", strings.Join(failed, ", "))
	}

	fmt.Printf("[self-update] updated to %s successfully\n", release.TagName)
	return nil
}

// discoverDalcenterServices finds all dalcenter systemd service instances.
// Discovers both template instances (dalcenter@<repo>) via env files
// and legacy non-template services (dalcenter, dalcenter-<name>).
func discoverDalcenterServices() []string {
	seen := make(map[string]bool)
	var services []string

	// Discover template instances from /etc/dalcenter/*.env
	configDir := paths.ConfigDir()
	if entries, err := filepath.Glob(filepath.Join(configDir, "*.env")); err == nil {
		for _, e := range entries {
			name := strings.TrimSuffix(filepath.Base(e), ".env")
			if name == "common" {
				continue
			}
			svc := "dalcenter@" + name
			if !seen[svc] {
				seen[svc] = true
				services = append(services, svc)
			}
		}
	}

	// Also discover legacy non-template service files
	if matches, err := filepath.Glob("/etc/systemd/system/dalcenter*.service"); err == nil {
		for _, m := range matches {
			base := filepath.Base(m)
			// Skip the template file itself
			if base == "dalcenter@.service" {
				continue
			}
			name := strings.TrimSuffix(base, ".service")
			// Skip if already discovered as template instance
			if !seen[name] {
				seen[name] = true
				services = append(services, name)
			}
		}
	}

	return services
}

// stopServices stops all services gracefully via systemctl.
func stopServices(services []string) error {
	for _, svc := range services {
		fmt.Printf("  stopping %s...\n", svc)
		cmd := exec.Command("systemctl", "stop", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("stop %s: %w", svc, err)
		}
	}
	return nil
}

// startServices starts all services via systemctl.
func startServices(services []string) error {
	for _, svc := range services {
		fmt.Printf("  starting %s...\n", svc)
		cmd := exec.Command("systemctl", "start", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("start %s: %w", svc, err)
		}
	}
	return nil
}

// checkServicesActive returns services that are not in "active" state.
func checkServicesActive(services []string) []string {
	var failed []string
	for _, svc := range services {
		out, err := exec.Command("systemctl", "is-active", svc).Output()
		if err != nil || strings.TrimSpace(string(out)) != "active" {
			failed = append(failed, svc)
		}
	}
	return failed
}

// rollback restores the backup binary and restarts services.
func rollback(backupPath, currentPath string, services []string) {
	// Stop any partially started services
	for _, svc := range services {
		exec.Command("systemctl", "stop", svc).Run()
	}

	// Restore backup
	if err := os.Rename(backupPath, currentPath); err != nil {
		fmt.Fprintf(os.Stderr, "[rollback] CRITICAL: cannot restore backup: %v\n", err)
		fmt.Fprintf(os.Stderr, "[rollback] backup is at: %s\n", backupPath)
		return
	}
	fmt.Println("[rollback] binary restored")

	// Restart with old binary
	for _, svc := range services {
		if err := exec.Command("systemctl", "start", svc).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "[rollback] warning: cannot start %s: %v\n", svc, err)
		} else {
			fmt.Printf("[rollback] restarted %s\n", svc)
		}
	}
}

// replaceBinary atomically replaces the target binary.
func replaceBinary(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		// Cross-device fallback: copy then remove
		if err := copyFile(src, dst); err != nil {
			return err
		}
		os.Remove(src)
	}
	return nil
}

// copyFile copies src to dst preserving permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatestRelease() (*ghRelease, error) {
	url := "https://api.github.com/repos/dalsoop/dalcenter/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &release, nil
}

func downloadAsset(url, dir string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("download returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	tmpFile, err := os.CreateTemp(dir, "dalcenter-update-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("write temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("close temp file: %w", err)
	}

	return tmpFile.Name(), nil
}

// getCurrentVersion runs the binary with "version" to get the current version string.
func getCurrentVersion(binaryPath string) string {
	out, err := exec.Command(binaryPath, "version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
