package main

import (
	"fmt"
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

func buildTaskSpec(dalName string, br bridge.Bridge, msg bridge.Message, task string, isDirectMention, isThreadReply, isDM bool) TaskSpec {
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

	if false && isThreadReply && !isDirectMention { // DISABLED
		spec.Prompt = buildThreadContext(br, msg, dalName, task)
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

func buildThreadContext(br bridge.Bridge, newMsg bridge.Message, dalName, latestTask string) string {
	// With matterbridge, thread context comes through the stream, not a separate API call.
	var sb strings.Builder
	sb.WriteString("너는 스레드의 후속 작업만 처리한다.\n")
	sb.WriteString(fmt.Sprintf("이번 요청: %s\n", latestTask))
	sb.WriteString("규칙: 스레드 전체를 재현하지 말고 위 요청만 근거로 간결하게 처리한다.")
	return sb.String()
}

func isStatusOnlyMessage(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "💬 작업 중") ||
		strings.HasPrefix(trimmed, "🔧 자가 수리") ||
		strings.HasPrefix(trimmed, "✅ 작업 완료") ||
		strings.HasPrefix(trimmed, "❌ 실패")
}
