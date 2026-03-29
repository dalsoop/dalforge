package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const credentialSyncCooldown = 10 * time.Minute

var lxcVMIDPattern = regexp.MustCompile(`/lxc/([0-9]+)(?:/|$)`)

var runCredentialOpCommand = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%w: %s", err, text)
		}
		return text, err
	}
	return text, nil
}

var readCredentialOpsFile = os.ReadFile

type credentialSyncRequest struct {
	Player    string
	VMID      string
	Source    string
	SourceDal string
	Repo      string
	ClaimID   string
}

type credentialSyncSSHConfig struct {
	Host    string
	User    string
	KeyPath string
}

type credentialSyncHTTPConfig struct {
	URL   string
	Token string
}

func credentialOpsEnabled() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("DALCENTER_CRED_OPS_ENABLED")))
	return raw == "" || (raw != "0" && raw != "false" && raw != "off")
}

func credentialOpsChannelName() string {
	if ch := strings.TrimSpace(os.Getenv("DALCENTER_CRED_OPS_CHANNEL")); ch != "" {
		return ch
	}
	return "dal-ops"
}

func credentialSyncSSHBridge() (credentialSyncSSHConfig, bool) {
	host := strings.TrimSpace(os.Getenv("DALCENTER_CRED_OPS_SSH_HOST"))
	user := strings.TrimSpace(os.Getenv("DALCENTER_CRED_OPS_SSH_USER"))
	if host == "" || user == "" {
		return credentialSyncSSHConfig{}, false
	}
	return credentialSyncSSHConfig{
		Host:    host,
		User:    user,
		KeyPath: strings.TrimSpace(os.Getenv("DALCENTER_CRED_OPS_SSH_KEY")),
	}, true
}

func credentialSyncHTTPBridge() (credentialSyncHTTPConfig, bool) {
	url := strings.TrimSpace(os.Getenv("DALCENTER_CRED_OPS_HTTP_URL"))
	token := strings.TrimSpace(os.Getenv("DALCENTER_CRED_OPS_HTTP_TOKEN"))
	if url == "" || token == "" {
		return credentialSyncHTTPConfig{}, false
	}
	return credentialSyncHTTPConfig{URL: strings.TrimRight(url, "/"), Token: token}, true
}

func parseCredentialSyncContext(raw string) (credentialSyncRequest, bool) {
	vals, err := url.ParseQuery(raw)
	if err != nil || vals.Get("kind") != "credential_sync" {
		return credentialSyncRequest{}, false
	}
	return credentialSyncRequest{
		Player:    strings.TrimSpace(vals.Get("player")),
		VMID:      strings.TrimSpace(vals.Get("vmid")),
		Source:    strings.TrimSpace(vals.Get("source")),
		SourceDal: strings.TrimSpace(vals.Get("source_dal")),
		Repo:      strings.TrimSpace(vals.Get("repo")),
	}, true
}

func detectCredentialSyncVMID() string {
	if vmid := strings.TrimSpace(os.Getenv("DALCENTER_VMID")); vmid != "" {
		return vmid
	}
	for _, path := range []string{"/proc/1/cgroup", "/proc/1/cpuset"} {
		data, err := readCredentialOpsFile(path)
		if err != nil {
			continue
		}
		if vmid := extractLXCVMID(string(data)); vmid != "" {
			return vmid
		}
	}
	return ""
}

