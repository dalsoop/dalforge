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

const (
	// containerBasePrefix is the base naming prefix for dal Docker containers.
	// Full name: containerBasePrefix + team + "-" + instanceName (e.g. dal-vk-leader)
	containerBasePrefix = "dal-"

	// imagePrefix is the Docker image repository prefix.
	imagePrefix = "dalcenter/"

	// containerWorkDir is the working directory inside dal containers.
	containerWorkDir = "/workspace"

	// containerDalDir is the mount point for the dal directory inside containers.
	containerDalDir = "/dal"

	// dockerHostAlias is the hostname used to reach the host from inside containers.
	dockerHostAlias = "host.docker.internal"

	// defaultGitEmailDomain is the fallback domain for dal git email.
	defaultGitEmailDomain = "dalcenter.local"

	// defaultLogTail is the default number of log lines to return.
	defaultLogTail = "100"
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
	containerName := dalContainerName(instanceName, dal.UUID)
	tag := "latest"
	if dal.PlayerVersion != "" {
		tag = dal.PlayerVersion
	}
	image := fmt.Sprintf("%s%s:%s", imagePrefix, dal.Player, tag)

	dalDir := filepath.Join(localdalRoot, dal.FolderName)
	home := playerHome(dal.Player)
	hostHome, _ := os.UserHomeDir()

	args := []string{
		"run", "-d",
		"--name", containerName,
		"--hostname", dal.Name,
		// Docker label for UUID-based filtering in reconcile
		"--label", "dalcenter.uuid=" + dal.UUID,
		// Linux Docker: host.docker.internal is not available by default.
		// Add it explicitly pointing to the Docker bridge gateway.
		"--add-host", dockerHostAlias + ":host-gateway",
		// Environment
		"-e", fmt.Sprintf("DAL_NAME=%s", dal.Name),
		"-e", fmt.Sprintf("DAL_UUID_SHORT=%s", uuidShort(dal.UUID)),
		"-e", fmt.Sprintf("DAL_UUID=%s", dal.UUID),
		"-e", fmt.Sprintf("DAL_ROLE=%s", dal.Role),
		"-e", fmt.Sprintf("DAL_PLAYER=%s", dal.Player),
	}
	if dal.Model != "" {
		args = append(args, "-e", fmt.Sprintf("DAL_MODEL=%s", dal.Model))
	}
	args = append(args,
		"-e", fmt.Sprintf("DALCENTER_URL=http://%s%s", dockerHostAlias, daemonAddr),
		"-e", fmt.Sprintf("MATTERMOST_URL=%s", os.Getenv("DALCENTER_MM_URL")), // set by daemon.Run()
		// VeilKey — pass through if available
		"-e", fmt.Sprintf("VEILKEY_LOCALVAULT_URL=%s", os.Getenv("VEILKEY_LOCALVAULT_URL")),
		// Mount dal directory (read-only)
		"-v", fmt.Sprintf("%s:%s:ro", dalDir, containerDalDir),
		// Working directory
		"-w", containerWorkDir,
	)

	// Mount service repo as workspace (shared mode) or leave empty for clone mode
	isCloneMode := dal.Workspace == "clone"
	if serviceRepo != "" && !isCloneMode {
		args = append(args, "-v", fmt.Sprintf("%s:%s", serviceRepo, containerWorkDir))
		// .dal/ ro overlay — member가 .dal/ 우회 수정 방지 (Docker 후순위 mount가 이김)
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", localdalRoot, filepath.Join(containerWorkDir, ".dal")))
	}

	// Mount credentials (player-specific)
	switch dal.Player {
	case "claude":
		credPath := filepath.Join(hostHome, ".claude", ".credentials.json")
		if _, err := os.Stat(credPath); err == nil {
			args = append(args, "-v", fmt.Sprintf("%s:%s/.credentials.json", credPath, home))
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
		// Gemini uses API key — resolve from dal.cue (VK:/env:), fallback to host env
		key := dal.GeminiAPIKey
		if key != "" {
			if strings.HasPrefix(key, "VK:") {
				resolved, err := resolveVeilKey(key)
				if err != nil {
					log.Printf("[docker] warning: failed to resolve %s: %v", key, err)
					key = ""
				} else {
					key = resolved
				}
			} else if strings.HasPrefix(key, "env:") {
				key = os.Getenv(strings.TrimPrefix(key, "env:"))
			}
		}
		if key == "" {
			key = os.Getenv("GEMINI_API_KEY")
		}
		if key != "" {
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

	// Mount decisions.md as shared team memory (read-only — scribe commits changes)
	decisionsPath := filepath.Join(localdalRoot, "decisions.md")
	if _, err := os.Stat(decisionsPath); err == nil {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", decisionsPath, filepath.Join(containerWorkDir, "decisions.md")))
	}

	// Mount decisions-archive.md (read-only for all)
	archivePath := filepath.Join(localdalRoot, "decisions-archive.md")
	if _, err := os.Stat(archivePath); err == nil {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", archivePath, filepath.Join(containerWorkDir, "decisions-archive.md")))
	}

	// Mount decisions inbox — rw for member (drop proposals), ro for leader
	inboxPath := inboxDir(serviceRepo)
	inboxContainerPath := filepath.Join(containerWorkDir, "decisions", "inbox")
	if dal.Role == "member" {
		args = append(args, "-v", fmt.Sprintf("%s:%s", inboxPath, inboxContainerPath))
	} else {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", inboxPath, inboxContainerPath))
	}

	// Mount wisdom.md (read-only for all)
	wisdomPath := filepath.Join(localdalRoot, "wisdom.md")
	if _, err := os.Stat(wisdomPath); err == nil {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", wisdomPath, filepath.Join(containerWorkDir, "wisdom.md")))
	}

	// Mount now.md from state dir (read-only for member, read-write for leader)
	nowFile := filepath.Join(stateDir(serviceRepo), "now.md")
	// Create now.md if it doesn't exist
	if _, err := os.Stat(nowFile); err != nil {
		os.WriteFile(nowFile, []byte("# Now\n"), 0644)
	}
	if dal.Role == "leader" {
		args = append(args, "-v", fmt.Sprintf("%s:%s", nowFile, filepath.Join(containerWorkDir, "now.md")))
	} else {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", nowFile, filepath.Join(containerWorkDir, "now.md")))
	}

	// Mount history-buffer — rw for member (append session records), ro for leader
	histBufPath := filepath.Join(stateDir(serviceRepo), "history-buffer")
	os.MkdirAll(histBufPath, 0755)
	if dal.Role == "member" {
		args = append(args, "-v", fmt.Sprintf("%s:%s", histBufPath, filepath.Join(containerWorkDir, "history-buffer")))
	} else {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", histBufPath, filepath.Join(containerWorkDir, "history-buffer")))
	}

	// Mount wisdom/inbox — rw for member (drop proposals), ro for leader
	wisdomInbox := filepath.Join(stateDir(serviceRepo), "wisdom", "inbox")
	os.MkdirAll(wisdomInbox, 0755)
	if dal.Role == "member" {
		args = append(args, "-v", fmt.Sprintf("%s:%s", wisdomInbox, filepath.Join(containerWorkDir, "wisdom-inbox")))
	} else {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", wisdomInbox, filepath.Join(containerWorkDir, "wisdom-inbox")))
	}

	// Inject Claude Code settings.json for autoApprove (dal runs unattended)
	if dal.Player == "claude" {
		settingsJSON := `{"permissions":{"allow":["Bash(*)","Edit(*)","Write(*)","Read(*)","Glob(*)","Grep(*)","WebFetch(*)","WebSearch","Agent(*)"],"defaultMode":"autoApprove"}}`
		settingsPath := filepath.Join(os.TempDir(), fmt.Sprintf("dal-settings-%s.json", containerName))
		if err := os.WriteFile(settingsPath, []byte(settingsJSON), 0644); err == nil {
			args = append(args, "-v", fmt.Sprintf("%s:%s:ro", settingsPath, filepath.Join(home, ".claude", "settings.json")))
		}
	}

	// Git config from dal.cue or defaults
	gitUser := dal.GitUser
	if gitUser == "" {
		gitUser = containerBasePrefix + dal.Name + "-" + uuidShort(dal.UUID)
	}
	gitEmail := dal.GitEmail
	if gitEmail == "" {
		gitEmail = fmt.Sprintf("%s%s-%s@%s", containerBasePrefix, dal.Name, uuidShort(dal.UUID), defaultGitEmailDomain)
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

	// Extra bash tools: unrestricted for leader (needs dalcli-leader, git, etc.)
	// and for special images like "go", "rust" (needs build tools).
	if dal.Role == "leader" || dal.PlayerVersion == "go" || dal.PlayerVersion == "rust" {
		args = append(args, "-e", "DAL_EXTRA_BASH=*")
	}

	// Auto task: periodic self-execution
	if dal.AutoTask != "" {
		args = append(args, "-e", fmt.Sprintf("DAL_AUTO_TASK=%s", dal.AutoTask))
		if dal.AutoInterval != "" {
			args = append(args, "-e", fmt.Sprintf("DAL_AUTO_INTERVAL=%s", dal.AutoInterval))
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

	// Clone mode: git clone repo into container workspace
	if isCloneMode && serviceRepo != "" {
		repoURL := detectRepoURL(serviceRepo)
		if repoURL != "" {
			if err := cloneRepoInContainer(containerID, repoURL); err != nil {
				w := fmt.Sprintf("workspace clone failed: %v (falling back to empty workspace)", err)
				log.Printf("[docker] %s", w)
				warnings = append(warnings, w)
			} else {
				log.Printf("[docker] workspace: cloned %s into %s", repoURL, containerName)
			}
		} else {
			w := "clone mode: could not detect repo URL, workspace is empty"
			log.Printf("[docker] %s", w)
			warnings = append(warnings, w)
		}
	}

	return containerID, warnings, nil
}

// detectRepoURL gets the git remote URL from a local repo.
func detectRepoURL(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// cloneRepoInContainer runs git clone inside a running container.
func cloneRepoInContainer(containerID, repoURL string) error {
	// Clone into a temp dir, then move contents to /workspace
	// (container may already have /workspace as WORKDIR)
	script := fmt.Sprintf(
		`git clone --depth=50 %s /tmp/_clone && `+
			`cp -a /tmp/_clone/. /workspace/ && `+
			`rm -rf /tmp/_clone && `+
			`cd /workspace && git checkout main 2>/dev/null; true`,
		repoURL,
	)
	cmd := exec.Command("docker", "exec", containerID, "bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
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

// dockerNeedsRestart checks if a container needs to be recreated based on dal.cue changes.
// Returns a reason string if restart is needed, empty string if not.
func dockerNeedsRestart(containerID string, dal *localdal.DalProfile) (string, error) {
	// 1. Check image: compare current container image with expected
	imgCmd := exec.Command("docker", "inspect", containerID, "--format", "{{.Config.Image}}")
	imgOut, err := imgCmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker inspect image: %w", err)
	}
	currentImage := strings.TrimSpace(string(imgOut))

	tag := "latest"
	if dal.PlayerVersion != "" {
		tag = dal.PlayerVersion
	}
	expectedImage := fmt.Sprintf("%s%s:%s", imagePrefix, dal.Player, tag)

	if currentImage != expectedImage {
		return fmt.Sprintf("image changed: %s -> %s", currentImage, expectedImage), nil
	}

	// 2. Check skill mounts: compare mount count with dal.Skills length
	mountsCmd := exec.Command("docker", "inspect", containerID, "--format", "{{json .Mounts}}")
	mountsOut, err := mountsCmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker inspect mounts: %w", err)
	}

	var mounts []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(mountsOut))), &mounts); err != nil {
		return "", fmt.Errorf("parse mounts: %w", err)
	}

	// Count skill mounts: those targeting <playerHome>/skills/*
	home := playerHome(dal.Player)
	skillMountPrefix := home + "/skills/"
	var skillMountCount int
	for _, m := range mounts {
		if strings.HasPrefix(m.Destination, skillMountPrefix) {
			skillMountCount++
		}
	}

	if skillMountCount != len(dal.Skills) {
		return fmt.Sprintf("skills changed: container has %d mounts, dal.cue has %d skills", skillMountCount, len(dal.Skills)), nil
	}

	return "", nil
}

// dockerSync verifies a running container matches its dal profile.
// Since instructions and skills are bind-mounted, file content changes are automatic.
// Sync detects structural changes (image tag, skills added/removed) that require container recreation.
func dockerSync(localdalRoot, containerID string, dal *localdal.DalProfile) (needsRestart bool, reason string, err error) {
	reason, err = dockerNeedsRestart(containerID, dal)
	if err != nil {
		return false, "", err
	}
	if reason != "" {
		return true, reason, nil
	}
	log.Printf("[sync] %s: no structural changes — bind mounts are live", dal.Name)
	return false, "", nil
}

// dockerLogs returns logs from a Docker container.
func dockerLogs(containerID string) (string, error) {
	cmd := exec.Command("docker", "logs", "--tail", defaultLogTail, containerID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker logs: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

// discoveredContainer represents a container found via docker ps.
type discoveredContainer struct {
	ID      string
	Name    string // e.g. "dal-dev", "dal-dev-2"
	Running bool
}

// discoverContainers finds existing dal-* containers (both running and stopped).
// discoverContainersByUUIDs finds containers matching any of the given UUIDs.
// Docker --filter label with same key uses AND logic, so we query each UUID separately.
func discoverContainersByUUIDs(uuids []string) ([]discoveredContainer, error) {
	if len(uuids) == 0 {
		return nil, nil
	}
	var all []discoveredContainer
	seen := make(map[string]bool)
	for _, uuid := range uuids {
		containers, err := discoverByLabel("dalcenter.uuid=" + uuid)
		if err != nil {
			return nil, err
		}
		for _, c := range containers {
			if !seen[c.ID] {
				seen[c.ID] = true
				all = append(all, c)
			}
		}
	}
	return all, nil
}

func discoverByLabel(label string) ([]discoveredContainer, error) {
	cmd := exec.Command("docker", "ps", "-a",
		"--filter", "label="+label,
		"--format", "{{.ID}}\t{{.Names}}\t{{.State}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %s: %w", strings.TrimSpace(string(out)), err)
	}

	output := strings.TrimSpace(string(out))
	if output == "" {
		return nil, nil
	}

	var containers []discoveredContainer
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		containers = append(containers, discoveredContainer{
			ID:      parts[0],
			Name:    parts[1],
			Running: parts[2] == "running",
		})
	}
	return containers, nil
}

// cleanStaleContainer force-removes a container by name (best-effort).
func cleanStaleContainer(name string) error {
	cmd := exec.Command("docker", "rm", "-f", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm -f %s: %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// dockerExec runs a command inside a Docker container (for attach).
func dockerExec(containerID string) *exec.Cmd {
	return exec.Command("docker", "exec", "-it", containerID, "/bin/bash")
}
