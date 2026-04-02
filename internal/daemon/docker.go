package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdkclient "github.com/docker/go-sdk/client"
	sdkcontainer "github.com/docker/go-sdk/container"
	cexec "github.com/docker/go-sdk/container/exec"
	apicontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	mobyclient "github.com/moby/moby/client"

	"github.com/dalsoop/dalcenter/internal/localdal"
	"github.com/dalsoop/dalcenter/internal/paths"
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

// dockerClient is the package-level Docker SDK client, lazily initialized.
var (
	dockerClient     sdkclient.SDKClient
	dockerClientOnce sync.Once
	dockerClientErr  error
)

// getDockerClient returns the shared Docker SDK client, creating it on first use.
func getDockerClient() (sdkclient.SDKClient, error) {
	dockerClientOnce.Do(func() {
		ctx := context.Background()
		dockerClient, dockerClientErr = sdkclient.New(ctx)
	})
	return dockerClient, dockerClientErr
}

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

func shouldDisableContainerDM(dal *localdal.DalProfile) bool {
	return dal != nil && dal.ChannelOnly
}

func inferredFallbackPlayer(primary string) string {
	switch strings.TrimSpace(primary) {
	case "claude":
		return "codex"
	case "codex":
		return "claude"
	default:
		return ""
	}
}

func credentialPlayers(dal *localdal.DalProfile) []string {
	if dal == nil {
		return nil
	}
	var players []string
	seen := make(map[string]bool)
	add := func(player string) {
		player = strings.TrimSpace(player)
		if player == "" || seen[player] {
			return
		}
		seen[player] = true
		players = append(players, player)
	}
	add(dal.Player)
	fallback := dal.FallbackPlayer
	if strings.TrimSpace(fallback) == "" {
		fallback = inferredFallbackPlayer(dal.Player)
	}
	add(fallback)
	return players
}

func appendCredentialMounts(mounts []mount.Mount, hostHome string, players []string, warnings *[]string) []mount.Mount {
	for _, player := range players {
		switch player {
		case "claude":
			credPath := filepath.Join(hostHome, ".claude", ".credentials.json")
			if _, err := os.Stat(credPath); err == nil {
				mounts = append(mounts, mount.Mount{
					Type:     mount.TypeBind,
					Source:   credPath,
					Target:   playerHome("claude") + "/.credentials.json",
					ReadOnly: true,
				})
				if expired, _ := isCredentialExpired(credPath); expired {
					w := "Claude credential expired — run: pve-sync-creds"
					log.Printf("WARNING: %s", w)
					*warnings = append(*warnings, w)
				}
			} else {
				w := fmt.Sprintf("Claude credential not found at %s", credPath)
				log.Printf("WARNING: %s", w)
				*warnings = append(*warnings, w)
			}
		case "codex":
			credPath := filepath.Join(hostHome, ".codex", "auth.json")
			if _, err := os.Stat(credPath); err == nil {
				mounts = append(mounts, mount.Mount{
					Type:     mount.TypeBind,
					Source:   credPath,
					Target:   playerHome("codex") + "/auth.json",
					ReadOnly: true,
				})
				if expired, _ := isCredentialExpired(credPath); expired {
					w := "Codex credential expired — run: pve-sync-creds"
					log.Printf("WARNING: %s", w)
					*warnings = append(*warnings, w)
				}
			} else {
				w := fmt.Sprintf("Codex credential not found at %s", credPath)
				log.Printf("WARNING: %s", w)
				*warnings = append(*warnings, w)
			}
		}
	}
	return mounts
}

// containerBridgeURL returns the bridge URL for dal containers.
// If dalbridgeURL is set, it is used directly (dalbridge uses a routable IP).
// Otherwise, falls back to the matterbridge URL with localhost rewriting.
func containerBridgeURL(dalbridgeURL, bridgeURL string) string {
	if dalbridgeURL != "" {
		return dalbridgeURL
	}
	return bridgeURLForContainer(bridgeURL)
}

// bridgeURLForContainer rewrites localhost URLs to host.docker.internal
// so containers can reach the host's matterbridge.
func bridgeURLForContainer(bridgeURL string) string {
	bridgeURL = strings.Replace(bridgeURL, "localhost", dockerHostAlias, 1)
	bridgeURL = strings.Replace(bridgeURL, "127.0.0.1", dockerHostAlias, 1)
	return bridgeURL
}

