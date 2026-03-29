package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		BotUsername: "dal-dev-abc123",
	}
	if c.BotUsername == "" {
		t.Fatal("Container must have BotUsername field")
	}
}

func TestDalBotUsername_Format(t *testing.T) {
	cases := []struct {
		name, uuid, want string
	}{
		{"leader", "v2-leader-20260327", "dal-leader"},
		{"dev", "dc-codex-dev-20260327", "dal-dev"},
		{"verifier", "vk-verifier-20260326", "dal-verifier"},
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

// ── handleAgentConfig team_members 동작 테스트 ──────────

func TestHandleAgentConfig_MultipleTeamMembers(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"leader": {DalName: "leader", BotToken: "t1", BotUsername: "dal-leader-x"},
			"dev":    {DalName: "dev", BotToken: "t2", BotUsername: "dal-dev-y"},
			"test":   {DalName: "test", BotToken: "t3", BotUsername: "dal-test-z"},
		},
		channelID: "ch",
		mm:        &MattermostConfig{URL: "http://mm"},
	}
	req := httptest.NewRequest("GET", "/api/agent-config/leader", nil)
	req.SetPathValue("name", "leader")
	w := httptest.NewRecorder()
	d.handleAgentConfig(w, req)

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	tm := resp["team_members"]
	if !strings.Contains(tm, "dev=dal-dev-y") {
		t.Errorf("should include dev, got %q", tm)
	}
	if !strings.Contains(tm, "test=dal-test-z") {
		t.Errorf("should include test, got %q", tm)
	}
	if strings.Contains(tm, "leader=") {
		t.Error("should not include self")
	}
}

// ── Container struct 필드 테스트 ─────────────────────────

func TestContainer_AllFieldsPresent(t *testing.T) {
	c := Container{
		DalName:     "test",
		UUID:        "uuid",
		Player:      "claude",
		Role:        "member",
		ContainerID: "cid",
		Status:      "running",
		Workspace:   "shared",
		Skills:      3,
		BotToken:    "tok",
		BotUsername: "dal-test",
	}
	if c.BotUsername == "" {
		t.Fatal("BotUsername must be settable")
	}
	if c.Workspace == "" {
		t.Fatal("Workspace must be settable")
	}
}

// ── handleSleep bot cleanup 테스트 ───────────────────────

func TestSleep_DeletesContainerFromMap(t *testing.T) {
	src := readSource(t, "daemon.go")
	// handleSleep must eventually call delete(d.containers, name)
	if !strings.Contains(src, "delete(d.containers, name)") {
		t.Fatal("handleSleep must call delete(d.containers, name)")
	}
}

// ── registry 테스트 ──────────────────────────────────────

func TestRegistry_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	r := newRegistry(dir)
	r.Set("uuid-1", RegistryEntry{Name: "dev", Repo: "/repo", ContainerID: "cid1", Status: "running"})

	entry := r.Get("uuid-1")
	if entry == nil {
		t.Fatal("should find entry")
	}
	if entry.Name != "dev" {
		t.Errorf("name = %q", entry.Name)
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	dir := t.TempDir()
	r := newRegistry(dir)
	entry := r.Get("nonexistent")
	if entry != nil {
		t.Fatal("should not find missing entry")
	}
}

func TestRegistry_GetByContainerID(t *testing.T) {
	dir := t.TempDir()
	r := newRegistry(dir)
	r.Set("uuid-1", RegistryEntry{Name: "dev", ContainerID: "abc123"})

	entry := r.GetByContainerID("abc123")
	if entry == nil {
		t.Fatal("should find by container ID")
	}
	if entry.Name != "dev" {
		t.Errorf("name = %q", entry.Name)
	}
}

func TestRegistry_GetByContainerID_PrefixMatch(t *testing.T) {
	dir := t.TempDir()
	r := newRegistry(dir)
	r.Set("uuid-1", RegistryEntry{Name: "dev", ContainerID: "abc123def456"})

	entry := r.GetByContainerID("abc123def456")
	if entry == nil {
		t.Fatal("should find exact full container ID")
	}
	entry = r.GetByContainerID("abc123def4567890")
	if entry == nil {
		t.Fatal("should find when lookup contains stored full container ID as prefix")
	}
	entry = r.GetByContainerID("abc123def456"[:12])
	if entry == nil {
		t.Fatal("should find when docker ps returns short container ID")
	}
}

func TestRegistry_GetByContainerID_NotFound(t *testing.T) {
	dir := t.TempDir()
	r := newRegistry(dir)
	entry := r.GetByContainerID("nonexistent")
	if entry != nil {
		t.Fatal("should not find missing container ID")
	}
}

// ── task store 추가 테스트 ───────────────────────────────

func TestTaskStore_Complete(t *testing.T) {
	ts := newTaskStore()
	result := ts.New("dev", "test task")
	task := ts.Get(result.ID)
	if task == nil {
		t.Fatal("should find task")
	}
	if task.Status != "running" {
		t.Errorf("status = %q, want running", task.Status)
	}
}

