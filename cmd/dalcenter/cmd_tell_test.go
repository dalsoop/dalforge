package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSendViaDalcenter(t *testing.T) {
	var received struct {
		From    string `json:"from"`
		Message string `json:"message"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/message" {
			t.Errorf("path = %s, want /api/message", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		json.NewDecoder(r.Body).Decode(&received)
		json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "myteam="+srv.URL)

	err := sendViaDalcenter("myteam", "hello leader")
	if err != nil {
		t.Fatalf("sendViaDalcenter: %v", err)
	}
	if received.Message != "hello leader" {
		t.Errorf("message = %q, want %q", received.Message, "hello leader")
	}
}

func TestSendViaDalcenterWithAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "team1="+srv.URL)
	t.Setenv("DALCENTER_TOKEN", "secret123")

	err := sendViaDalcenter("team1", "msg")
	if err != nil {
		t.Fatalf("sendViaDalcenter: %v", err)
	}
	if gotAuth != "Bearer secret123" {
		t.Errorf("auth = %q, want %q", gotAuth, "Bearer secret123")
	}
}

func TestSendViaBridge(t *testing.T) {
	var received struct {
		Text     string `json:"text"`
		Username string `json:"username"`
		Gateway  string `json:"gateway"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/message" {
			t.Errorf("path = %s, want /api/message", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_BRIDGE_URLS", "teamA="+srv.URL)

	err := sendViaBridge("teamA", "direct message")
	if err != nil {
		t.Fatalf("sendViaBridge: %v", err)
	}
	if received.Text != "direct message" {
		t.Errorf("text = %q, want %q", received.Text, "direct message")
	}
	if received.Gateway != "dal-team" {
		t.Errorf("gateway = %q, want %q", received.Gateway, "dal-team")
	}
}

func TestSendViaBridgeWithToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_BRIDGE_URLS", "teamB="+srv.URL)
	t.Setenv("MATTERBRIDGE_TOKEN", "bridge-secret")

	err := sendViaBridge("teamB", "msg")
	if err != nil {
		t.Fatalf("sendViaBridge: %v", err)
	}
	if gotAuth != "Bearer bridge-secret" {
		t.Errorf("auth = %q, want %q", gotAuth, "Bearer bridge-secret")
	}
}

func TestResolveBridgeURL_FromEnvVar(t *testing.T) {
	t.Setenv("DALCENTER_BRIDGE_URLS", "team1=http://10.0.0.1:4242,team2=http://10.0.0.1:4243")

	url, err := resolveBridgeURL("team2")
	if err != nil {
		t.Fatalf("resolveBridgeURL: %v", err)
	}
	if url != "http://10.0.0.1:4243" {
		t.Errorf("url = %q, want %q", url, "http://10.0.0.1:4243")
	}
}

func TestResolveBridgeURL_FromEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", tmpDir)
	t.Setenv("DALCENTER_BRIDGE_URLS", "") // ensure env var is empty

	envContent := "DALCENTER_BRIDGE_URL=http://192.168.1.10:4243\n"
	os.WriteFile(filepath.Join(tmpDir, "teamX.env"), []byte(envContent), 0644)

	url, err := resolveBridgeURL("teamX")
	if err != nil {
		t.Fatalf("resolveBridgeURL: %v", err)
	}
	if url != "http://192.168.1.10:4243" {
		t.Errorf("url = %q, want %q", url, "http://192.168.1.10:4243")
	}
}

func TestResolveBridgeURL_DefaultFallback(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", tmpDir)
	t.Setenv("DALCENTER_BRIDGE_URLS", "")

	// Write common.env with host IP
	os.WriteFile(filepath.Join(tmpDir, "common.env"), []byte("DALCENTER_HOST_IP=10.0.0.5\n"), 0644)

	url, err := resolveBridgeURL("unknown-team")
	if err != nil {
		t.Fatalf("resolveBridgeURL: %v", err)
	}
	if url != "http://10.0.0.5:4242" {
		t.Errorf("url = %q, want %q", url, "http://10.0.0.5:4242")
	}
}

func TestResolveBridgeGateway_Default(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", tmpDir)

	gw := resolveBridgeGateway("anyteam")
	if gw != "dal-team" {
		t.Errorf("gateway = %q, want %q", gw, "dal-team")
	}
}

func TestResolveBridgeGateway_FromEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", tmpDir)

	envContent := "DALCENTER_BRIDGE_GATEWAY=custom-gateway\n"
	os.WriteFile(filepath.Join(tmpDir, "team1.env"), []byte(envContent), 0644)

	gw := resolveBridgeGateway("team1")
	if gw != "custom-gateway" {
		t.Errorf("gateway = %q, want %q", gw, "custom-gateway")
	}
}

func TestSendViaDalcenter_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", 500)
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "errteam="+srv.URL)

	err := sendViaDalcenter("errteam", "msg")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestSendViaBridge_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", 401)
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_BRIDGE_URLS", "errteam="+srv.URL)

	err := sendViaBridge("errteam", "msg")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestNewTellCmd_IssueFlag(t *testing.T) {
	cmd := newTellCmd()

	// Verify flags exist
	f := cmd.Flags().Lookup("issue")
	if f == nil {
		t.Fatal("--issue flag not found")
	}
	f = cmd.Flags().Lookup("direct")
	if f == nil {
		t.Fatal("--direct flag not found")
	}
}
