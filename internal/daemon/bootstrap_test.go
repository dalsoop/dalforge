package daemon

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── #121: Claude Code autoApprove injection ──────────────────

func TestDockerRun_InjectsClaudeSettings(t *testing.T) {
	src := readSource(t, "docker.go")
	if !strings.Contains(src, "autoApprove") {
		t.Fatal("dockerRun must inject Claude settings.json with autoApprove")
	}
}

func TestDockerRun_SettingsOnlyForClaude(t *testing.T) {
	src := readSource(t, "docker.go")
	// settings injection must be conditional on player == "claude"
	if !strings.Contains(src, `dal.Player == "claude"`) {
		t.Fatal("autoApprove injection must be guarded by player == claude")
	}
}

func TestDockerRun_SettingsHasBashPermission(t *testing.T) {
	src := readSource(t, "docker.go")
	if !strings.Contains(src, `Bash(*)`) {
		t.Fatal("settings must include Bash(*) permission")
	}
}

// ── #122: Team members injection in agent config ─────────────

func TestHandleAgentConfig_IncludesTeamMembers(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"leader": {DalName: "leader", BotToken: "tok-l", BotUsername: "dal-leader-lead"},
			"dev":    {DalName: "dev", BotToken: "tok-d", BotUsername: "dal-dev-dev01"},
		},
		channelID: "ch-1",
		mm:        &MattermostConfig{URL: "http://mm:8065"},
	}

	req := httptest.NewRequest("GET", "/api/agent-config/leader", nil)
	req.SetPathValue("name", "leader")
	w := httptest.NewRecorder()
	d.handleAgentConfig(w, req)

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	tm := resp["team_members"]
	if !strings.Contains(tm, "dev=dal-dev-dev01") {
		t.Errorf("team_members should include dev, got %q", tm)
	}
	if strings.Contains(tm, "leader=") {
		t.Error("team_members should not include self (leader)")
	}
}

func TestHandleAgentConfig_NoTeamMembersWhenAlone(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"solo": {DalName: "solo", BotToken: "tok", BotUsername: "dal-solo-s"},
		},
		channelID: "ch-1",
		mm:        &MattermostConfig{URL: "http://mm:8065"},
	}

	req := httptest.NewRequest("GET", "/api/agent-config/solo", nil)
	req.SetPathValue("name", "solo")
	w := httptest.NewRecorder()
	d.handleAgentConfig(w, req)

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["team_members"] != "" {
		t.Errorf("team_members should be empty when alone, got %q", resp["team_members"])
	}
}

func TestHandleAgentConfig_SkipsEmptyBotUsername(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"leader": {DalName: "leader", BotToken: "tok", BotUsername: "dal-leader-x"},
			"ghost":  {DalName: "ghost", BotToken: "tok", BotUsername: ""},
		},
		channelID: "ch-1",
		mm:        &MattermostConfig{URL: "http://mm:8065"},
	}

	req := httptest.NewRequest("GET", "/api/agent-config/leader", nil)
	req.SetPathValue("name", "leader")
	w := httptest.NewRecorder()
	d.handleAgentConfig(w, req)

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	if strings.Contains(resp["team_members"], "ghost") {
		t.Error("team_members should skip members with empty BotUsername")
	}
}

// ── #120: BotUsername stored in Container ─────────────────────

func TestContainerHasBotUsername(t *testing.T) {
	c := &Container{
		DalName:     "dev",
		BotUsername:  "dal-dev-abc123",
	}
	if c.BotUsername == "" {
		t.Fatal("Container must have BotUsername field")
	}
}

func TestDalBotUsername_Format(t *testing.T) {
	cases := []struct {
		name, uuid, want string
	}{
		{"leader", "v2-leader-20260327", "dal-leader-v2lead"},
		{"dev", "dc-codex-dev-20260327", "dal-dev-dccode"},
		{"verifier", "vk-verifier-20260326", "dal-verifier-vkveri"},
	}
	for _, tc := range cases {
		got := dalBotUsername(tc.name, tc.uuid)
		if got != tc.want {
			t.Errorf("dalBotUsername(%q, %q) = %q, want %q", tc.name, tc.uuid, got, tc.want)
		}
	}
}

// ── Orphan cleanup safety ────────────────────────────────────

func TestOrphanCleanup_SkipsWhenNoContainers(t *testing.T) {
	// Verify the code guards against empty containers
	src := readSource(t, "daemon.go")
	if !strings.Contains(src, "no active containers") {
		t.Fatal("orphan cleanup must skip when no active containers (prevents wiping all bots)")
	}
}

func TestOrphanCleanup_DelayedExecution(t *testing.T) {
	src := readSource(t, "daemon.go")
	if !strings.Contains(src, "time.Sleep") {
		t.Fatal("orphan cleanup must be delayed to wait for reconcile")
	}
}

func TestOrphanCleanup_IncludesDalcenterAdmin(t *testing.T) {
	src := readSource(t, "daemon.go")
	if !strings.Contains(src, `"dalcenter-admin"`) {
		t.Fatal("orphan cleanup must always keep dalcenter-admin active")
	}
}

// ── Sleep bot cleanup ────────────────────────────────────────

