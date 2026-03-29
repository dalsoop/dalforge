package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseCredentialSyncContext(t *testing.T) {
	req, ok := parseCredentialSyncContext("kind=credential_sync&player=claude&source=dalcli&source_dal=verifier&vmid=105")
	if !ok {
		t.Fatal("expected credential sync context to parse")
	}
	if req.Player != "claude" || req.Source != "dalcli" || req.SourceDal != "verifier" || req.VMID != "105" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestExtractLXCVMID(t *testing.T) {
	vmid := extractLXCVMID("0::/lxc/105/ns/init.scope\n")
	if vmid != "105" {
		t.Fatalf("vmid = %q, want 105", vmid)
	}
}

func TestReserveCredentialSync_Dedupes(t *testing.T) {
	d := &Daemon{credSyncLast: newCredentialSyncMap()}
	req := credentialSyncRequest{Player: "claude", VMID: "105", Source: "test"}
	if !d.reserveCredentialSync(req) {
		t.Fatal("first reserve should succeed")
	}
	if d.reserveCredentialSync(req) {
		t.Fatal("second reserve should be deduped")
	}
}

func TestRunCredentialSync_RunsExpectedCommandsAndResolvesClaim(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	credDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(credDir, 0755); err != nil {
		t.Fatalf("mkdir cred dir: %v", err)
	}
	credFile := filepath.Join(credDir, ".credentials.json")
	if err := os.WriteFile(credFile, []byte(`{"claudeAiOauth":{"expiresAt":9999999999999}}`), 0600); err != nil {
		t.Fatalf("write cred file: %v", err)
	}

	var calls []string
	oldRun := runCredentialOpCommand
	runCredentialOpCommand = func(name string, args ...string) (string, error) {
		calls = append(calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		return "ok", nil
	}
	defer func() { runCredentialOpCommand = oldRun }()

	claims := newClaimStore()
	claim := claims.Add("verifier", ClaimBlocked, "credential 만료로 호스트 sync 필요", "test", "kind=credential_sync&player=claude&vmid=105")
	d := &Daemon{
		claims:       claims,
		credSyncLast: newCredentialSyncMap(),
	}

	d.runCredentialSync(credentialSyncRequest{
		Player:    "claude",
		VMID:      "105",
		Source:    "test",
		SourceDal: "verifier",
		ClaimID:   claim.ID,
	})

	want := []string{
		"proxmox-host-setup ai sync --agent claude",
		"pve-sync-creds 105",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}

	got := claims.Get(claim.ID)
	if got == nil {
		t.Fatal("claim missing")
	}
	if got.Status != "resolved" {
		t.Fatalf("claim status = %q, want resolved", got.Status)
	}
}

func TestHandleClaim_CredentialSyncTriggersOps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	credDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(credDir, 0755); err != nil {
		t.Fatalf("mkdir cred dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(credDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"expiresAt":9999999999999}}`), 0600); err != nil {
		t.Fatalf("write cred file: %v", err)
	}

	var calls []string
	oldRun := runCredentialOpCommand
	runCredentialOpCommand = func(name string, args ...string) (string, error) {
		calls = append(calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		return "ok", nil
	}
	defer func() { runCredentialOpCommand = oldRun }()

	d := &Daemon{
		claims:       newClaimStore(),
		credSyncLast: newCredentialSyncMap(),
	}

	body := `{"dal":"verifier","type":"blocked","title":"credential 만료로 호스트 sync 필요","detail":"claude auth failed","context":"kind=credential_sync&player=claude&source=dalcli&source_dal=verifier&vmid=105"}`
	req := httptest.NewRequest("POST", "/api/claim", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleClaim(w, req)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list := d.claims.List("")
		if len(list) == 1 && list[0].Status == "resolved" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(calls) != 2 {
		t.Fatalf("credential sync commands not executed, calls=%v", calls)
	}
	list := d.claims.List("")
	if len(list) != 1 || list[0].Status != "resolved" {
		t.Fatalf("claims = %#v, want single resolved claim", list)
	}
}

func TestRunCredentialSyncViaSSH(t *testing.T) {
	t.Setenv("DALCENTER_CRED_OPS_SSH_HOST", "10.50.0.1")
	t.Setenv("DALCENTER_CRED_OPS_SSH_USER", "root")
	t.Setenv("DALCENTER_CRED_OPS_SSH_KEY", "/root/.ssh/id_ed25519")

	var gotCmd string
	oldRun := runCredentialOpCommand
	runCredentialOpCommand = func(name string, args ...string) (string, error) {
		gotCmd = strings.TrimSpace(name + " " + strings.Join(args, " "))
		return "ok", nil
	}
	defer func() { runCredentialOpCommand = oldRun }()

	if _, err := runCredentialSyncViaSSH("claude", "105"); err != nil {
		t.Fatalf("runCredentialSyncViaSSH: %v", err)
	}
	if !strings.Contains(gotCmd, "ssh ") || !strings.Contains(gotCmd, "root@10.50.0.1") || !strings.Contains(gotCmd, "credential-sync claude 105") {
		t.Fatalf("unexpected ssh command: %s", gotCmd)
	}
}

func TestRunCredentialSyncViaHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["player"] != "claude" || req["vmid"] != "105" {
			t.Fatalf("unexpected request body: %#v", req)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_CRED_OPS_HTTP_URL", srv.URL)
	t.Setenv("DALCENTER_CRED_OPS_HTTP_TOKEN", "test-token")

	body, err := runCredentialSyncViaHTTP("claude", "105")
	if err != nil {
		t.Fatalf("runCredentialSyncViaHTTP: %v", err)
	}
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}
