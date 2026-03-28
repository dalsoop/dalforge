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

	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update dalcenter binary from GitHub release",
		RunE: func(cmd *cobra.Command, args []string) error {
			release, err := fetchLatestRelease()
			if err != nil {
				return fmt.Errorf("fetch latest release: %w", err)
			}

			currentPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("get executable path: %w", err)
			}
			currentPath, err = filepath.EvalSymlinks(currentPath)
			if err != nil {
				return fmt.Errorf("resolve symlink: %w", err)
			}

			if !force {
				currentVersion := getCurrentVersion(currentPath)
				if currentVersion != "" && currentVersion == release.TagName {
					fmt.Printf("[update] already at version %s (use --force to re-install)\n", currentVersion)
					return nil
				}
			}

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

			fmt.Printf("[update] downloading %s (%s)...\n", release.TagName, assetName)

			tmpFile, err := downloadAsset(downloadURL, filepath.Dir(currentPath))
			if err != nil {
				return fmt.Errorf("download asset: %w", err)
			}
			defer os.Remove(tmpFile) // clean up on failure

			if err := os.Chmod(tmpFile, 0755); err != nil {
				return fmt.Errorf("chmod: %w", err)
			}

			if err := os.Rename(tmpFile, currentPath); err != nil {
				return fmt.Errorf("replace binary: %w", err)
			}

			fmt.Printf("[update] binary replaced: %s\n", currentPath)

			fmt.Println("[update] restarting dalcenter via systemctl...")
			restartCmd := exec.Command("systemctl", "restart", "dalcenter")
			restartCmd.Stdout = os.Stdout
			restartCmd.Stderr = os.Stderr
			if err := restartCmd.Run(); err != nil {
				return fmt.Errorf("systemctl restart: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force update even if already at latest version")
	return cmd
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

// getCurrentVersion attempts to get the version by running the current binary with --version.
// Returns empty string if it fails.
func getCurrentVersion(binaryPath string) string {
	out, err := exec.Command(binaryPath, "version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
