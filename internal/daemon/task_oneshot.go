package daemon

import (
	"bytes"
	"net/http"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dalsoop/dalcenter/internal/localdal"
)

// execTaskOneShot creates an ephemeral container, runs the task, and auto-removes it.
// Pattern: Shannon (--rm -d) + claude-code-container (I/O separation, security) + AgentField (atomic limiter)
func (d *Daemon) execTaskOneShot(dalName, role, task string, tr *taskResult) {
	log.Printf("[oneshot] starting %s role=%s task=%s", tr.ID, role, truncateStr(task, 80))

	// ── PRE-GATE (harness) ──
	// Build a temporary Container for harness compatibility
	tempC := &Container{DalName: dalName, Role: role, Player: "claude"}
	if err := d.preTaskGate(tempC, tr); err != nil {
		now := time.Now().UTC()
		tr.DoneAt = &now
		tr.Status = "failed"
		tr.Error = err.Error()
		return
	}

	// ── CONCURRENCY LIMIT (AgentField pattern) ──
	if err := d.taskLimiter.Acquire("oneshot"); err != nil {
		now := time.Now().UTC()
		tr.DoneAt = &now
		tr.Status = "failed"
		tr.Error = err.Error()
		log.Printf("[oneshot] %s blocked by limiter: %v", tr.ID, err)
		return
	}
	defer d.taskLimiter.Release("oneshot")

	// ── DAL PROFILE ──
	dal, err := localdal.ReadDalCue(d.dalCuePath(dalName), dalName)
	if err != nil {
		dal = &localdal.DalProfile{Name: dalName, Player: "claude", Role: role}
	}

	// ── CONTAINER NAME (Shannon pattern: task-{id}) ──
	containerName := fmt.Sprintf("dal-task-%s", tr.ID[:12])

	// Clean stale container with same name (best effort)
	exec.Command("docker", "rm", "-f", containerName).Run()

	// ── BUILD DOCKER RUN ARGS ──
	args := d.buildOneShotArgs(containerName, dal, tr)

	log.Printf("[oneshot] %s container=%s starting", tr.ID, containerName)

	// ── EXECUTE (blocking) ──
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Pipe task via stdin for claude -p
	cmd.Stdin = strings.NewReader(task)

	startTime := time.Now()
	err = cmd.Run()
	duration := time.Since(startTime)

	now := time.Now().UTC()
	tr.DoneAt = &now

	if err != nil {
		tr.Status = "failed"
		tr.Error = fmt.Sprintf("container exited: %v", err)
		tr.Output = stderr.String()
		if tr.Output == "" {
			tr.Output = stdout.String()
		}
		log.Printf("[oneshot] %s failed after %v: %v", tr.ID, duration, err)
	} else {
		tr.Status = "done"
		tr.Output = stdout.String()
		log.Printf("[oneshot] %s completed in %v (%d bytes output)", tr.ID, duration, len(tr.Output))
	}

	// ── POST-GATE (harness) ──
	d.postTaskGate(tempC, tr)
	if tr.Status != "failed" {
		resetConsecutiveFailure(dalName)
	}

	// Container auto-removed by --rm. Force cleanup on failure.
	exec.Command("docker", "rm", "-f", containerName).Run()
}

// buildOneShotArgs constructs docker run arguments.
// Combines Shannon (ephemeral), claude-code-container (security), claudebox (mounts).
func (d *Daemon) buildOneShotArgs(containerName string, dal *localdal.DalProfile, tr *taskResult) []string {
	hostHome, _ := os.UserHomeDir()
	image := fmt.Sprintf("dalcenter/%s:latest", dal.Player)

	args := []string{
		"run",
		"--rm",                                    // Auto-remove on exit (Shannon)
		"--init",                                  // tini as PID 1 (claudebox)
		"--name", containerName,                   // Unique name (Shannon: task-{id})
		"--cap-drop=ALL",                          // Security (claude-code-container)
		"--security-opt=no-new-privileges:true",   // Security
		"--pids-limit=200",                        // Resource limit
		"--memory=2g",                             // Memory limit
		"--tmpfs", "/tmp:noexec,nosuid,size=100m", // Temp (claude-code-container)
	}

	// ── VOLUMES (claude-code-container pattern: input ro, output rw) ──
	// Workspace (rw)
	args = append(args, "-v", d.serviceRepo+":/workspace:rw")
	// Credentials (ro)
	credPath := filepath.Join(hostHome, ".claude", ".credentials.json")
	if _, err := os.Stat(credPath); err == nil {
		home := "/root"
		if dal.Player == "codex" {
			home = "/root"
		}
		args = append(args, "-v", credPath+":"+home+"/.claude/.credentials.json:ro")
	}
	// Settings (ro)
	settingsPath := "/etc/dalcenter/settings.json"
	if _, err := os.Stat(settingsPath); err == nil {
		args = append(args, "-v", settingsPath+":/root/.claude/settings.json:ro")
	// gh CLI auth (ro)
	ghConfigDir := filepath.Join(hostHome, ".config", "gh")
	if _, err := os.Stat(ghConfigDir); err == nil {
		args = append(args, "-v", ghConfigDir+":/root/.config/gh:ro")
	}
	// git config
	gitConfigFile := filepath.Join(hostHome, ".gitconfig")
	if _, err := os.Stat(gitConfigFile); err == nil {
		args = append(args, "-v", gitConfigFile+":/root/.gitconfig:ro")
	}
	}

	// ── ENVIRONMENT ──
	args = append(args,
		"-e", "DAL_NAME="+dal.Name,
		"-e", "DAL_ROLE="+dal.Role,
		"-e", "DAL_PLAYER="+dal.Player,
		"-e", "CLAUDE_CODE_ENTRYPOINT=dalcli",
		"-e", "HOME=/root",
	)
	// GitHub token
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		args = append(args, "-e", "GITHUB_TOKEN="+token)
	}
	// Cross-repo
	if tr.Repo != "" {
		args = append(args, "-e", "TASK_REPO="+tr.Repo)
	}

	// ── COMMAND ──
	// claude -p with allowed tools, reading task from stdin
	claudeCmd := `cd /workspace && TOOLS="Bash(git:*,gh:*) Read Write Glob Grep Edit" && claude -p --allowedTools "$TOOLS"`
	args = append(args,
		"-i",  // stdin for task pipe
		image,
		"bash", "-c", claudeCmd,
	)

	return args
}

// handleTaskOneShot handles POST /api/task/oneshot — bypasses persistent container logic.
func (d *Daemon) handleTaskOneShot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Task         string `json:"task"`
		Role         string `json:"role"`
		DalName      string `json:"dal_name"`
		Async        bool   `json:"async"`
		CallbackPane string `json:"callback_pane,omitempty"`
		Repo         string `json:"repo,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}
	if req.Task == "" {
		http.Error(w, "task required", 400)
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if req.DalName == "" {
		req.DalName = "dev"
	}

	tr := d.tasks.New(req.DalName, req.Task)
	tr.Repo = req.Repo
	tr.CallbackPane = req.CallbackPane

	if req.Async {
		go d.execTaskOneShot(req.DalName, req.Role, req.Task, tr)
		respondJSON(w, 202, map[string]string{
			"task_id": tr.ID,
			"status":  "running",
		})
		return
	}

	d.execTaskOneShot(req.DalName, req.Role, req.Task, tr)
	status := 200
	if tr.Status == "failed" {
		status = 500
	}
	respondJSON(w, status, tr)
}
