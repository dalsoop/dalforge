package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const hostopsTimeout = 30 * time.Second

// HostOpsRequest is a skill-based request sent to the LXC 101 gateway.
type HostOpsRequest struct {
	Skill  string            `json:"skill"`
	Params map[string]string `json:"params"`
}

// HostOpsResponse is the result returned from the LXC 101 gateway.
type HostOpsResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HostOpsClient calls the LXC 101 host-ops API gateway.
type HostOpsClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// Supported ops skill names.
const (
	SkillCFPagesDeploy  = "cf-pages-deploy"
	SkillDNSManage      = "dns-manage"
	SkillGitPush        = "git-push"
	SkillCertManage     = "cert-manage"
	SkillServiceRestart = "service-restart"
)

// skillEndpoints maps skill names to their API paths on the gateway.
var skillEndpoints = map[string]string{
	SkillCFPagesDeploy:  "/api/cf-pages/deploy",
	SkillDNSManage:      "/api/dns/manage",
	SkillGitPush:        "/api/git/push",
	SkillCertManage:     "/api/cert/manage",
	SkillServiceRestart: "/api/service/restart",
}

// newHostOpsClient creates a client from environment variables.
// Returns nil if DALCENTER_HOSTOPS_URL is not configured.
func newHostOpsClient() *HostOpsClient {
	url := strings.TrimSpace(os.Getenv("DALCENTER_HOSTOPS_URL"))
	if url == "" {
		return nil
	}
	token := strings.TrimSpace(os.Getenv("DALCENTER_HOSTOPS_TOKEN"))
	url = strings.TrimRight(url, "/")
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	log.Printf("[hostops] client configured: %s", url)
	return &HostOpsClient{
		baseURL: url,
		token:   token,
		http:    &http.Client{Timeout: hostopsTimeout},
	}
}

// Execute sends an ops skill request to the gateway.
func (c *HostOpsClient) Execute(req HostOpsRequest) (*HostOpsResponse, error) {
	endpoint, ok := skillEndpoints[req.Skill]
	if !ok {
		return nil, fmt.Errorf("unknown ops skill: %s", req.Skill)
	}

	body, err := json.Marshal(req.Params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to gateway: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("gateway auth failed (401): check DALCENTER_HOSTOPS_TOKEN")
	}

	var result HostOpsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Non-JSON response — wrap raw body as error
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("gateway error %d: %s", resp.StatusCode, truncateHostOps(string(respBody), 300))
		}
		return &HostOpsResponse{OK: true, Output: string(respBody)}, nil
	}

	if resp.StatusCode >= 400 && !result.OK {
		return &result, fmt.Errorf("gateway error %d: %s", resp.StatusCode, result.Error)
	}

	return &result, nil
}

// CFPagesDeploy deploys a project to Cloudflare Pages.
func (c *HostOpsClient) CFPagesDeploy(project, directory string) (*HostOpsResponse, error) {
	return c.Execute(HostOpsRequest{
		Skill: SkillCFPagesDeploy,
		Params: map[string]string{
			"project":   project,
			"directory": directory,
		},
	})
}

// DNSManage creates or deletes a DNS record.
func (c *HostOpsClient) DNSManage(action, recordType, name, content string) (*HostOpsResponse, error) {
	return c.Execute(HostOpsRequest{
		Skill: SkillDNSManage,
		Params: map[string]string{
			"action":  action,
			"type":    recordType,
			"name":    name,
			"content": content,
		},
	})
}

// GitPush pushes a branch to a remote repository.
func (c *HostOpsClient) GitPush(repo, branch, remote string) (*HostOpsResponse, error) {
	return c.Execute(HostOpsRequest{
		Skill: SkillGitPush,
		Params: map[string]string{
			"repo":   repo,
			"branch": branch,
			"remote": remote,
		},
	})
}

// CertManage issues or renews a TLS certificate.
func (c *HostOpsClient) CertManage(action, domain string) (*HostOpsResponse, error) {
	return c.Execute(HostOpsRequest{
		Skill: SkillCertManage,
		Params: map[string]string{
			"action": action,
			"domain": domain,
		},
	})
}

// ServiceRestart restarts a systemd service.
func (c *HostOpsClient) ServiceRestart(service string) (*HostOpsResponse, error) {
	return c.Execute(HostOpsRequest{
		Skill: SkillServiceRestart,
		Params: map[string]string{
			"service": service,
		},
	})
}

// handleHostOps proxies an ops skill request to the LXC 101 gateway.
func (d *Daemon) handleHostOps(w http.ResponseWriter, r *http.Request) {
	if d.hostops == nil {
		http.Error(w, `{"ok":false,"error":"hostops not configured (set DALCENTER_HOSTOPS_URL)"}`, http.StatusServiceUnavailable)
		return
	}

	var req HostOpsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"ok":false,"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Skill == "" {
		http.Error(w, `{"ok":false,"error":"skill is required"}`, http.StatusBadRequest)
		return
	}

	log.Printf("[hostops] executing skill=%s params=%v", req.Skill, req.Params)

	resp, err := d.hostops.Execute(req)
	if err != nil {
		log.Printf("[hostops] skill=%s failed: %v", req.Skill, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(HostOpsResponse{
			OK:    false,
			Error: err.Error(),
		})
		return
	}

	log.Printf("[hostops] skill=%s ok=%v", req.Skill, resp.OK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func truncateHostOps(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
