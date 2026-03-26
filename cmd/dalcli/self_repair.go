package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ErrorClass categorizes task failures for self-repair decisions.
type ErrorClass string

const (
	ErrClassEnv          ErrorClass = "env"          // missing binary, wrong dir, permission
	ErrClassDeps         ErrorClass = "deps"         // go mod, npm, cargo dependency issues
	ErrClassGit          ErrorClass = "git"          // merge conflict, detached HEAD
	ErrClassInstructions ErrorClass = "instructions" // stale/wrong instructions
	ErrClassUnknown      ErrorClass = "unknown"
)

// classifyTaskError analyzes combined output to determine error category.
func classifyTaskError(output string) ErrorClass {
	lower := strings.ToLower(output)

	// Environment issues
	envPatterns := []string{
		"command not found", "no such file or directory",
		"permission denied", "not a directory",
		"exec format error", "is not recognized",
	}
	for _, p := range envPatterns {
		if strings.Contains(lower, p) {
			return ErrClassEnv
		}
	}

	// Dependency issues
	depPatterns := []string{
		"go: module", "go mod tidy", "cannot find module",
		"npm err", "module not found", "package not found",
		"cargo build", "unresolved import", "could not compile",
	}
	for _, p := range depPatterns {
		if strings.Contains(lower, p) {
			return ErrClassDeps
		}
	}

	// Git issues
	gitPatterns := []string{
		"merge conflict", "detached head", "head detached", "not a git repository",
		"your branch is behind", "unmerged files",
		"fatal: refusing to merge unrelated",
	}
	for _, p := range gitPatterns {
		if strings.Contains(lower, p) {
			return ErrClassGit
		}
	}

	// Stale instructions
	instrPatterns := []string{
		"instructions.md", "stale instruction",
		"task.md", "issue #",
	}
	matchCount := 0
	for _, p := range instrPatterns {
		if strings.Contains(lower, p) {
			matchCount++
		}
	}
	// Only classify as instructions if multiple indicators present
	if matchCount >= 2 && strings.Contains(lower, "error") {
		return ErrClassInstructions
	}

	return ErrClassUnknown
}

// repairCooldown prevents repeated repair attempts for the same task.
var repairCooldown = struct {
	mu    sync.Mutex
	tasks map[string]time.Time
}{tasks: make(map[string]time.Time)}

const repairCooldownDuration = 5 * time.Minute

func taskHash(task string) string {
	h := sha256.Sum256([]byte(task))
	return fmt.Sprintf("%x", h[:8])
}

func isRepairCoolingDown(task string) bool {
	repairCooldown.mu.Lock()
	defer repairCooldown.mu.Unlock()
	hash := taskHash(task)
	if t, ok := repairCooldown.tasks[hash]; ok {
		if time.Since(t) < repairCooldownDuration {
			return true
		}
	}
	return false
}

func markRepairAttempted(task string) {
	repairCooldown.mu.Lock()
	defer repairCooldown.mu.Unlock()
	repairCooldown.tasks[taskHash(task)] = time.Now()
}

// selfRepair attempts to fix a classified error. Returns whether to retry.
func selfRepair(task, output string, taskErr error) (shouldRetry bool, fix string) {
	if isRepairCoolingDown(task) {
		return false, ""
	}
	markRepairAttempted(task)

	class := classifyTaskError(output)
	log.Printf("[repair] classified as %s", class)

	switch class {
	case ErrClassEnv:
		// Try to fix workspace directory
		if _, err := os.Stat("/workspace"); err == nil {
			fix = "cd /workspace"
			if err := os.Chdir("/workspace"); err == nil {
				return true, fix
			}
		}
		return false, ""

	case ErrClassDeps:
		// Try go mod tidy
		if _, err := os.Stat("/workspace/go.mod"); err == nil {
			cmd := exec.Command("go", "mod", "tidy")
			cmd.Dir = "/workspace"
			if err := cmd.Run(); err == nil {
				return true, "go mod tidy"
			}
		}
		// Try npm install
		if _, err := os.Stat("/workspace/package.json"); err == nil {
			cmd := exec.Command("npm", "install")
			cmd.Dir = "/workspace"
			if err := cmd.Run(); err == nil {
				return true, "npm install"
			}
		}
		return false, ""

	case ErrClassGit:
		cmd := exec.Command("git", "checkout", "main")
		cmd.Dir = "/workspace"
		if err := cmd.Run(); err == nil {
			pull := exec.Command("git", "pull")
			pull.Dir = "/workspace"
			if err := pull.Run(); err == nil {
				return true, "git checkout main && git pull"
			}
		}
		return false, ""

	case ErrClassInstructions, ErrClassUnknown:
		// Cannot self-repair — escalate
		return false, ""
	}

	return false, ""
}

// escalateToHost reports a task failure to the dalcenter daemon for tracking.
func escalateToHost(dalName, task, output, errorClass string) {
	dcURL := os.Getenv("DALCENTER_URL")
	if dcURL == "" {
		dcURL = "http://host.docker.internal:11190"
	}
	body := fmt.Sprintf(`{"dal":%q,"task":%q,"output":%q,"error_class":%q}`,
		dalName, truncate(task, 500), truncate(output, 1000), errorClass)
	resp, err := http.Post(dcURL+"/api/escalate", "application/json", strings.NewReader(body))
	if err != nil {
		log.Printf("[escalate] failed to reach daemon: %v", err)
		return
	}
	resp.Body.Close()
	log.Printf("[escalate] reported to daemon: dal=%s class=%s", dalName, errorClass)
}
