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

	msgID, err := sendViaDalcenter("myteam", "hello leader", "")
	if err != nil {
		t.Fatalf("sendViaDalcenter: %v", err)
	}
	if received.Message != "@dal-leader hello leader" {
		t.Errorf("message = %q, want %q", received.Message, "@dal-leader hello leader")
	}
	// Server returned no message_id in this test
	if msgID != "" {
		t.Errorf("message_id = %q, want empty", msgID)
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

	_, err := sendViaDalcenter("team1", "msg", "")
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

	err := sendViaBridge("teamA", "direct message", "")
	if err != nil {
		t.Fatalf("sendViaBridge: %v", err)
	}
	if received.Text != "@dal-leader direct message" {
		t.Errorf("text = %q, want %q", received.Text, "@dal-leader direct message")
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

	err := sendViaBridge("teamB", "msg", "")
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

	_, err := sendViaDalcenter("errteam", "msg", "")
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

	err := sendViaBridge("errteam", "msg", "")
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
	f = cmd.Flags().Lookup("member")
	if f == nil {
		t.Fatal("--member flag not found")
	}
}

func TestTriggerIssueWorkflow(t *testing.T) {
	var received struct {
		IssueID string `json:"issue_id"`
		Member  string `json:"member"`
		Task    string `json:"task"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/issue-workflow" {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"workflow_id": "iwf-test-123",
				"status":      "pending",
			})
			return
		}
		// /api/message handler for sendViaDalcenter
		json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "myteam="+srv.URL)

	err := triggerIssueWorkflow("myteam", 608, "dev", "[issue #608] implement feature")
	if err != nil {
		t.Fatalf("triggerIssueWorkflow: %v", err)
	}
	if received.IssueID != "608" {
		t.Errorf("issue_id = %q, want %q", received.IssueID, "608")
	}
	if received.Member != "dev" {
		t.Errorf("member = %q, want %q", received.Member, "dev")
	}
	if received.Task != "[issue #608] implement feature" {
		t.Errorf("task = %q, want %q", received.Task, "[issue #608] implement feature")
	}
}

func TestTriggerIssueWorkflow_WithAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"workflow_id": "iwf-test",
			"status":      "pending",
		})
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "team1="+srv.URL)
	t.Setenv("DALCENTER_TOKEN", "secret123")

	err := triggerIssueWorkflow("team1", 100, "dev", "task")
	if err != nil {
		t.Fatalf("triggerIssueWorkflow: %v", err)
	}
	if gotAuth != "Bearer secret123" {
		t.Errorf("auth = %q, want %q", gotAuth, "Bearer secret123")
	}
}

func TestTriggerIssueWorkflow_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "conflict", http.StatusConflict)
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "errteam="+srv.URL)

	err := triggerIssueWorkflow("errteam", 100, "dev", "task")
	if err == nil {
		t.Fatal("expected error for 409 response")
	}
}

func TestTellCmd_IssueTriggersWorkflow(t *testing.T) {
	var messageReceived, workflowReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/message":
			messageReceived = true
			json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
		case "/api/issue-workflow":
			workflowReceived = true
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"workflow_id": "iwf-test",
				"status":      "pending",
			})
		case "/api/ps":
			json.NewEncoder(w).Encode([]struct{}{})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "target="+srv.URL)

	cmd := newTellCmd()
	cmd.SetArgs([]string{"target", "do the thing", "--issue", "608", "--member", "dev"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cmd.Execute: %v", err)
	}

	if !messageReceived {
		t.Error("expected /api/message to be called")
	}
	if !workflowReceived {
		t.Error("expected /api/issue-workflow to be called")
	}
}

func TestSendViaDalcenter_ReturnsMessageID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"status":     "sent",
			"message_id": "msg-0001",
		})
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "myteam="+srv.URL)

	msgID, err := sendViaDalcenter("myteam", "hello", "")
	if err != nil {
		t.Fatalf("sendViaDalcenter: %v", err)
	}
	if msgID != "msg-0001" {
		t.Errorf("message_id = %q, want %q", msgID, "msg-0001")
	}
}

func TestWaitForACK_Immediate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/messages/msg-0001" {
			json.NewEncoder(w).Encode(map[string]string{"status": "acked"})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "target="+srv.URL)

	err := waitForACK("target", "msg-0001")
	if err != nil {
		t.Fatalf("waitForACK: %v", err)
	}
}

func TestWaitForACK_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/messages/msg-fail" {
			json.NewEncoder(w).Encode(map[string]string{"status": "failed"})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "target="+srv.URL)

	err := waitForACK("target", "msg-fail")
	if err == nil {
		t.Fatal("expected error for failed message")
	}
}

func TestNewTellCmd_NoACKFlag(t *testing.T) {
	cmd := newTellCmd()
	f := cmd.Flags().Lookup("no-ack")
	if f == nil {
		t.Fatal("--no-ack flag not found")
	}
}

func TestTellCmd_NoIssue_NoWorkflow(t *testing.T) {
	var workflowCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/issue-workflow" {
			workflowCalled = true
		}
		if r.URL.Path == "/api/ps" {
			json.NewEncoder(w).Encode([]struct{}{})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "target="+srv.URL)

	cmd := newTellCmd()
	cmd.SetArgs([]string{"target", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cmd.Execute: %v", err)
	}

	if workflowCalled {
		t.Error("expected /api/issue-workflow NOT to be called without --issue")
	}
}

func TestSendViaDalcenter_LeaderMention(t *testing.T) {
	var received struct {
		From    string `json:"from"`
		Message string `json:"message"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_URLS", "team1="+srv.URL)

	msgs := []string{"test message", "another one", "issue #123 fix"}
	for _, msg := range msgs {
		_, err := sendViaDalcenter("team1", msg, "")
		if err != nil {
			t.Fatalf("sendViaDalcenter(%q): %v", msg, err)
		}
		want := "@dal-leader " + msg
		if received.Message != want {
			t.Errorf("sendViaDalcenter(%q): message = %q, want %q", msg, received.Message, want)
		}
	}
}

func TestSendViaBridge_LeaderMention(t *testing.T) {
	var received struct {
		Text     string `json:"text"`
		Username string `json:"username"`
		Gateway  string `json:"gateway"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("DALCENTER_BRIDGE_URLS", "team1="+srv.URL)

	msgs := []string{"bridge message", "hello world", "urgent task"}
	for _, msg := range msgs {
		err := sendViaBridge("team1", msg, "")
		if err != nil {
			t.Fatalf("sendViaBridge(%q): %v", msg, err)
		}
		want := "@dal-leader " + msg
		if received.Text != want {
			t.Errorf("sendViaBridge(%q): text = %q, want %q", msg, received.Text, want)
		}
	}
}
