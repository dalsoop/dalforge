package daemon

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// enforceWakeRules checks rules before allowing a dal to wake.
// Returns error if wake should be blocked.
func (d *Daemon) enforceWakeRules(name, role string) error {
	// Rule: leader-only-persistent
	if role != "leader" {
		return fmt.Errorf("dal %q (role=%s) cannot be woken as persistent — only leader can be persistent. Use POST /api/task with oneshot=true", name, role)
	}
	return nil
}

// enforceTaskRules checks rules before executing a task.
// Returns error if task should be blocked.
func (d *Daemon) enforceTaskRules(dalName string, isOneshot bool) error {
	// Rule: oneshot-for-members
	d.mu.RLock()
	c, exists := d.containers[dalName]
	d.mu.RUnlock()

	if exists && c.Role != "leader" && !isOneshot {
		return fmt.Errorf("dal %q is a member — must use oneshot=true", dalName)
	}
	return nil
}

// enforceGitRules checks git rules before auto-commit/push.
// Called from autoGitWorkflow.
func enforceGitRules(containerID, issueNumber string) error {
	// Rule: no-duplicate-pr
	if issueNumber != "" {
		out, err := exec.Command("docker", "exec", containerID,
			"gh", "pr", "list", "--state", "open", "--search", issueNumber,
			"--json", "number", "--jq", "length").CombinedOutput()
		if err == nil {
			count := strings.TrimSpace(string(out))
			if count != "0" && count != "" {
				return fmt.Errorf("PR already exists for issue #%s — push to existing branch instead of creating new PR", issueNumber)
			}
		}
	}
	return nil
}

// enforceChannelRules checks MM channel exists before wake.
// Returns error if channel/webhook is missing.
func (d *Daemon) enforceChannelRules(teamName string) error {
	if d.pipeline == nil || !d.pipeline.configured() || d.pipeline.mmToken == "" {
		// MM not configured — skip channel enforcement
		return nil
	}

	// Rule: channel-required-for-wake
	// Check channel exists via MM API
	// (lightweight check — just verify we can post)
	log.Printf("[rules] channel check for team %q — MM configured, check skipped (TODO: implement)", teamName)
	return nil
}

// logRuleViolation logs a rule violation for auditing.
func logRuleViolation(rule, detail string) {
	log.Printf("[rules] VIOLATION: %s — %s", rule, detail)
}
