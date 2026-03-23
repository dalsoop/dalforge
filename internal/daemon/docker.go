package daemon

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dalsoop/dalcenter/internal/localdal"
)

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
func dockerRun(localdalRoot string, dal *localdal.DalProfile) (string, error) {
	containerName := fmt.Sprintf("dal-%s", dal.Name)
	image := fmt.Sprintf("dalcenter/%s:latest", dal.Player)

	dalDir := filepath.Join(localdalRoot, dal.FolderName)
	home := playerHome(dal.Player)

	args := []string{
		"run", "-d",
		"--name", containerName,
		"--hostname", dal.Name,
		// Environment
		"-e", fmt.Sprintf("DAL_NAME=%s", dal.Name),
		"-e", fmt.Sprintf("DAL_UUID=%s", dal.UUID),
		"-e", fmt.Sprintf("DAL_ROLE=%s", dal.Role),
		"-e", fmt.Sprintf("DAL_PLAYER=%s", dal.Player),
		// Mount dal directory (read-only)
		"-v", fmt.Sprintf("%s:%s:ro", dalDir, "/dal"),
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

	args = append(args, image)

	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker run: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
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

// dockerSync re-copies skills and instructions to a running container.
func dockerSync(localdalRoot, containerID string, dal *localdal.DalProfile) error {
	home := playerHome(dal.Player)

	// Copy instructions
	dalDir := filepath.Join(localdalRoot, dal.FolderName)
	instrSrc := filepath.Join(dalDir, "instructions.md")
	instrDst := fmt.Sprintf("%s:%s", containerID, filepath.Join(home, instructionsFileName(dal.Player)))
	cp := exec.Command("docker", "cp", instrSrc, instrDst)
	if out, err := cp.CombinedOutput(); err != nil {
		return fmt.Errorf("docker cp instructions: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Copy skills
	for _, skill := range dal.Skills {
		skillPath := filepath.Join(localdalRoot, skill)
		targetPath := fmt.Sprintf("%s:%s", containerID, filepath.Join(home, "skills", filepath.Base(skill)))
		cp := exec.Command("docker", "cp", skillPath, targetPath)
		if out, err := cp.CombinedOutput(); err != nil {
			return fmt.Errorf("docker cp skill %s: %s: %w", skill, strings.TrimSpace(string(out)), err)
		}
	}

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