// dockerRun creates and starts a Docker container for a dal.
// It returns the container ID, any credential warnings, and an error.
func dockerRun(localdalRoot, serviceRepo, instanceName, daemonAddr, bridgeURL, dalbridgeURL string, dal *localdal.DalProfile) (string, []string, error) {
	var warnings []string
	ctx := context.Background()

	cli, err := getDockerClient()
	if err != nil {
		return "", nil, fmt.Errorf("docker client: %w", err)
	}

	containerName := dalContainerName(instanceName, dal.UUID)
	tag := "latest"
	if dal.PlayerVersion != "" {
		tag = dal.PlayerVersion
	}
	image := fmt.Sprintf("%s%s:%s", imagePrefix, dal.Player, tag)

	tplRoot := localdal.ResolveTemplateRoot(localdalRoot)
	dalDir := filepath.Join(tplRoot, dal.FolderName)
	if _, err := os.Stat(dalDir); err != nil {
		// Fallback: look directly in .dal/<name>
		dalDir = filepath.Join(localdalRoot, dal.FolderName)
	}
	home := playerHome(dal.Player)
	hostHome, _ := os.UserHomeDir()

	// Build environment variables
	envMap := map[string]string{
		"DAL_NAME":               dal.Name,
		"DAL_UUID_SHORT":         uuidShort(dal.UUID),
		"DAL_UUID":               dal.UUID,
		"DAL_ROLE":               dal.Role,
		"DAL_PLAYER":             dal.Player,
		"DALCENTER_URL":          fmt.Sprintf("http://%s%s", dockerHostAlias, daemonAddr),
		"DALCENTER_BRIDGE_URL":   containerBridgeURL(dalbridgeURL, bridgeURL),
		"VEILKEY_LOCALVAULT_URL": os.Getenv("VEILKEY_LOCALVAULT_URL"),
	}
	if shouldDisableContainerDM(dal) {
		envMap["DAL_NO_DM"] = "1"
	}
	if dal.Model != "" {
		envMap["DAL_MODEL"] = dal.Model
	}
	if dal.FallbackPlayer != "" {
		envMap["DAL_FALLBACK_PLAYER"] = dal.FallbackPlayer
	}
	if externalURL := strings.TrimSpace(os.Getenv("DALCENTER_EXTERNAL_URL")); externalURL != "" {
		envMap["DALCENTER_EXTERNAL_URL"] = externalURL
	}

	// Build bind mounts
	var bindMounts []mount.Mount

	// Dal directory (read-only)
	bindMounts = append(bindMounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   dalDir,
		Target:   containerDalDir,
		ReadOnly: true,
	})

	// Mount service repo as workspace (shared mode) or leave empty for clone mode
	isCloneMode := dal.Workspace == "clone" || dal.Role == "member"
	if serviceRepo != "" && !isCloneMode {
		bindMounts = append(bindMounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: serviceRepo,
			Target: containerWorkDir,
		})
		// .dal/ ro overlay — member cannot bypass-modify .dal/ (Docker later mount wins)
		bindMounts = append(bindMounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   localdalRoot,
			Target:   filepath.Join(containerWorkDir, ".dal"),
			ReadOnly: true,
		})
	}

	// Mount credentials for primary + fallback players.
	bindMounts = appendCredentialMounts(bindMounts, hostHome, credentialPlayers(dal), &warnings)

	// Player-specific env
	switch dal.Player {
	case "gemini":
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
			envMap["GEMINI_API_KEY"] = key
		} else {
			w := "GEMINI_API_KEY not set for gemini dal"
			log.Printf("WARNING: %s", w)
			warnings = append(warnings, w)
		}
	}

	// Mount skills: shared skills from .dal/skills/ + per-dal skills from dal.cue
	mountedSkills := make(map[string]bool)

	// 1. Always mount all shared skills from .dal/template/skills/
	sharedSkillsDir := filepath.Join(tplRoot, "skills")
	if entries, err := os.ReadDir(sharedSkillsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillName := entry.Name()
			skillPath := filepath.Join(sharedSkillsDir, skillName)
			targetPath := filepath.Join(home, "skills", skillName)
			bindMounts = append(bindMounts, mount.Mount{
				Type:     mount.TypeBind,
				Source:   skillPath,
				Target:   targetPath,
				ReadOnly: true,
			})
			mountedSkills[skillName] = true
		}
	}

	// 2. Mount per-dal skills from dal.cue (skip if already mounted as shared)
	for _, skill := range dal.Skills {
		skillBase := filepath.Base(skill)
		if mountedSkills[skillBase] {
			continue
		}
		skillPath := filepath.Join(tplRoot, skill)
		if _, err := os.Stat(skillPath); err != nil {
			skillPath = filepath.Join(localdalRoot, skill)
		}
		if _, err := os.Stat(skillPath); err != nil {
			log.Printf("[docker] skill %s not found, skipping", skill)
			continue
		}
		targetPath := filepath.Join(home, "skills", skillBase)
		bindMounts = append(bindMounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   skillPath,
			Target:   targetPath,
			ReadOnly: true,
		})
		mountedSkills[skillBase] = true
	}

	// Mount charter.md as the right filename (e.g. CLAUDE.md, AGENTS.md, GEMINI.md).
	instrSrc := filepath.Join(dalDir, "charter.md")
	instrDst := filepath.Join(home, instructionsFileName(dal.Player))
	if info, err := os.Stat(instrSrc); err != nil {
		w := fmt.Sprintf("charter.md not found at %s — skipping instructions mount", instrSrc)
		log.Printf("WARNING: %s", w)
		warnings = append(warnings, w)
	} else if info.IsDir() {
		w := fmt.Sprintf("charter.md is a directory at %s — skipping instructions mount", instrSrc)
		log.Printf("WARNING: %s", w)
		warnings = append(warnings, w)
	} else {
		bindMounts = append(bindMounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   instrSrc,
			Target:   instrDst,
			ReadOnly: true,
		})
	}

	// Mount decisions.md as shared team memory (read-only)
	decisionsPath := filepath.Join(tplRoot, "decisions.md")
	if _, err := os.Stat(decisionsPath); err != nil {
		decisionsPath = filepath.Join(localdalRoot, "decisions.md")
	}
	if _, err := os.Stat(decisionsPath); err == nil {
		bindMounts = append(bindMounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   decisionsPath,
			Target:   filepath.Join(containerWorkDir, "decisions.md"),
			ReadOnly: true,
		})
	}

	// Mount decisions-archive.md (read-only for all)
	archivePath := filepath.Join(tplRoot, "decisions-archive.md")
	if _, err := os.Stat(archivePath); err != nil {
		archivePath = filepath.Join(localdalRoot, "decisions-archive.md")
	}
	if _, err := os.Stat(archivePath); err == nil {
		bindMounts = append(bindMounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   archivePath,
			Target:   filepath.Join(containerWorkDir, "decisions-archive.md"),
			ReadOnly: true,
		})
	}

	// Mount decisions inbox — rw for member, ro for leader
	inboxPath := inboxDir(serviceRepo)
	inboxContainerPath := filepath.Join(containerWorkDir, "decisions", "inbox")
	bindMounts = append(bindMounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   inboxPath,
		Target:   inboxContainerPath,
		ReadOnly: dal.Role != "member",
	})

	// Mount wisdom.md (read-only for all)
	wisdomPath := filepath.Join(tplRoot, "wisdom.md")
	if _, err := os.Stat(wisdomPath); err != nil {
		wisdomPath = filepath.Join(localdalRoot, "wisdom.md")
	}
	if _, err := os.Stat(wisdomPath); err == nil {
		bindMounts = append(bindMounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   wisdomPath,
			Target:   filepath.Join(containerWorkDir, "wisdom.md"),
			ReadOnly: true,
		})
	}

	// Mount now.md from state dir (read-only for member, read-write for leader)
	nowFile := filepath.Join(stateDir(serviceRepo), "now.md")
	if _, err := os.Stat(nowFile); err != nil {
		os.WriteFile(nowFile, []byte("# Now\n"), 0644)
	}
	bindMounts = append(bindMounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   nowFile,
		Target:   filepath.Join(containerWorkDir, "now.md"),
		ReadOnly: dal.Role != "leader",
	})

	// Mount history-buffer — rw for member, ro for leader
	histBufPath := filepath.Join(stateDir(serviceRepo), "history-buffer")
	os.MkdirAll(histBufPath, 0755)
	bindMounts = append(bindMounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   histBufPath,
		Target:   filepath.Join(containerWorkDir, "history-buffer"),
		ReadOnly: dal.Role != "member",
	})

	// Mount wisdom/inbox — rw for member, ro for leader
	wisdomInbox := filepath.Join(stateDir(serviceRepo), "wisdom", "inbox")
	os.MkdirAll(wisdomInbox, 0755)
	bindMounts = append(bindMounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   wisdomInbox,
		Target:   filepath.Join(containerWorkDir, "wisdom-inbox"),
		ReadOnly: dal.Role != "member",
	})

	// Inject Claude Code settings.json for autoApprove (dal runs unattended)
	if dal.Player == "claude" {
		settingsJSON := `{"permissions":{"allow":["Bash(*)","Edit(*)","Write(*)","Read(*)","Glob(*)","Grep(*)","WebFetch(*)","WebSearch","Agent(*)"],"defaultMode":"autoApprove"}}`
		settingsPath := filepath.Join(os.TempDir(), fmt.Sprintf("dal-settings-%s.json", containerName))
		if err := os.WriteFile(settingsPath, []byte(settingsJSON), 0644); err == nil {
			bindMounts = append(bindMounts, mount.Mount{
				Type:     mount.TypeBind,
				Source:   settingsPath,
				Target:   filepath.Join(home, ".claude", "settings.json"),
				ReadOnly: true,
			})
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
	envMap["GIT_AUTHOR_NAME"] = gitUser
	envMap["GIT_AUTHOR_EMAIL"] = gitEmail
	envMap["GIT_COMMITTER_NAME"] = gitUser
	envMap["GIT_COMMITTER_EMAIL"] = gitEmail

	// GitHub token for git push + gh CLI
	if dal.GitHubToken != "" {
		token := dal.GitHubToken
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
			envMap["GITHUB_TOKEN"] = token
			envMap["GH_TOKEN"] = token
		}
	}

	// Extra bash tools: unrestricted for leader and special images
	if dal.Role == "leader" || dal.PlayerVersion == "go" || dal.PlayerVersion == "rust" {
		envMap["DAL_EXTRA_BASH"] = "*"
	}

	// DAL_MAX_DURATION — pass through from host env
	if maxDur := os.Getenv("DAL_MAX_DURATION"); maxDur != "" {
		envMap["DAL_MAX_DURATION"] = maxDur
	}

	// Auto task: periodic self-execution
	if dal.AutoTask != "" {
		envMap["DAL_AUTO_TASK"] = dal.AutoTask
		if dal.AutoInterval != "" {
			envMap["DAL_AUTO_INTERVAL"] = dal.AutoInterval
		}
	}

	// Build container options
	opts := []sdkcontainer.ContainerCustomizer{
		sdkcontainer.WithClient(cli),
		sdkcontainer.WithImage(image),
		sdkcontainer.WithName(containerName),
		sdkcontainer.WithEnv(envMap),
		sdkcontainer.WithLabels(map[string]string{
			"dalcenter.uuid": dal.UUID,
		}),
		sdkcontainer.WithConfigModifier(func(cfg *apicontainer.Config) {
			cfg.Hostname = dal.Name
			cfg.WorkingDir = containerWorkDir
		}),
		sdkcontainer.WithHostConfigModifier(func(hc *apicontainer.HostConfig) {
			hc.ExtraHosts = append(hc.ExtraHosts, dockerHostAlias+":host-gateway")
			hc.Mounts = append(hc.Mounts, bindMounts...)
		}),
	}

	ctr, err := sdkcontainer.Run(ctx, opts...)
	if err != nil {
		return "", nil, fmt.Errorf("docker run: %w", err)
	}
	containerID := ctr.ID()

	// Inject dalcli / dalcli-leader based on role
	if err := injectCli(ctx, ctr, dal.Role); err != nil {
		log.Printf("[docker] warning: failed to inject dalcli: %v", err)
	}

	// Clone mode: git clone repo into container workspace
	if isCloneMode && serviceRepo != "" {
		repoURL := detectRepoURL(serviceRepo)
		if repoURL != "" {
			if err := cloneRepoInContainer(ctx, ctr, repoURL); err != nil {
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
func cloneRepoInContainer(ctx context.Context, ctr *sdkcontainer.Container, repoURL string) error {
	script := fmt.Sprintf(
		`git clone --depth=50 %s /tmp/_clone && `+
			`cp -a /tmp/_clone/. /workspace/ && `+
			`rm -rf /tmp/_clone && `+
			`cd /workspace && git checkout main 2>/dev/null; true`,
		repoURL,
	)
	exitCode, output, err := ctr.Exec(ctx, []string{"bash", "-c", script}, cexec.Multiplexed())
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if exitCode != 0 {
		out, _ := io.ReadAll(output)
		return fmt.Errorf("exit %d: %s", exitCode, strings.TrimSpace(string(out)))
	}
	return nil
}

// crossRepoDir is the path inside the container where cross-repo clones are stored.
const crossRepoDir = "/tmp/cross-repo"

// setupCrossRepo clones a target repo inside the container for cross-repo task execution.
// Returns the working directory path inside the container.
func setupCrossRepo(containerID, repo string) (string, error) {
	// Clone using gh repo clone (handles auth via GH_TOKEN already in container env)
	script := fmt.Sprintf(
		`rm -rf %s && gh repo clone %s %s -- --depth=50`,
		crossRepoDir, repo, crossRepoDir,
	)
	cmd := exec.Command("docker", "exec", containerID, "bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cross-repo clone %s: %v: %s", repo, err, strings.TrimSpace(string(out)))
	}
	log.Printf("[cross-repo] cloned %s into %s:%s", repo, containerID[:12], crossRepoDir)
	return crossRepoDir, nil
}

// cleanupCrossRepo removes the cross-repo clone from the container.
func cleanupCrossRepo(containerID string) {
	cmd := exec.Command("docker", "exec", containerID, "rm", "-rf", crossRepoDir)
	if err := cmd.Run(); err != nil {
		log.Printf("[cross-repo] cleanup failed: %v", err)
	}
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

// injectCli copies dalcli or dalcli-leader binary into the container,
// and injects the settings.json from the host config dir.
func injectCli(ctx context.Context, ctr *sdkcontainer.Container, role string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	binDir := filepath.Dir(self)

	// Always inject dalcli
	dalcliPath := filepath.Join(binDir, "dalcli")
	if _, err := os.Stat(dalcliPath); err == nil {
		data, err := os.ReadFile(dalcliPath)
		if err != nil {
			return fmt.Errorf("read dalcli: %w", err)
		}
		if err := ctr.CopyToContainer(ctx, data, "/usr/local/bin/dalcli", 0755); err != nil {
			return fmt.Errorf("inject dalcli: %w", err)
		}
	}

	// Inject dalcli-leader for leader role
	if role == "leader" {
		leaderPath := filepath.Join(binDir, "dalcli-leader")
		if _, err := os.Stat(leaderPath); err == nil {
			data, err := os.ReadFile(leaderPath)
			if err != nil {
				return fmt.Errorf("read dalcli-leader: %w", err)
			}
			if err := ctr.CopyToContainer(ctx, data, "/usr/local/bin/dalcli-leader", 0755); err != nil {
				return fmt.Errorf("inject dalcli-leader: %w", err)
			}
		}
	}

	// Inject Claude settings.json if available (auto-approve for autonomous operation)
	settingsPath := filepath.Join(paths.ConfigDir(), "settings.json")
	if _, err := os.Stat(settingsPath); err == nil {
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			log.Printf("[inject] settings.json read failed: %v", err)
		} else {
			if err := ctr.CopyToContainer(ctx, data, "/root/.claude/settings.json", 0644); err != nil {
				log.Printf("[inject] settings.json copy failed: %v", err)
			}
		}
	}

	return nil
}

// setupWorkspace prepares the workspace inside a running container:
// 1. Branch checkout (if issueID is provided)
// 2. Package installation (if setup.packages is non-empty)
// 3. Setup commands (if setup.commands is non-empty)
// Returns warnings for non-fatal failures.
func setupWorkspace(containerID string, dal *localdal.DalProfile, issueID string) []string {
	var warnings []string
	ctx := context.Background()

	cli, err := getDockerClient()
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("docker client: %v", err))
		return warnings
	}

	ctr, err := sdkcontainer.FromID(ctx, cli, containerID)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("container from ID: %v", err))
		return warnings
	}

	// 1. Branch checkout: issue-{N}/{dal-name}
	if issueID != "" {
		branchName := fmt.Sprintf("issue-%s/%s", issueID, dal.Name)
		base := dal.Branch.Base
		if base == "" {
			base = "main"
		}
		// Verify base branch exists; fallback to HEAD if not
		exitCode, _, _ := ctr.Exec(ctx, []string{"git", "-C", "/workspace", "rev-parse", "--verify", base}, cexec.Multiplexed())
		if exitCode != 0 {
			log.Printf("[setup] base branch %q not found, using HEAD", base)
			base = "HEAD"
		}

		// Try to create new branch first, then checkout existing
		exitCode, output, _ := ctr.Exec(ctx, []string{"git", "-C", "/workspace", "checkout", "-b", branchName, base}, cexec.Multiplexed())
		if exitCode != 0 {
			out1, _ := io.ReadAll(output)
			exitCode2, output2, _ := ctr.Exec(ctx, []string{"git", "-C", "/workspace", "checkout", branchName}, cexec.Multiplexed())
			if exitCode2 != 0 {
				out2, _ := io.ReadAll(output2)
				warnings = append(warnings, fmt.Sprintf("branch checkout failed: %s / %s", strings.TrimSpace(string(out1)), strings.TrimSpace(string(out2))))
			} else {
				log.Printf("[setup] checked out existing branch %s", branchName)
			}
		} else {
			log.Printf("[setup] created branch %s from %s", branchName, base)
		}
	}

	// 2. Package installation
	if len(dal.Setup.Packages) > 0 {
		shellCmd := "apt-get update -qq && apt-get install -y -qq " + strings.Join(dal.Setup.Packages, " ")
		exitCode, output, _ := ctr.Exec(ctx, []string{"bash", "-c", shellCmd}, cexec.Multiplexed())
		if exitCode != 0 {
			out, _ := io.ReadAll(output)
			warnings = append(warnings, fmt.Sprintf("package install failed: %s", strings.TrimSpace(string(out))))
		} else {
			log.Printf("[setup] installed packages: %v", dal.Setup.Packages)
		}
	}

	// 3. Setup commands (with 1 retry on failure)
	for _, cmd := range dal.Setup.Commands {
		exitCode, output, _ := ctr.Exec(ctx, []string{"bash", "-c", cmd}, cexec.Multiplexed(), cexec.WithWorkingDir("/workspace"))
		if exitCode != 0 {
			log.Printf("[setup] command failed (retrying): %s", cmd)
			exitCode2, output2, _ := ctr.Exec(ctx, []string{"bash", "-c", cmd}, cexec.Multiplexed(), cexec.WithWorkingDir("/workspace"))
			if exitCode2 != 0 {
				out, _ := io.ReadAll(output2)
				warnings = append(warnings, fmt.Sprintf("setup command failed after retry: %q: %s", cmd, strings.TrimSpace(string(out))))
			} else {
				log.Printf("[setup] command succeeded on retry: %s", cmd)
			}
			_ = output
		} else {
			log.Printf("[setup] command: %s", cmd)
		}
	}

	return warnings
}

