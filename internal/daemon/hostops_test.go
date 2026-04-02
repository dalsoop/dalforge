package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostOpsClient_Execute_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/cf-pages/deploy" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("missing auth header")
		}

		var params map[string]string
		json.NewDecoder(r.Body).Decode(&params)
		if params["project"] != "my-site" {
			t.Fatalf("unexpected project: %s", params["project"])
		}

		json.NewEncoder(w).Encode(HostOpsResponse{
			OK:      true,
			Message: "deployed",
			Output:  "https://my-site.pages.dev",
		})
	}))
	defer srv.Close()

	c := &HostOpsClient{
		baseURL: srv.URL,
		token:   "test-token",
		http:    srv.Client(),
	}

	resp, err := c.CFPagesDeploy("my-site", "/dist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.OK || resp.Message != "deployed" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHostOpsClient_Execute_UnknownSkill(t *testing.T) {
	c := &HostOpsClient{baseURL: "http://localhost", http: http.DefaultClient}
	_, err := c.Execute(HostOpsRequest{Skill: "unknown-skill"})
	if err == nil || !strings.Contains(err.Error(), "unknown ops skill") {
		t.Fatalf("expected unknown skill error, got: %v", err)
	}
}

func TestHostOpsClient_Execute_GatewayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(HostOpsResponse{
			OK:    false,
			Error: "cf api timeout",
		})
	}))
	defer srv.Close()

	c := &HostOpsClient{baseURL: srv.URL, http: srv.Client()}
	resp, err := c.DNSManage("create", "CNAME", "app.example.com", "target.example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if resp == nil || resp.Error != "cf api timeout" {
		t.Fatalf("expected error in response: %+v", resp)
	}
}

func TestHostOpsClient_Execute_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	c := &HostOpsClient{baseURL: srv.URL, token: "bad", http: srv.Client()}
	_, err := c.ServiceRestart("nginx")
	if err == nil || !strings.Contains(err.Error(), "auth failed") {
		t.Fatalf("expected auth error, got: %v", err)
	}
}

func TestHandleHostOps_NotConfigured(t *testing.T) {
	d := &Daemon{}
	req := httptest.NewRequest(http.MethodPost, "/api/hostops", strings.NewReader(`{"skill":"dns-manage"}`))
	w := httptest.NewRecorder()
	d.handleHostOps(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleHostOps_InvalidBody(t *testing.T) {
	d := &Daemon{hostops: &HostOpsClient{baseURL: "http://localhost", http: http.DefaultClient}}
	req := httptest.NewRequest(http.MethodPost, "/api/hostops", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	d.handleHostOps(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleHostOps_MissingSkill(t *testing.T) {
	d := &Daemon{hostops: &HostOpsClient{baseURL: "http://localhost", http: http.DefaultClient}}
	req := httptest.NewRequest(http.MethodPost, "/api/hostops", strings.NewReader(`{"params":{"foo":"bar"}}`))
	w := httptest.NewRecorder()
	d.handleHostOps(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleHostOps_ProxiesRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(HostOpsResponse{OK: true, Message: "restarted"})
	}))
	defer srv.Close()

	d := &Daemon{
		hostops: &HostOpsClient{baseURL: srv.URL, http: srv.Client()},
	}

	body := `{"skill":"service-restart","params":{"service":"nginx"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/hostops", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleHostOps(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp HostOpsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.OK || resp.Message != "restarted" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestNewHostOpsClient_NoEnv(t *testing.T) {
	t.Setenv("DALCENTER_HOSTOPS_URL", "")
	c := newHostOpsClient()
	if c != nil {
		t.Fatal("expected nil client when URL not set")
	}
}

func TestNewHostOpsClient_WithEnv(t *testing.T) {
	t.Setenv("DALCENTER_HOSTOPS_URL", "10.0.0.101:9100")
	t.Setenv("DALCENTER_HOSTOPS_TOKEN", "secret")
	c := newHostOpsClient()
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.baseURL != "http://10.0.0.101:9100" {
		t.Fatalf("unexpected baseURL: %s", c.baseURL)
	}
	if c.token != "secret" {
		t.Fatalf("unexpected token: %s", c.token)
	}
}

func TestSkillEndpoints_AllSkillsHaveEndpoints(t *testing.T) {
	skills := []string{
		SkillCFPagesDeploy,
		SkillDNSManage,
		SkillGitPush,
		SkillCertManage,
		SkillServiceRestart,
	}
	for _, s := range skills {
		if _, ok := skillEndpoints[s]; !ok {
			t.Errorf("skill %q has no endpoint mapping", s)
		}
	}
}
