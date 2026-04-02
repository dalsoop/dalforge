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

const autoWakeIdleThreshold = 30 * time.Minute

func newTellCmd() *cobra.Command {
	var issueNum int
	var direct bool
	var member string
	var noWake bool

	cmd := &cobra.Command{
		Use:   "tell <team> <message>",
		Short: "Send a message to a team's dalcenter or matterbridge",
		Long: `Send a message to another dalcenter instance or directly to its matterbridge.

By default, messages are routed through the target dalcenter's /api/message endpoint.
Use --direct to send directly to the team's matterbridge API (bypassing dalcenter).
Use --issue to include a GitHub issue reference in the message.
Use --member with --issue to also trigger the issue-workflow pipeline on the target dalcenter.
Use --no-wake to disable auto-wake when the target dal is idle.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			team := args[0]
			message := strings.Join(args[1:], " ")

			if issueNum > 0 {
				message = fmt.Sprintf("[issue #%d] %s", issueNum, message)
			}

			// Auto-wake: check target dal idle time and restart if needed
			var wakeNote string
			if !noWake {
				targetURL, err := resolveRepoURL(team)
				if err != nil {
					return fmt.Errorf("resolve repo URL: %w", err)
				}
				idleDur, wakedName, err := autoWakeDal(targetURL, member)
				if err != nil {
					return fmt.Errorf("auto-wake %s: %w", team, err)
				}
				if idleDur > 0 {
					wakeNote = fmt.Sprintf("auto-waked %s, was idle %s", wakedName, formatIdleDuration(idleDur))
				}
			}

			if direct {
				return sendViaBridge(team, message, wakeNote)
			}

			if err := sendViaDalcenter(team, message, wakeNote); err != nil {
				return err
			}

			// Trigger issue-workflow if --issue is specified
			if issueNum > 0 {
				if err := triggerIssueWorkflow(team, issueNum, member, message); err != nil {
					return fmt.Errorf("issue-workflow trigger: %w", err)
				}
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&issueNum, "issue", 0, "Attach GitHub issue number to the message")
	cmd.Flags().BoolVar(&direct, "direct", false, "Send directly to matterbridge (bypass dalcenter)")
	cmd.Flags().StringVar(&member, "member", "", "Target member for issue-workflow (used with --issue)")
	cmd.Flags().BoolVar(&noWake, "no-wake", false, "Disable auto-wake for idle dals")

	return cmd
}

// sendViaDalcenter sends a message through the target dalcenter's /api/message endpoint.
func sendViaDalcenter(team, message, wakeNote string) error {
	targetURL, err := resolveRepoURL(team)
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
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	extra := ""
	if wakeNote != "" {
		extra = wakeNote + ", "
	}
	if result.PostID != "" {
		fmt.Printf("[tell] message sent to %s (%spost_id=%s)\n", team, extra, result.PostID)
	} else {
		fmt.Printf("[tell] message sent to %s (%sstatus=%s)\n", team, extra, result.Status)
	}
	return nil
}

// triggerIssueWorkflow calls the target dalcenter's /api/issue-workflow endpoint.
func triggerIssueWorkflow(team string, issueNum int, member, task string) error {
	targetURL, err := resolveRepoURL(team)
	if err != nil {
		return fmt.Errorf("resolve repo URL: %w", err)
	}

	payload := struct {
		IssueID string `json:"issue_id"`
		Member  string `json:"member"`
		Task    string `json:"task"`
	}{
		IssueID: fmt.Sprintf("%d", issueNum),
		Member:  member,
		Task:    task,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, targetURL+"/api/issue-workflow", strings.NewReader(string(body)))
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
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("issue-workflow failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var result struct {
		WorkflowID string `json:"workflow_id"`
		Status     string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Printf("[tell] issue-workflow triggered on %s (workflow_id=%s, status=%s)\n", team, result.WorkflowID, result.Status)
	return nil
}

// sendViaBridge sends a message directly to a team's matterbridge API.
func sendViaBridge(team, message, wakeNote string) error {
	bridgeURL, err := resolveBridgeURL(team)
	if err != nil {
		return fmt.Errorf("resolve bridge URL: %w", err)
	}

	gateway := resolveBridgeGateway(team)
	from := currentRepoName()

	payload := struct {
		Text     string `json:"text"`
		Username string `json:"username"`
		Gateway  string `json:"gateway"`
	}{
		Text:     message,
		Username: from,
		Gateway:  gateway,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, bridgeURL+"/api/message", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if token := os.Getenv("MATTERBRIDGE_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send to bridge: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bridge send failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	extra := ""
	if wakeNote != "" {
		extra = ", " + wakeNote
	}
	fmt.Printf("[tell] message sent to %s via bridge (gateway=%s%s)\n", team, gateway, extra)
	return nil
}

// resolveBridgeURL looks up the matterbridge URL for a team.
// Priority: 1) DALCENTER_BRIDGE_URLS env  2) <team>.env DALCENTER_BRIDGE_URL  3) default port
func resolveBridgeURL(team string) (string, error) {
	// 1. Explicit env var: DALCENTER_BRIDGE_URLS=team1=http://host:4242,team2=http://host:4243
	if urls := os.Getenv("DALCENTER_BRIDGE_URLS"); urls != "" {
		for _, entry := range strings.Split(urls, ",") {
			entry = strings.TrimSpace(entry)
			parts := strings.SplitN(entry, "=", 2)
			if len(parts) == 2 && parts[0] == team {
				return strings.TrimRight(parts[1], "/"), nil
			}
		}
	}

	// 2. Team env file: DALCENTER_BRIDGE_URL in /etc/dalcenter/<team>.env
	if url := readEnvVar(paths.ConfigDir(), team+".env", "DALCENTER_BRIDGE_URL"); url != "" {
		return strings.TrimRight(url, "/"), nil
	}

	// 3. Default: use DALCENTER_HOST_IP (or localhost) with default bridge port
	host := "localhost"
	if h := readEnvVar(paths.ConfigDir(), "common.env", "DALCENTER_HOST_IP"); h != "" {
		host = h
	}
	return "http://" + host + ":4242", nil
}

// resolveBridgeGateway returns the matterbridge gateway name for a team.
// Reads from <team>.env DALCENTER_BRIDGE_GATEWAY, defaults to "dal-team".
func resolveBridgeGateway(team string) string {
	if gw := readEnvVar(paths.ConfigDir(), team+".env", "DALCENTER_BRIDGE_GATEWAY"); gw != "" {
		return gw
	}
	return "dal-team"
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
			host := "localhost"
			if h := readEnvVar(paths.ConfigDir(), "common.env", "DALCENTER_HOST_IP"); h != "" {
				host = h
			}
			return "http://" + host + ":" + port, nil
		}
	}

	return "", fmt.Errorf("repo %q not found (no DALCENTER_URLS, no /etc/dalcenter/%s.env)", repo, repo)
}

// currentRepoName returns the current repository name from the working directory.
func currentRepoName() string {
	wd, _ := os.Getwd()
	return filepath.Base(wd)
}

// autoWakeDal queries /api/ps on the target dalcenter and performs sleep→wake
// (restart) if the target dal's idle time exceeds the threshold.
// If dalName is empty, the leader is checked. Returns the idle duration and
// the dal name if a wake was performed, or zero duration if no wake was needed.
func autoWakeDal(targetURL, dalName string) (time.Duration, string, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get(targetURL + "/api/ps")
	if err != nil {
		return 0, "", fmt.Errorf("query ps: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return 0, "", fmt.Errorf("ps failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var containers []struct {
		DalName string `json:"dal_name"`
		Role    string `json:"role"`
		IdleFor string `json:"idle_for"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return 0, "", fmt.Errorf("decode ps: %w", err)
	}

	// Find target dal by name, or fall back to leader role.
	var idleStr, targetName string
	for _, c := range containers {
		if dalName != "" && c.DalName == dalName {
			idleStr = c.IdleFor
			targetName = c.DalName
			break
		}
		if dalName == "" && c.Role == "leader" {
			idleStr = c.IdleFor
			targetName = c.DalName
			break
		}
	}

	if targetName == "" || idleStr == "" {
		return 0, "", nil
	}

	idle, err := time.ParseDuration(idleStr)
	if err != nil {
		return 0, "", nil
	}

	if idle < autoWakeIdleThreshold {
		return 0, "", nil
	}

	// Sleep then wake (restart) to ensure the dal is responsive.
	if err := postWithAuth(client, targetURL+"/api/sleep/"+targetName); err != nil {
		return 0, targetName, fmt.Errorf("sleep %s: %w", targetName, err)
	}
	if err := postWithAuth(client, targetURL+"/api/wake/"+targetName); err != nil {
		return 0, targetName, fmt.Errorf("wake %s: %w", targetName, err)
	}

	return idle, targetName, nil
}

// postWithAuth sends an authenticated POST request to the given URL.
func postWithAuth(client *http.Client, url string) error {
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("DALCENTER_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("(%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// formatIdleDuration formats a duration as a human-readable string like "3h35m".
func formatIdleDuration(d time.Duration) string {
	d = d.Truncate(time.Minute)
	if d == 0 {
		return "0m"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// readEnvVar reads a specific variable from an env file.
func readEnvVar(dir, file, key string) string {
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"=")
		}
	}
	return ""
}
