package talk

import (
	"bytes"
	"encoding/json"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Mode determines how Claude is invoked.
type Mode string

const (
	ModeAsk  Mode = "ask"  // claude --print (text response only)
	ModeExec Mode = "exec" // claude -p (tool use enabled)
)

// Executor calls Claude CLI and returns the response.
type Executor struct {
	Binary string
	Role   string
	Cwd    string // working directory (repo root)
}

func NewExecutor(role, cwd string) *Executor {
	binary, err := exec.LookPath("claude")
	if err != nil {
		binary = "claude"
	}
	return &Executor{Binary: binary, Role: role, Cwd: cwd}
}

// Run invokes Claude with the given message and mode.
func (e *Executor) Run(ctx context.Context, mode Mode, message string) (string, error) {
	prompt := message
	if e.Role != "" {
		prompt = fmt.Sprintf("너는 %s 역할이야. %s", e.Role, message)
	}

	var args []string
	switch mode {
	case ModeAsk:
		args = []string{"--print", prompt}
	case ModeExec:
		args = []string{"-p", prompt, "--allowedTools", "Bash,Read,Write,Edit", "--output-format", "stream-json", "--verbose"}
	default:
		return "", fmt.Errorf("unknown mode: %s", mode)
	}

	cmd := exec.CommandContext(ctx, e.Binary, args...)
	cmd.Env = append(os.Environ(), "PATH=/usr/local/bin:/usr/bin:/bin")
	if e.Cwd != "" {
		cmd.Dir = e.Cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("claude %s failed: %s", mode, errMsg)
	}

	if mode == ModeExec {
		return extractResult(stdout.String()), nil
	}
	return strings.TrimSpace(stdout.String()), nil
}

// extractResult parses stream-json output and returns the final result text.
func extractResult(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var msg struct {
			Type   string `json:"type"`
			Result string `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Type == "result" && msg.Result != "" {
			return msg.Result
		}
	}
	return strings.TrimSpace(output)
}

// Sanitizer redacts sensitive content from dal output.
type Sanitizer struct {
	rules []sanitizeRule
}

type sanitizeRule struct {
	pattern *regexp.Regexp
	replace string
}

var defaultRules = []sanitizeRule{
	{regexp.MustCompile(`VK:[a-zA-Z0-9_-]+`), "[VK:***]"},
	{regexp.MustCompile(`Bearer\s+[a-zA-Z0-9._-]+`), "Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)aws_secret_access_key\s*=\s*\S+`), "AWS_SECRET_ACCESS_KEY=[REDACTED]"},
	{regexp.MustCompile(`(?i)(api[_-]?key|token|secret)\s*[:=]\s*["']?\S+`), "${1}=[REDACTED]"},
}

func NewSanitizer() *Sanitizer {
	return &Sanitizer{rules: defaultRules}
}

// Clean applies all rules to the input string.
func (s *Sanitizer) Clean(input string) string {
	result := input
	for _, r := range s.rules {
		result = r.pattern.ReplaceAllString(result, r.replace)
	}
	return result
}