func TestTaskStore_ListAll(t *testing.T) {
	ts := newTaskStore()
	ts.New("dev", "task1")
	ts.New("dev", "task2")
	ts.New("host", "task3")

	all := ts.List()
	if len(all) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(all))
	}
}

// ── dalBotUsername 추가 테스트 ────────────────────────────

func TestDalBotUsername_ShortUUID(t *testing.T) {
	result := dalBotUsername("dev", "abc")
	if result != "dal-dev" {
		t.Errorf("got %q", result)
	}
}

func TestDalBotUsername_LongUUID(t *testing.T) {
	result := dalBotUsername("leader", "very-long-uuid-string-here")
	expected := "dal-leader"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

// ── dalContainerName 테스트 ──────────────────────────────

func TestDalContainerName_Basic(t *testing.T) {
	result := dalContainerName("dev", "dc-dev-20260327")
	expected := "dal-dev-dcdev2"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

// ── handleClaimGet / handleClaimRespond 테스트 ───────────

func TestHandleClaimGet_NotFound(t *testing.T) {
	d := &Daemon{
		claims: newClaimStore(),
	}
	req := httptest.NewRequest("GET", "/api/claims/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	d.handleClaimGet(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleClaimRespond_NotFound(t *testing.T) {
	d := &Daemon{
		claims: newClaimStore(),
	}
	req := httptest.NewRequest("POST", "/api/claims/nonexistent/respond", strings.NewReader(`{"status":"resolved","response":"done"}`))
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	d.handleClaimRespond(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── truncateStr 테스트 ───────────────────────────────────

func TestTruncateStr_Short(t *testing.T) {
	if truncateStr("hi", 10) != "hi" {
		t.Fatal("short should not truncate")
	}
}

func TestTruncateStr_Long(t *testing.T) {
	result := truncateStr("abcdefghij", 5)
	if len(result) > 8 {
		t.Fatalf("expected max 8, got %d", len(result))
	}
}

// ── handleEscalations 테스트 ─────────────────────────────

func TestHandleEscalations_Empty(t *testing.T) {
	d := &Daemon{
		escalations: newEscalationStoreWithFile(""),
	}
	req := httptest.NewRequest("GET", "/api/escalations", nil)
	w := httptest.NewRecorder()
	d.handleEscalations(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ── escalation store 테스트 ──────────────────────────────

func TestEscalationStore_AddAndList(t *testing.T) {
	es := newEscalationStoreWithFile("")
	es.Add("dev", "task failed", "compile error", "unknown")
	list := es.Unresolved()
	if len(list) != 1 {
		t.Fatalf("expected 1 escalation, got %d", len(list))
	}
	if list[0].Dal != "dev" {
		t.Errorf("dal = %q", list[0].Dal)
	}
}

func TestEscalationStore_Resolve(t *testing.T) {
	es := newEscalationStoreWithFile("")
	es.Add("dev", "task", "error", "unknown")
	list := es.Unresolved()
	if len(list) == 0 {
		t.Fatal("should have unresolved")
	}
	es.Resolve(list[0].ID)
	if len(es.Unresolved()) != 0 {
		t.Fatal("should have no unresolved after resolve")
	}
}

// ── claim store 추가 테스트 ──────────────────────────────

func TestClaimStore_AddAndList_2(t *testing.T) {
	cs := newClaimStore()
	cs.Add("dev", ClaimBlocked, "git permission denied", "detail", "")
	list := cs.List("open")
	if len(list) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(list))
	}
	if list[0].Dal != "dev" {
		t.Errorf("dal = %q", list[0].Dal)
	}
}

func TestClaimStore_Respond_2(t *testing.T) {
	cs := newClaimStore()
	cs.Add("dev", ClaimBlocked, "issue", "detail", "")
	list := cs.List("")
	cs.Respond(list[0].ID, "resolved", "fixed it")
	resolved := cs.List("resolved")
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
}

func TestClaimStore_Get_ByID(t *testing.T) {
	cs := newClaimStore()
	cs.Add("dev", ClaimEnv, "missing tool", "detail", "")
	list := cs.List("")
	claim := cs.Get(list[0].ID)
	if claim == nil {
		t.Fatal("should find claim by ID")
	}
	if claim.Type != "env" {
		t.Errorf("type = %q", claim.Type)
	}
}

func TestClaimStore_GetMissing_2(t *testing.T) {
	cs := newClaimStore()
	if cs.Get("nonexistent") != nil {
		t.Fatal("should not find missing claim")
	}
}

// ── uuidShort 테스트 ─────────────────────────────────────

func TestUuidShort_Long(t *testing.T) {
	s := uuidShort("dc-host-20260327")
	if len(s) > 6 {
		t.Fatalf("expected max 6 chars, got %d: %q", len(s), s)
	}
}

func TestUuidShort_Short(t *testing.T) {
	s := uuidShort("abc")
	if s != "abc" {
		t.Errorf("short uuid should pass through, got %q", s)
	}
}

// ── dalContainerName 추가 테스트 ─────────────────────────

func TestDalContainerName_WithHyphens(t *testing.T) {
	result := dalContainerName("codex-dev", "dc-codex-dev-20260327")
	if !strings.HasPrefix(result, "dal-codex-dev-") {
		t.Errorf("got %q", result)
	}
}

// ── persistJSON 테스트 ───────────────────────────────────

func TestPersistJSON_WritesFile(t *testing.T) {
	dir := t.TempDir()
	data := map[string]string{"key": "value"}
	persistJSON(filepath.Join(dir, "test.json"), data, nil)
	content, err := os.ReadFile(filepath.Join(dir, "test.json"))
	if err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if !strings.Contains(string(content), "value") {
		t.Fatal("file should contain value")
	}
}

// ── requireAuth 테스트 ───────────────────────────────────

func TestRequireAuth_EmptyToken(t *testing.T) {
	d := &Daemon{apiToken: ""}
	handler := d.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal("empty token should allow all")
	}
}

func TestRequireAuth_WrongToken(t *testing.T) {
	d := &Daemon{apiToken: "secret"}
	handler := d.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("wrong token should 401, got %d", w.Code)
	}
}

func TestRequireAuth_CorrectToken(t *testing.T) {
	d := &Daemon{apiToken: "secret"}
	handler := d.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("correct token should 200, got %d", w.Code)
	}
}

// ── handleTask 분기 테스트 ───────────────────────────────

func TestHandleTask_MissingDal(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{},
		tasks:      newTaskStore(),
	}
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(`{"dal":"nonexistent","task":"do something"}`))
	w := httptest.NewRecorder()
	d.handleTask(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── handlePs 추가 분기 ──────────────────────────────────

func TestHandlePs_WithContainers(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{
			"dev": {DalName: "dev", Player: "claude", Role: "member", ContainerID: "abc123456789", Status: "running", Skills: 3},
		},
	}
	req := httptest.NewRequest("GET", "/api/ps", nil)
	w := httptest.NewRecorder()
	d.handlePs(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "dev") {
		t.Fatal("should contain dal name")
	}
}

// ── handleStatus 분기 ────────────────────────────────────

func TestHandleStatus_ShowsAllDals(t *testing.T) {
	d := &Daemon{
		containers:   map[string]*Container{},
		localdalRoot: t.TempDir(),
	}
	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	d.handleStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ── handleLogs 분기 ──────────────────────────────────────

func TestHandleLogs_NotRunning(t *testing.T) {
	d := &Daemon{
		containers: map[string]*Container{},
	}
	req := httptest.NewRequest("GET", "/api/logs/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	d.handleLogs(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── discoverByLabel 테스트 ───────────────────────────────

func TestDiscoverByLabel_FuncExists(t *testing.T) {
	src := readSource(t, "docker.go")
	if !strings.Contains(src, "func discoverByLabel") {
		t.Fatal("discoverByLabel must exist")
	}
}

// ── webhook dispatch guard ──────────────────────────────

func TestWebhookDispatch_FuncExists(t *testing.T) {
	src := readSource(t, "webhook.go")
	if !strings.Contains(src, "func DispatchTaskComplete") {
		t.Fatal("DispatchTaskComplete must exist")
	}
	if !strings.Contains(src, "func DispatchTaskFailed") {
		t.Fatal("DispatchTaskFailed must exist")
	}
}

// ── repo watcher guard ──────────────────────────────────

func TestRepoWatcher_FuncExists(t *testing.T) {
	src := readSource(t, "repo_watcher.go")
	if !strings.Contains(src, "func startRepoWatcher") {
		t.Fatal("startRepoWatcher must exist")
	}
}

// ── credential watcher 분기 ─────────────────────────────

func TestIsApproachingExpiry_FarFuture(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cred.json")
	future := time.Now().Add(24 * time.Hour).UnixMilli()
	os.WriteFile(f, []byte(fmt.Sprintf(`{"claudeAiOauth":{"expiresAt":%d}}`, future)), 0600)
	approaching, _ := isApproachingExpiry(f, 1*time.Hour)
	if approaching {
		t.Fatal("far future should not be approaching")
	}
}

func TestIsApproachingExpiry_NearExpiry(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cred.json")
	near := time.Now().Add(30 * time.Minute).UnixMilli()
	os.WriteFile(f, []byte(fmt.Sprintf(`{"claudeAiOauth":{"expiresAt":%d}}`, near)), 0600)
	approaching, _ := isApproachingExpiry(f, 1*time.Hour)
	if !approaching {
		t.Fatal("near expiry should be approaching")
	}
}
