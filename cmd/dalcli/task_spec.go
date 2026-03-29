package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dalsoop/dalcenter/internal/bridge"
)

type TaskIntent string

const (
	TaskIntentExecute          TaskIntent = "execute"
	TaskIntentCredentialStatus TaskIntent = "credential_status"
)

type TaskState string

const (
	TaskStateDone    TaskState = "done"
	TaskStateBlocked TaskState = "blocked"
	TaskStateFailed  TaskState = "failed"
	TaskStateNoop    TaskState = "noop"
)

type TaskSpec struct {
	Intent      TaskIntent
	UserTask    string
	Prompt      string
	ThreadID    string
	Channel     string
	Source      string
	DoneWhen    string
	ReplyFormat string
}

type TaskResult struct {
	State  TaskState
	Output string
	Err    error
}

func buildTaskSpec(dalName string, mm *bridge.MattermostBridge, msg bridge.Message, task string, isDirectMention, isThreadReply, isDM bool) TaskSpec {
	threadID := msg.RootID
	if threadID == "" {
		threadID = msg.ID
	}

	spec := TaskSpec{
		Intent:      TaskIntentExecute,
		UserTask:    task,
		Prompt:      task,
		ThreadID:    threadID,
		Channel:     msg.Channel,
		Source:      "channel",
		DoneWhen:    "요청이 완료되거나, 막힌 이유를 명확히 설명할 수 있을 때 종료한다.",
		ReplyFormat: "간결한 최종 결과만 답한다.",
	}

	switch {
	case isThreadReply:
		spec.Source = "thread"
	case isDM:
		spec.Source = "dm"
	case isDirectMention:
		spec.Source = "mention"
	}

	if isThreadReply && !isDirectMention {
		spec.Prompt = buildThreadContext(mm, msg, dalName, task)
	}
	if isCredentialStatusQuery(credentialStatusQueryInput(task, spec.Prompt)) {
		spec.Intent = TaskIntentCredentialStatus
	}
	return spec
}

func runTaskSpec(spec TaskSpec) TaskResult {
	if spec.Intent != TaskIntentExecute {
		return TaskResult{State: TaskStateNoop}
	}
	output, err := executeTask(spec.Prompt)
	if err != nil {
		return TaskResult{State: TaskStateFailed, Output: output, Err: err}
	}
	return TaskResult{State: TaskStateDone, Output: output}
}

func buildThreadContext(mm *bridge.MattermostBridge, newMsg bridge.Message, dalName, latestTask string) string {
	rootTask := fetchThreadRootTask(mm, newMsg, dalName)
	var sb strings.Builder
	sb.WriteString("너는 Mattermost 스레드의 후속 작업만 처리한다.\n")
	if rootTask != "" && rootTask != latestTask {
		sb.WriteString(fmt.Sprintf("원래 요청: %s\n", rootTask))
	}
	sb.WriteString(fmt.Sprintf("이번 요청: %s\n", latestTask))
	sb.WriteString("규칙: 스레드 전체를 재현하지 말고 위 요청만 근거로 간결하게 처리한다.")
	return sb.String()
}

func fetchThreadRootTask(mm *bridge.MattermostBridge, newMsg bridge.Message, dalName string) string {
	threadID := newMsg.RootID
	if threadID == "" {
		threadID = newMsg.ID
	}

	agentCfg, _ := fetchAgentConfig(dalName)
	if agentCfg == nil || agentCfg.MMURL == "" || agentCfg.BotToken == "" {
		return ""
	}

	url := fmt.Sprintf("%s/api/v4/posts/%s/thread", agentCfg.MMURL, threadID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+agentCfg.BotToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var thread struct {
		Order []string                   `json:"order"`
		Posts map[string]json.RawMessage `json:"posts"`
	}
	if json.Unmarshal(body, &thread) != nil {
		return ""
	}

	for _, pid := range thread.Order {
		var post struct {
			UserID  string `json:"user_id"`
			Message string `json:"message"`
		}
		if json.Unmarshal(thread.Posts[pid], &post) != nil {
			continue
		}
		if post.UserID == mm.BotUserID {
			continue
		}
		if isOperationalNoticeMessage(post.Message) || isStatusOnlyMessage(post.Message) {
			continue
		}
		return strings.TrimSpace(post.Message)
	}

	return ""
}

func isStatusOnlyMessage(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "💬 작업 중") ||
		strings.HasPrefix(trimmed, "🔧 자가 수리") ||
		strings.HasPrefix(trimmed, "✅ 작업 완료") ||
		strings.HasPrefix(trimmed, "❌ 실패")
}
