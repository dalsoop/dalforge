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
	"sync"
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

	// Track active threads this dal is participating in
	var activeThreads sync.Map // threadID → true

	for msg := range mm.Listen() {
		// Skip own messages
		if msg.From == mm.BotUserID {
			continue
		}

		// Determine message type
		isDirectMention := strings.Contains(msg.Content, mention)
		isAssignment := isDirectMention && strings.Contains(msg.Content, assignPrefix)
		isThreadReply := msg.RootID != "" && isActiveThread(&activeThreads, msg.RootID)

		if !isAssignment && !isDirectMention && !isThreadReply {
			continue
		}

		// --- Case 1: New task assignment ---
		if isAssignment {
			task := extractTask(msg.Content, assignPrefix)
			if task == "" {
				continue
			}

			// Track this thread
			threadID := msg.ID // new thread starts from this message
			activeThreads.Store(threadID, true)

			log.Printf("[agent] task received: %s", truncate(task, 80))

			mm.Send(bridge.Message{
				Content: fmt.Sprintf("작업 시작합니다: %s", truncate(task, 100)),
				ReplyTo: msg.ID,
			})

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
			mm.Send(bridge.Message{
				Content: formatReport(output),
				ReplyTo: msg.ID,
			})
			continue
		}

		// --- Case 2: Thread reply (conversation in existing thread) ---
		if isThreadReply || isDirectMention {
			threadID := msg.RootID
			if threadID == "" {
				threadID = msg.ID
			}
			activeThreads.Store(threadID, true)

			// Build context: read thread history + new message
			context := buildThreadContext(mm, msg, dalName)

			log.Printf("[agent] thread reply from %s: %s", msg.From[:8], truncate(msg.Content, 80))

			mm.Send(bridge.Message{
				Content: "💬 응답 중...",
				ReplyTo: threadID,
			})

			output, err := executeTask(context)
			if err != nil {
				log.Printf("[agent] thread reply failed: %v", err)
				mm.Send(bridge.Message{
					Content: fmt.Sprintf("❌ 응답 실패: %v", err),
					ReplyTo: threadID,
				})
				continue
			}

			// Thread replies: no code block wrapping, natural conversation
			mm.Send(bridge.Message{
				Content: truncate(strings.TrimSpace(output), 3000),
				ReplyTo: threadID,
			})
			continue
		}
	}

	return nil
}

// isActiveThread checks if the dal is participating in this thread.
func isActiveThread(threads *sync.Map, threadID string) bool {
	_, ok := threads.Load(threadID)
	return ok
}

// buildThreadContext reads thread history and builds a prompt for Claude
// so it can respond with full conversation context.
func buildThreadContext(mm *bridge.MattermostBridge, newMsg bridge.Message, dalName string) string {
	var sb strings.Builder
	sb.WriteString("너는 Mattermost 스레드에서 팀원과 대화 중이다. ")
	sb.WriteString("아래는 스레드 대화 내역이다. 마지막 메시지에 대해 네 역할에 맞게 응답하라.\n\n")

	// Fetch thread posts if possible
	threadID := newMsg.RootID
	if threadID == "" {
		threadID = newMsg.ID
	}

	sb.WriteString(fmt.Sprintf("--- 스레드 ---\n"))
	sb.WriteString(fmt.Sprintf("[상대방]: %s\n", newMsg.Content))
	sb.WriteString(fmt.Sprintf("---\n\n"))
	sb.WriteString(fmt.Sprintf("너의 이름: %s\n", dalName))
	sb.WriteString("위 메시지에 대해 너의 역할과 전문성에 맞게 응답하라. 간결하게.")

	return sb.String()
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
		// Leader needs Bash for delegation + Write/Edit for file output.
		cmd = exec.Command("claude",
			"--allowedTools", "Bash(dalcli-leader:*) Read Write Glob Grep Edit",
			"--print", task)
	} else {
		// Members: read + write for reports and file creation.
		cmd = exec.Command("claude",
			"--allowedTools", "Read Write Glob Grep Edit",
			"--print", task)
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