func extractLXCVMID(content string) string {
	m := lxcVMIDPattern.FindStringSubmatch(content)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func credentialFileForPlayer(player string) string {
	home, _ := os.UserHomeDir()
	switch player {
	case "claude":
		return filepath.Join(home, ".claude", ".credentials.json")
	case "codex":
		return filepath.Join(home, ".codex", "auth.json")
	case "gemini":
		return filepath.Join(home, ".gemini", "credentials.json")
	default:
		return ""
	}
}

func (d *Daemon) reserveCredentialSync(req credentialSyncRequest) bool {
	if !credentialOpsEnabled() {
		return false
	}
	if req.Player == "" {
		req.Player = "all"
	}
	if req.VMID == "" {
		req.VMID = detectCredentialSyncVMID()
	}
	if req.VMID == "" {
		log.Printf("[credential-ops] skipped: vmid unavailable for player=%s", req.Player)
		return false
	}

	key := req.Player + "@" + req.VMID
	now := time.Now()

	d.credSyncMu.Lock()
	defer d.credSyncMu.Unlock()
	if last, ok := d.credSyncLast[key]; ok && now.Sub(last) < credentialSyncCooldown {
		log.Printf("[credential-ops] deduped player=%s vmid=%s source=%s", req.Player, req.VMID, req.Source)
		return false
	}
	d.credSyncLast[key] = now
	return true
}

func (d *Daemon) requestCredentialSync(req credentialSyncRequest) bool {
	if !d.reserveCredentialSync(req) {
		return false
	}
	if req.Player == "" {
		req.Player = "all"
	}
	if req.VMID == "" {
		req.VMID = detectCredentialSyncVMID()
	}
	go d.runCredentialSync(req)
	return true
}

func (d *Daemon) runCredentialSync(req credentialSyncRequest) {
	player := req.Player
	if player == "" {
		player = "all"
	}
	vmid := req.VMID
	if vmid == "" {
		vmid = detectCredentialSyncVMID()
	}
	if vmid == "" {
		msg := fmt.Sprintf("[credential-ops] 실패 player=%s: vmid를 찾지 못했습니다", player)
		log.Printf("%s", msg)
		d.postOpsMessage(msg)
		d.postProjectMessage(msg)
		if req.ClaimID != "" {
			d.claims.Respond(req.ClaimID, "rejected", msg)
		}
		return
	}

	startMsg := fmt.Sprintf("[credential-ops] 시작 player=%s vmid=%s source=%s dal=%s", player, vmid, req.Source, req.SourceDal)
	log.Printf("%s", startMsg)
	d.postOpsMessage(startMsg)
	if req.ClaimID != "" {
		d.claims.Respond(req.ClaimID, "acknowledged", fmt.Sprintf("credential sync started automatically for %s vmid=%s", player, vmid))
	}

	if hasCredentialOpCommand("proxmox-host-setup") {
		if output, err := runCredentialOpCommand("proxmox-host-setup", "ai", "sync", "--agent", player); err != nil {
			d.finishCredentialSyncFailure(req, player, vmid, "proxmox-host-setup ai sync", err, output)
			return
		}
	} else {
		log.Printf("[credential-ops] host sync command unavailable in this runtime, skipping proxmox-host-setup ai sync")
	}
	if !hasCredentialOpCommand("pve-sync-creds") {
		if output, err := runCredentialSyncViaHTTP(player, vmid); err == nil {
			log.Printf("[credential-ops] remote HTTP bridge sync ok: %s", truncateCredentialOps(output, 200))
		} else if output, err := runCredentialSyncViaSSH(player, vmid); err == nil {
			log.Printf("[credential-ops] remote SSH bridge sync ok: %s", truncateCredentialOps(output, 200))
		} else {
			d.finishCredentialSyncFailure(req, player, vmid, "command lookup", err, output)
			return
		}
	} else {
		if output, err := runCredentialOpCommand("pve-sync-creds", vmid); err != nil {
			d.finishCredentialSyncFailure(req, player, vmid, "pve-sync-creds", err, output)
			return
		}
	}

	verify := credentialFileForPlayer(player)
	mtime := ""
	if verify != "" {
		info, err := os.Stat(verify)
		if err != nil {
			d.finishCredentialSyncFailure(req, player, vmid, "verify", err, verify)
			return
		}
		mtime = info.ModTime().UTC().Format(time.RFC3339)
	}

	successMsg := fmt.Sprintf("[credential-ops] 완료 player=%s vmid=%s", player, vmid)
	if mtime != "" {
		successMsg = fmt.Sprintf("%s mtime=%s", successMsg, mtime)
	}
	log.Printf("%s", successMsg)
	d.postOpsMessage(successMsg)
	d.postProjectMessage(successMsg)
	if req.ClaimID != "" {
		d.claims.Respond(req.ClaimID, "resolved", successMsg)
	}
}

func (d *Daemon) finishCredentialSyncFailure(req credentialSyncRequest, player, vmid, step string, err error, output string) {
	msg := fmt.Sprintf("[credential-ops] 실패 player=%s vmid=%s step=%s err=%v", player, vmid, step, err)
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		msg = fmt.Sprintf("%s output=%s", msg, truncateCredentialOps(trimmed, 240))
	}
	log.Printf("%s", msg)
	d.postOpsMessage(msg)
	d.postProjectMessage(msg)
	if req.ClaimID != "" {
		d.claims.Respond(req.ClaimID, "rejected", msg)
	}
}

func (d *Daemon) postOpsMessage(message string) {
	if d.mm == nil || d.opsChannelID == "" {
		return
	}
	body := fmt.Sprintf(`{"channel_id":%q,"message":%q}`, d.opsChannelID, message)
	if _, err := mmPost(d.mm.URL, d.mm.AdminToken, "/api/v4/posts", body); err != nil {
		log.Printf("[credential-ops] ops post failed: %v", err)
	}
}

func (d *Daemon) postProjectMessage(message string) {
	if d.mm == nil || d.channelID == "" {
		return
	}
	body := fmt.Sprintf(`{"channel_id":%q,"message":%q}`, d.channelID, message)
	if _, err := mmPost(d.mm.URL, d.mm.AdminToken, "/api/v4/posts", body); err != nil {
		log.Printf("[credential-ops] project post failed: %v", err)
	}
}

func truncateCredentialOps(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func hasCredentialOpCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func runCredentialSyncViaSSH(player, vmid string) (string, error) {
	cfg, ok := credentialSyncSSHBridge()
	if !ok {
		return "", fmt.Errorf("credential ops bridge not configured: pve-sync-creds unavailable")
	}
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=5",
	}
	if cfg.KeyPath != "" {
		args = append(args, "-i", cfg.KeyPath)
	}
	args = append(args, fmt.Sprintf("%s@%s", cfg.User, cfg.Host), "credential-sync", player, vmid)
	return runCredentialOpCommand("ssh", args...)
}

func runCredentialSyncViaHTTP(player, vmid string) (string, error) {
	cfg, ok := credentialSyncHTTPBridge()
	if !ok {
		return "", fmt.Errorf("credential ops HTTP bridge not configured")
	}
	body, _ := json.Marshal(map[string]string{
		"player": player,
		"vmid":   vmid,
	})
	req, err := http.NewRequest(http.MethodPost, cfg.URL+"/sync", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return string(respBody), fmt.Errorf("http %d", resp.StatusCode)
	}
	return string(respBody), nil
}

func (d *Daemon) handleCredentialSyncClaim(claim Claim) {
	req, ok := parseCredentialSyncContext(claim.Context)
	if !ok {
		return
	}
	req.ClaimID = claim.ID
	if req.SourceDal == "" {
		req.SourceDal = claim.Dal
	}
	if req.Source == "" {
		req.Source = "claim"
	}
	if !d.requestCredentialSync(req) {
		d.claims.Respond(claim.ID, "acknowledged", "credential sync already queued recently")
	}
}

func newCredentialSyncMap() map[string]time.Time { return make(map[string]time.Time) }