// dockerStop stops and removes a Docker container.
func dockerStop(containerID string) error {
	ctx := context.Background()
	cli, err := getDockerClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	ctr, err := sdkcontainer.FromID(ctx, cli, containerID)
	if err != nil {
		return fmt.Errorf("container from ID: %w", err)
	}

	// Stop
	if err := ctr.Stop(ctx); err != nil {
		return fmt.Errorf("docker stop: %w", err)
	}
	// Remove
	if err := ctr.Terminate(ctx); err != nil {
		return fmt.Errorf("docker rm: %w", err)
	}
	return nil
}

// dockerNeedsRestart checks if a container needs to be recreated based on dal.cue changes.
// Returns a reason string if restart is needed, empty string if not.
func dockerNeedsRestart(localdalRoot, containerID string, dal *localdal.DalProfile) (string, error) {
	ctx := context.Background()
	cli, err := getDockerClient()
	if err != nil {
		return "", fmt.Errorf("docker client: %w", err)
	}

	ctr, err := sdkcontainer.FromID(ctx, cli, containerID)
	if err != nil {
		return "", fmt.Errorf("container from ID: %w", err)
	}

	// 1. Check image: compare current container image with expected
	inspectResult, err := ctr.Inspect(ctx)
	if err != nil {
		return "", fmt.Errorf("docker inspect: %w", err)
	}
	currentImage := inspectResult.Container.Config.Image

	tag := "latest"
	if dal.PlayerVersion != "" {
		tag = dal.PlayerVersion
	}
	expectedImage := fmt.Sprintf("%s%s:%s", imagePrefix, dal.Player, tag)

	if currentImage != expectedImage {
		return fmt.Sprintf("image changed: %s -> %s", currentImage, expectedImage), nil
	}

	// 2. Check skill mounts: shared skills from .dal/skills/ + per-dal skills
	home := playerHome(dal.Player)
	skillMountPrefix := home + "/skills/"
	var skillMountCount int
	for _, m := range inspectResult.Container.Mounts {
		if strings.HasPrefix(m.Destination, skillMountPrefix) {
			skillMountCount++
		}
	}

	// Expected: shared skills from .dal/template/skills/ + unique per-dal skills
	tplRoot := localdal.ResolveTemplateRoot(localdalRoot)
	expectedSkills := make(map[string]bool)
	sharedSkillsDir := filepath.Join(tplRoot, "skills")
	if entries, err := os.ReadDir(sharedSkillsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				expectedSkills[entry.Name()] = true
			}
		}
	}
	for _, skill := range dal.Skills {
		expectedSkills[filepath.Base(skill)] = true
	}

	if skillMountCount != len(expectedSkills) {
		return fmt.Sprintf("skills changed: container has %d mounts, expected %d skills", skillMountCount, len(expectedSkills)), nil
	}

	return "", nil
}

