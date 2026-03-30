package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dalsoop/dalcenter/internal/paths"
	"github.com/spf13/cobra"
)

func newTellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tell <repo> <message>",
		Short: "Send a message to another dalcenter instance",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := args[0]
			message := strings.Join(args[1:], " ")

			targetURL, err := resolveRepoURL(repo)
			if err != nil {
				return fmt.Errorf("resolve repo URL: %w", err)
			}

			from := currentRepoName()

			body := fmt.Sprintf(`{"from":%q,"message":%q}`, from, message)
			req, err := http.NewRequest(http.MethodPost, targetURL+"/api/message", strings.NewReader(body))
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}
			req.Header.Set("Content-Type", "application/json")

			if token := os.Getenv("DALCENTER_TOKEN"); token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("send message: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				b, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("message failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
			}

			var result struct {
				PostID string `json:"post_id"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			fmt.Printf("[tell] message sent to %s (post_id=%s)\n", repo, result.PostID)
			return nil
		},
	}
}

// resolveRepoURL looks up the dalcenter URL for a given repo name.
// Priority: 1) DALCENTER_URLS env  2) auto-detect from /etc/dalcenter/<repo>.env
func resolveRepoURL(repo string) (string, error) {
	// 1. Explicit env var
	if urls := os.Getenv("DALCENTER_URLS"); urls != "" {
		for _, entry := range strings.Split(urls, ",") {
			entry = strings.TrimSpace(entry)
			parts := strings.SplitN(entry, "=", 2)
			if len(parts) == 2 && parts[0] == repo {
				return strings.TrimRight(parts[1], "/"), nil
			}
		}
	}

	// 2. Auto-detect from /etc/dalcenter/<repo>.env
	envFile := filepath.Join(paths.ConfigDir(), repo+".env")
	data, err := os.ReadFile(envFile)
	if err == nil {
		var port string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "DALCENTER_PORT=") {
				port = strings.TrimPrefix(line, "DALCENTER_PORT=")
				break
			}
		}
		if port != "" {
			return "http://localhost:" + port, nil
		}
	}

	return "", fmt.Errorf("repo %q not found (no DALCENTER_URLS, no /etc/dalcenter/%s.env)", repo, repo)
}

// currentRepoName returns the current repository name from the working directory.
func currentRepoName() string {
	wd, _ := os.Getwd()
	return filepath.Base(wd)
}
