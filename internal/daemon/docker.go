package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dalsoop/dalcenter/internal/localdal"
)

// isCredentialExpired checks if a player's credential file has expired.
// Supports Claude (.credentials.json) and Codex (auth.json).
func isCredentialExpired(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	// Claude: {"claudeAiOauth":{"expiresAt":1234567890123}}
	var claude struct {
		ClaudeAiOauth struct {
			ExpiresAt int64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &claude) == nil && claude.ClaudeAiOauth.ExpiresAt > 0 {
		return time.Now().UnixMilli() > claude.ClaudeAiOauth.ExpiresAt, nil
	}
	// Codex: {"tokens":{"expires_at":"2026-03-20T..."}, "last_refresh":"..."}
	var codex struct {
		Tokens struct {
			ExpiresAt string `json:"expires_at"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &codex) == nil && codex.Tokens.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, codex.Tokens.ExpiresAt); err == nil {
			return time.Now().After(t), nil
		}
	}
	return false, nil
}

// instructionsFileName returns the target filename based on player.
func instructionsFileName(player string) string {
	switch player {
	case "claude":
		return "CLAUDE.md"
	case "codex":
		return "AGENTS.md"
	case "gemini":
		return "GEMINI.md"
	default:
		return "AGENTS.md"
	}
}

// playerHome returns the config home path inside the container.
func playerHome(player string) string {
	switch player {
	case "claude":
		return "/root/.claude"
	case "codex":
		return "/root/.codex"
	case "gemini":
		return "/root/.gemini"
	default:
		return "/root/.config"
	}
}

// dockerRun creates and starts a Docker container for a dal.
// It returns the container ID, any credential warnings, and an error.
func dockerRun(localdalRoot, serviceRepo, instanceName, daemonAddr string, dal *localdal.DalProfile) (string, []string, error) {
	var warnings []string
	containerName := fmt.Sprintf("dal-%s", instanceName)
	tag := "latest"
	if dal.PlayerVersion != "" {
		tag = dal.PlayerVersion
	}
	image := fmt.Sprintf("dalcenter/%s:%s", dal.Player, tag)

	dalDir := filepath.Join(localdalRoot, dal.FolderName)
	home := playerHome(dal.Player)
	hostHome, _ := os.UserHomeDir()

	args := []string{
		"run", "-d",
		"--name", containerName,
		"--hostname", dal.Name,
		// Environment
		"-e", fmt.Sprintf("DAL_NAME=%s", dal.Name),
		"-e", fmt.Sprintf("DAL_UUID=%s", dal.UUID),
		"-e", fmt.Sprintf("DAL_ROLE=%s", dal.Role),
		"-e", fmt.Sprintf("DAL_PLAYER=%s", dal.Player),
		"-e", fmt.Sprintf("DALCENTER_URL=http://host.docker.internal%s", daemonAddr),
		// VeilKey — pass through if available
		"-e", fmt.Sprintf("VEILKEY_LOCALVAULT_URL=%s", os.Getenv("VEILKEY_LOCALVAULT_URL")),
		// Mount dal directory (read-only)
		"-v", fmt.Sprintf("%s:%s:ro", dalDir, "/dal"),
		// Working directory
		"-w", "/workspace",
	}

	// Mount service repo as /workspace
	if serviceRepo != "" {
		args = append(args, "-v", fmt.Sprintf("%s:/workspace", serviceRepo))
	}

	// Mount credentials (player-specific)
	switch dal.Player {
	case "claude":
		credPath := filepath.Join(hostHome, ".claude", ".credentials.json")
		if _, err := os.Stat(credPath); err == nil {
			args = append(args, "-v", fmt.Sprintf("%s:%s/.credentials.json:ro", credPath, home))
			if expired, _ := isCredentialExpired(credPath); expired {
				w := "Claude credential expired — run: pve-sync-creds"
				log.Printf("WARNING: %s", w)
				warnings = append(warnings, w)
			}
		} else {
			w := fmt.Sprintf("Claude credential not found at %s", credPath)
			log.Printf("WARNING: %s", w)
			warnings = append(warnings, w)
		}
	case "codex":
		credPath := filepath.Join(hostHome, ".codex", "auth.json")
		if _, err := os.Stat(credPath); err == nil {
			args = append(args, "-v", fmt.Sprintf("%s:%s/auth.json:ro", credPath, home))
			if expired, _ := isCredentialExpired(credPath); expired {
				w := "Codex credential expired — run: pve-sync-creds"
				log.Printf("WARNING: %s", w)
				warnings = append(warnings, w)
			}
		} else {
			w := fmt.Sprintf("Codex credential not found at %s", credPath)
			log.Printf("WARNING: %s", w)
			warnings = append(warnings, w)
		}
	case "gemini":
		// Gemini uses API key via environment variable
		if key := os.Getenv("GEMINI_API_KEY"); key != "" {
			args = append(args, "-e", fmt.Sprintf("GEMINI_API_KEY=%s", key))
		} else {
			w := "GEMINI_API_KEY not set for gemini dal"
			log.Printf("WARNING: %s", w)
			warnings = append(warnings, w)
		}
	}

	// Mount skills
	for _, skill := range dal.Skills {
		skillPath := filepath.Join(localdalRoot, skill)
		targetPath := filepath.Join(home, "skills", filepath.Base(skill))
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", skillPath, targetPath))
	}

	// Mount instructions.md as the right filename
	instrSrc := filepath.Join(dalDir, "instructions.md")
	instrDst := filepath.Join(home, instructionsFileName(dal.Player))
	args = append(args, "-v", fmt.Sprintf("%s:%s:ro", instrSrc, instrDst))

	// Git config from dal.cue or defaults
	gitUser := dal.GitUser
	if gitUser == "" {
		gitUser = "dal-" + dal.Name
	}
	gitEmail := dal.GitEmail
	if gitEmail == "" {
		gitEmail = fmt.Sprintf("dal-%s@dalcenter.local", dal.Name)
	}
	args = append(args, "-e", fmt.Sprintf("GIT_AUTHOR_NAME=%s", gitUser))
	args = append(args, "-e", fmt.Sprintf("GIT_AUTHOR_EMAIL=%s", gitEmail))
	args = append(args, "-e", fmt.Sprintf("GIT_COMMITTER_NAME=%s", gitUser))
	args = append(args, "-e", fmt.Sprintf("GIT_COMMITTER_EMAIL=%s", gitEmail))

	// GitHub token for git push + gh CLI
	if dal.GitHubToken != "" {
		token := dal.GitHubToken
		// Resolve references
		if strings.HasPrefix(token, "VK:") {
			resolved, err := resolveVeilKey(token)
			if err != nil {
				log.Printf("[docker] warning: failed to resolve %s: %v", token, err)
			} else {
				token = resolved
			}
		} else if strings.HasPrefix(token, "env:") {
			envName := strings.TrimPrefix(token, "env:")
			token = os.Getenv(envName)
		}
		if token != "" && !strings.HasPrefix(token, "VK:") && !strings.HasPrefix(token, "env:") {
			args = append(args, "-e", fmt.Sprintf("GITHUB_TOKEN=%s", token))
			args = append(args, "-e", fmt.Sprintf("GH_TOKEN=%s", token))
		}
	}

	args = append(args, image)

	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run: %s: %w", strings.TrimSpace(string(out)), err)
	}
	containerID := strings.TrimSpace(string(out))

	// Inject dalcli / dalcli-leader based on role
	if err := injectCli(containerID, dal.Role); err != nil {
		log.Printf("[docker] warning: failed to inject dalcli: %v", err)
	}

	return containerID, warnings, nil
}

// resolveVeilKey resolves a VK: reference via veil CLI or localvault API.
func resolveVeilKey(ref string) (string, error) {
	// Try veil CLI first
	if path, err := exec.LookPath("veil"); err == nil {
		cmd := exec.Command(path, "resolve", ref)
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}

	// Try veilkey-cli
	if path, err := exec.LookPath("veilkey-cli"); err == nil {
		cmd := exec.Command(path, "resolve", ref)
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}

	// Fallback: localvault HTTP API
	lvURL := os.Getenv("VEILKEY_LOCALVAULT_URL")
	if lvURL == "" {
		return "", fmt.Errorf("no veil CLI or VEILKEY_LOCALVAULT_URL to resolve %s", ref)
	}
	resp, err := http.Get(lvURL + "/api/resolve/" + ref)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("resolve %s: %s", ref, string(body))
	}
	return strings.TrimSpace(string(body)), nil
}

// injectCli copies dalcli or dalcli-leader binary into the container.
func injectCli(containerID, role string) error {
	// Find binaries next to dalcenter binary
	self, err := os.Executable()
	if err != nil {
		return err
	}
	binDir := filepath.Dir(self)

	// Always inject dalcli
	dalcliPath := filepath.Join(binDir, "dalcli")
	if _, err := os.Stat(dalcliPath); err == nil {
		cp := exec.Command("docker", "cp", dalcliPath, containerID+":/usr/local/bin/dalcli")
		if out, err := cp.CombinedOutput(); err != nil {
			return fmt.Errorf("inject dalcli: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	// Inject dalcli-leader for leader role
	if role == "leader" {
		leaderPath := filepath.Join(binDir, "dalcli-leader")
		if _, err := os.Stat(leaderPath); err == nil {
			cp := exec.Command("docker", "cp", leaderPath, containerID+":/usr/local/bin/dalcli-leader")
			if out, err := cp.CombinedOutput(); err != nil {
				return fmt.Errorf("inject dalcli-leader: %s: %w", strings.TrimSpace(string(out)), err)
			}
		}
	}

	return nil
}

// dockerStop stops and removes a Docker container.
func dockerStop(containerID string) error {
	// Stop
	stop := exec.Command("docker", "stop", containerID)
	if out, err := stop.CombinedOutput(); err != nil {
		return fmt.Errorf("docker stop: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// Remove
	rm := exec.Command("docker", "rm", containerID)
	if out, err := rm.CombinedOutput(); err != nil {
		return fmt.Errorf("docker rm: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// dockerSync verifies a running container matches its dal profile.
// Since instructions and skills are bind-mounted, file changes are automatic.
// Sync handles structural changes (new skills added/removed in dal.cue).
func dockerSync(localdalRoot, containerID string, dal *localdal.DalProfile) error {
	// Bind mounts auto-reflect file changes.
	// If dal.cue changed (e.g., new skill added), container needs restart.
	// For now, log what would change.
	log.Printf("[sync] %s: player=%s, skills=%d — bind mounts are live", dal.Name, dal.Player, len(dal.Skills))
	return nil
}

// dockerLogs returns logs from a Docker container.
func dockerLogs(containerID string) (string, error) {
	cmd := exec.Command("docker", "logs", "--tail", "100", containerID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker logs: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

// dockerExec runs a command inside a Docker container (for attach).
func dockerExec(containerID string) *exec.Cmd {
	return exec.Command("docker", "exec", "-it", containerID, "/bin/bash")
}
