package main

import (
	"os"
	"strings"
	"testing"
)

func readSrc(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("cannot read %s: %v", file, err)
	}
	return string(data)
}

func TestMemberReportsToLeader(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "reportToLeader") {
		t.Fatal("member dal must call reportToLeader on direct user tasks")
	}
}

func TestReportToLeader_ChecksRole(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, `role == "member"`) {
		t.Fatal("reportToLeader must only trigger for member role")
	}
}

func TestReportToLeader_SkipsLeaderMessages(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "isFromLeader") {
		t.Fatal("must check isFromLeader to avoid report loops")
	}
}

func TestIsFromLeader_ChecksUsername(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, `"leader"`) {
		t.Fatal("isFromLeader must check for 'leader' in username")
	}
}

func TestTeamMembersEnvUsed(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "DAL_TEAM_MEMBERS") {
		t.Fatal("must use DAL_TEAM_MEMBERS env for leader mention")
	}
}

func TestAgentConfig_HasTeamMembersField(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "TeamMembers") {
		t.Fatal("agentConfig must have TeamMembers field")
	}
}

// ── DM 지원 테스트 ────────────────────────────────────────

func TestDM_IsDetected(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "isDM") {
		t.Fatal("must detect DM messages (isDM variable)")
	}
}

func TestDM_DifferentChannelID(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "msg.Channel != cfg.ChannelID") {
		t.Fatal("isDM must check msg.Channel != cfg.ChannelID")
	}
}

func TestDM_BypassesMentionCheck(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "!isDM") {
		t.Fatal("DM must bypass mention check (isDM in filter condition)")
	}
}

func TestDM_AllSendCallsHaveChannel(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	// mm.Send에서 ReplyTo가 있는 블록은 Channel도 있어야 함
	// 최소 3곳: 상태, 에러, 응답
	count := strings.Count(src, "Channel: msg.Channel,")
	if count < 3 {
		t.Fatalf("expected at least 3 Send calls with Channel: msg.Channel, got %d", count)
	}
}

func TestDM_ResponseIncludesChannel(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	// 최종 응답에도 Channel이 전달되어야 함
	if !strings.Contains(src, "Channel: msg.Channel,") {
		t.Fatal("response mm.Send must include Channel: msg.Channel")
	}
}

// ── 타임아웃 테스트 ──────────────────────────────────────

func TestTimeout_ContextUsed(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "context.WithTimeout") {
		t.Fatal("runClaude must use context.WithTimeout")
	}
}

func TestTimeout_DefaultDuration(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "5 * time.Minute") {
		t.Fatal("default timeout must be 5 minutes")
	}
}

func TestTimeout_EnvOverride(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "DAL_MAX_DURATION") {
		t.Fatal("timeout must be configurable via DAL_MAX_DURATION")
	}
}

func TestTimeout_DeadlineExceeded(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "DeadlineExceeded") {
		t.Fatal("must check context.DeadlineExceeded for timeout detection")
	}
}

func TestTimeout_ErrorMessage(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "TIMEOUT") {
		t.Fatal("timeout error must contain TIMEOUT keyword")
	}
}

func TestTimeout_CommandContext(t *testing.T) {
	src := readSrc(t, "cmd_run.go")
	if !strings.Contains(src, "exec.CommandContext") {
		t.Fatal("must use exec.CommandContext for cancellable execution")
	}
}

// ── 크로스 채널 폴링 테스트 ──────────────────────────────

func TestBridge_PollsAllChannelTypes(t *testing.T) {
	src := readSrc(t, "../../internal/bridge/mattermost.go")
	// DM + 일반 채널 + 비공개 채널 모두 폴링
	for _, chType := range []string{`"D"`, `"O"`, `"P"`} {
		if !strings.Contains(src, chType) {
			t.Fatalf("bridge must poll channel type %s", chType)
		}
	}
}

func TestBridge_SkipsMainChannel(t *testing.T) {
	src := readSrc(t, "../../internal/bridge/mattermost.go")
	if !strings.Contains(src, "m.ChannelID") {
		t.Fatal("must skip main channel in extra polling (already polled)")
	}
}

func TestBridge_PerChannelLastAt(t *testing.T) {
	src := readSrc(t, "../../internal/bridge/mattermost.go")
	if !strings.Contains(src, "dmLastAt") {
		t.Fatal("must have per-channel lastAt tracking (dmLastAt)")
	}
}

func TestBridge_FetchChannelLatestAt(t *testing.T) {
	src := readSrc(t, "../../internal/bridge/mattermost.go")
	if !strings.Contains(src, "fetchChannelLatestAt") {
		t.Fatal("must have fetchChannelLatestAt for initial sinceAt")
	}
}
