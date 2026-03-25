package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dalsoop/dalcenter/internal/bridge"
	"github.com/spf13/cobra"
)

// agentConfig holds MM connection info fetched from dalcenter daemon.
type agentConfig struct {
	DalName   string `json:"dal_name"`
	BotToken  string `json:"bot_token"`
	ChannelID string `json:"channel_id"`
	MMURL     string `json:"mm_url"`
}

func runCmd(dalName string) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Start agent loop — poll Mattermost, execute tasks via Claude, report back",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentLoop(dalName)
		},
	}
}

func runAgentLoop(dalName string) error {
	log.Printf("[agent] starting agent loop for %s", dalName)

	// 1. Fetch MM config from dalcenter daemon
	cfg, err := fetchAgentConfig(dalName)
	if err != nil {
		return fmt.Errorf("fetch agent config: %w", err)
	}
	if cfg.BotToken == "" || cfg.MMURL == "" || cfg.ChannelID == "" {
		return fmt.Errorf("incomplete agent config: mm_url=%q bot_token_set=%v channel_id=%q",
			cfg.MMURL, cfg.BotToken != "", cfg.ChannelID)
	}
	log.Printf("[agent] connected: mm=%s channel=%s", cfg.MMURL, cfg.ChannelID[:8])

	// 2. Connect to Mattermost
	mm := bridge.NewMattermostBridge(cfg.MMURL, cfg.BotToken, cfg.ChannelID, 5*time.Second)
	if err := mm.Connect(); err != nil {
		return fmt.Errorf("mattermost connect: %w", err)
	}
	defer mm.Close()

	log.Printf("[agent] listening for tasks...")

	// 3. Listen for messages
	mention := fmt.Sprintf("@dal-%s", dalName)
	assignPrefix := "작업 지시:"

	for msg := range mm.Listen() {
		// Skip own messages
		if msg.From == mm.BotUserID {
			continue
		}

		// Check if this message mentions this dal
		if !strings.Contains(msg.Content, mention) {
			continue
		}

		// Extract task from "작업 지시: <task>" pattern
		task := extractTask(msg.Content, assignPrefix)
		if task == "" {
			continue
		}

		log.Printf("[agent] task received: %s", truncate(task, 80))

		// 4. Reply: starting
		mm.Send(bridge.Message{
			Content: fmt.Sprintf("작업 시작합니다: %s", truncate(task, 100)),
			ReplyTo: msg.ID,
		})

		// 5. Execute via Claude Code
		output, err := executeTask(task)
		if err != nil {
			log.Printf("[agent] task failed: %v", err)
			mm.Send(bridge.Message{
				Content: fmt.Sprintf("❌ 작업 실패: %v\n```\n%s\n```", err, truncate(output, 500)),
				ReplyTo: msg.ID,
			})
			continue
		}

		log.Printf("[agent] task completed (%d bytes output)", len(output))

		// 6. Report completion
		report := formatReport(output)
		mm.Send(bridge.Message{
			Content: report,
			ReplyTo: msg.ID,
		})
	}

	return nil
}

func fetchAgentConfig(dalName string) (*agentConfig, error) {
	dcURL := os.Getenv("DALCENTER_URL")
	if dcURL == "" {
		return nil, fmt.Errorf("DALCENTER_URL not set")
	}
	resp, err := http.Get(dcURL + "/api/agent-config/" + dalName)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	var cfg agentConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func executeTask(task string) (string, error) {
	role := os.Getenv("DAL_ROLE")

	var cmd *exec.Cmd
	if role == "leader" {
		// Leader needs tool access (Bash) to run dalcli-leader assign.
		// Pipe task via stdin with full permissions.
		cmd = exec.Command("claude", "--dangerously-skip-permissions", "-p", task)
	} else {
		// Members use print mode (read-only analysis).
		cmd = exec.Command("claude", "-p", task)
	}

	cmd.Dir = "/workspace"
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_ENTRYPOINT=dalcli")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func extractTask(content, prefix string) string {
	idx := strings.Index(content, prefix)
	if idx < 0 {
		return ""
	}
	task := strings.TrimSpace(content[idx+len(prefix):])
	return task
}

func formatReport(output string) string {
	if len(output) > 3000 {
		output = output[:1500] + "\n\n... (truncated) ...\n\n" + output[len(output)-1500:]
	}
	return fmt.Sprintf("✅ 작업 완료\n```\n%s\n```", output)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