func TestSleep_RemovesBotFromChannel(t *testing.T) {
	src := readSource(t, "daemon.go")
	if !strings.Contains(src, "RemoveBotFromChannel") {
		t.Fatal("sleep must call RemoveBotFromChannel")
	}
}

func TestSleep_HidesBotDM(t *testing.T) {
	src := readSource(t, "daemon.go")
	if !strings.Contains(src, "HideBotDMFromUsers") {
		t.Fatal("sleep must call HideBotDMFromUsers to hide DM from sidebar")
	}
}

// ── Bot welcome DM cleanup ───────────────────────────────────

func TestSetupBot_CleansWelcomeDM(t *testing.T) {
	src := readSource(t, "../talk/bot.go")
	if !strings.Contains(src, "cleanupBotWelcomeDMs") {
		t.Fatal("SetupBot must call cleanupBotWelcomeDMs after adding bot to channel")
	}
}

func TestCleanupBotWelcomeDMs_TargetsCorrectMessage(t *testing.T) {
	src := readSource(t, "../talk/bot.go")
	if !strings.Contains(src, "Please add me to teams") {
		t.Fatal("cleanupBotWelcomeDMs must target 'Please add me to teams' message")
	}
}

// ── Bot token management ─────────────────────────────────────

func TestSetupBot_LimitsTokenCount(t *testing.T) {
	src := readSource(t, "../talk/bot.go")
	if !strings.Contains(src, "keep max 2 tokens") {
		t.Fatal("SetupBot must limit token count to prevent accumulation")
	}
}

func TestRemoveBotFromChannel_Exists(t *testing.T) {
	src := readSource(t, "../talk/bot.go")
	if !strings.Contains(src, "func RemoveBotFromChannel(") {
		t.Fatal("RemoveBotFromChannel must exist")
	}
}

func TestHideBotDMFromUsers_Exists(t *testing.T) {
	src := readSource(t, "../talk/bot.go")
	if !strings.Contains(src, "func HideBotDMFromUsers(") {
		t.Fatal("HideBotDMFromUsers must exist")
	}
}

func TestCleanupOrphanBotDMs_Exists(t *testing.T) {
	src := readSource(t, "../talk/bot.go")
	if !strings.Contains(src, "func CleanupOrphanBotDMs(") {
		t.Fatal("CleanupOrphanBotDMs must exist")
	}
}

// ── Member → Leader reporting ────────────────────────────────







// ── Bridge: GetUsername ──────────────────────────────────────


// ── helper ───────────────────────────────────────────────────

func readSource(t *testing.T, relPath string) string {
	t.Helper()
	absPath := filepath.Join(".", relPath)
	// Try relative to package dir
	data, err := os.ReadFile(absPath)
	if err != nil {
		// Try from internal/daemon/
		data, err = os.ReadFile(filepath.Join("..", relPath))
		if err != nil {
			t.Fatalf("cannot read %s: %v", relPath, err)
		}
	}
	return string(data)
}



// ── MATTERMOST_URL 환경변수 주입 ─────────────────────────

func TestMattermostURLExported(t *testing.T) {
	src := readSource(t, "daemon.go")
	if !strings.Contains(src, "DALCENTER_MM_URL") {
		t.Fatal("daemon must export DALCENTER_MM_URL for containers")
	}
}

func TestDockerRun_InjectsMattermostURL(t *testing.T) {
	src := readSource(t, "docker.go")
	if !strings.Contains(src, "MATTERMOST_URL") {
		t.Fatal("dockerRun must inject MATTERMOST_URL into container")
	}
}

// ── restart 핸들러 ───────────────────────────────────────

func TestRestartHandler_Exists(t *testing.T) {
	src := readSource(t, "daemon.go")
	if !strings.Contains(src, "handleRestart") {
		t.Fatal("daemon must have handleRestart handler")
	}
}

func TestRestartHandler_Pipeline(t *testing.T) {
	src := readSource(t, "daemon.go")
	// handleRestart 이후 200자 내에 dockerStop과 handleWake가 있어야 함
	idx := strings.Index(src, "func (d *Daemon) handleRestart")
	if idx < 0 {
		t.Fatal("handleRestart not found")
	}
	block := src[idx : idx+500]
	if !strings.Contains(block, "dockerStop") {
		t.Fatal("handleRestart must call dockerStop")
	}
	if !strings.Contains(block, "handleWake") {
		t.Fatal("handleRestart must call handleWake")
	}
}

func TestRestartRoute_Registered(t *testing.T) {
	src := readSource(t, "daemon.go")
	if !strings.Contains(src, "/api/restart/") {
		t.Fatal("must register POST /api/restart/{name} route")
	}
}

func TestClientRestart_Exists(t *testing.T) {
	src := readSource(t, "client.go")
	if !strings.Contains(src, "func (c *Client) Restart(") {
		t.Fatal("Client must have Restart method")
	}
}

func TestClientAgentConfig_Exists(t *testing.T) {
	src := readSource(t, "client.go")
	if !strings.Contains(src, "func (c *Client) AgentConfig(") {
		t.Fatal("Client must have AgentConfig method")
	}
}