// dockerSync verifies a running container matches its dal profile.
// Since instructions and skills are bind-mounted, file content changes are automatic.
// Sync detects structural changes (image tag, skills added/removed) that require container recreation.
func dockerSync(localdalRoot, containerID string, dal *localdal.DalProfile) (needsRestart bool, reason string, err error) {
	reason, err = dockerNeedsRestart(localdalRoot, containerID, dal)
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
	ctx := context.Background()
	cli, err := getDockerClient()
	if err != nil {
		return "", fmt.Errorf("docker client: %w", err)
	}

	result, err := cli.ContainerLogs(ctx, containerID, mobyclient.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       defaultLogTail,
	})
	if err != nil {
		return "", fmt.Errorf("docker logs: %w", err)
	}
	defer result.Close()

	out, err := io.ReadAll(result)
	if err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}
	return string(out), nil
}

// discoveredContainer represents a container found via docker ps.
type discoveredContainer struct {
	ID      string
	Name    string // e.g. "dal-dev", "dal-dev-2"
	Running bool
}

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
	ctx := context.Background()
	cli, err := getDockerClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	filters := make(mobyclient.Filters)
	filters = filters.Add("label", label)

	result, err := cli.ContainerList(ctx, mobyclient.ContainerListOptions{
		All:     true,
		Filters: filters,
	})
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}

	var containers []discoveredContainer
	for _, c := range result.Items {
		name := ""
		if len(c.Names) > 0 {
			// Docker prefixes names with "/", strip it
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		containers = append(containers, discoveredContainer{
			ID:      c.ID,
			Name:    name,
			Running: c.State == apicontainer.StateRunning,
		})
	}
	return containers, nil
}

// cleanStaleContainer force-removes a container by name (best-effort).
func cleanStaleContainer(name string) error {
	ctx := context.Background()
	cli, err := getDockerClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	_, err = cli.ContainerRemove(ctx, name, mobyclient.ContainerRemoveOptions{
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("docker rm -f %s: %w", name, err)
	}
	return nil
}

// dockerExec runs a command inside a Docker container (for interactive attach).
// This uses exec.Command directly because interactive TTY sessions require
// direct stdin/stdout/stderr passthrough that the SDK does not easily support.
func dockerExec(containerID string) *exec.Cmd {
	return exec.Command("docker", "exec", "-it", containerID, "/bin/bash")
}
