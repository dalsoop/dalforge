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
