package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dalforge-hub/dalcenter/internal/export"

	"dalforge-hub/dalcenter/internal/state"
)

func TestBuildCommandDefaults(t *testing.T) {
	args := BuildCommand(Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "dalcli-agent-coach",
	})

	cmd := strings.Join(args, " ")
	if !strings.Contains(cmd, "create 0") {
		t.Fatalf("expected VMID 0 (auto), got: %s", cmd)
	}
	if !strings.Contains(cmd, "local:vztmpl/ubuntu-24.04") {
		t.Fatalf("expected ubuntu template, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--hostname dalcli-agent-coach") {
		t.Fatalf("expected hostname, got: %s", cmd)
	}
}

func TestBuildCommandCustomOptions(t *testing.T) {
	args := BuildCommand(Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
		VMID:         "300",
		Storage:      "zfs-pool",
		Bridge:       "vmbr1",
		Memory:       "2048",
		Cores:        "4",
	})
	cmd := strings.Join(args, " ")
	if !strings.Contains(cmd, "--storage zfs-pool") {
		t.Fatalf("expected zfs-pool storage, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--memory 2048") {
		t.Fatalf("expected 2048 memory, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--cores 4") {
		t.Fatalf("expected 4 cores, got: %s", cmd)
	}
	if !strings.Contains(cmd, "bridge=vmbr1") {
		t.Fatalf("expected vmbr1 bridge, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--rootfs zfs-pool:4") {
		t.Fatalf("expected rootfs with zfs-pool, got: %s", cmd)
	}
}

func TestBuildCommandDefaultsNoBridge(t *testing.T) {
	args := BuildCommand(Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
	})
	cmd := strings.Join(args, " ")
	if strings.Contains(cmd, "--net0") {
		t.Fatalf("expected no --net0 without bridge, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--storage local-lvm") {
		t.Fatalf("expected default storage, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--cores 1") {
		t.Fatalf("expected default cores, got: %s", cmd)
	}
}

func TestBuildCommandWithVMID(t *testing.T) {
	args := BuildCommand(Spec{
		Base:         "debian:12",
		InstanceName: "test-instance",
		VMID:         "211500",
	})

	cmd := strings.Join(args, " ")
	if !strings.Contains(cmd, "create 211500") {
		t.Fatalf("expected VMID 211500, got: %s", cmd)
	}
	if !strings.Contains(cmd, "debian-12-standard") {
		t.Fatalf("expected debian template, got: %s", cmd)
	}
}

func TestBuildCommandCustomTemplate(t *testing.T) {
	args := BuildCommand(Spec{
		Base:         "local:vztmpl/custom.tar.gz",
		InstanceName: "custom",
	})

	cmd := strings.Join(args, " ")
	if !strings.Contains(cmd, "local:vztmpl/custom.tar.gz") {
		t.Fatalf("expected passthrough template, got: %s", cmd)
	}
}

func TestDryRunDoesNotExecute(t *testing.T) {
	result := Provision("/nonexistent", Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
	}, true)

	if result.Error != nil {
		t.Fatalf("dry-run should not error: %v", result.Error)
	}
	if len(result.Commands) == 0 || !strings.Contains(result.Commands[0], "pct create") {
		t.Fatalf("expected pct command, got: %v", result.Commands)
	}
}

func TestBuildAllCommandsWithPackages(t *testing.T) {
	cmds := BuildAllCommands(Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
		VMID:         "211500",
		Packages:     []string{"bash", "python3", "tmux"},
	})

	if len(cmds) != 4 {
		t.Fatalf("expected 4 commands (create+start+update+install), got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "pct create 211500") {
		t.Fatalf("cmd[0]: %s", cmds[0])
	}
	if !strings.Contains(cmds[1], "pct start 211500") {
		t.Fatalf("cmd[1]: %s", cmds[1])
	}
	if !strings.Contains(cmds[2], "apt-get update") {
		t.Fatalf("cmd[2]: %s", cmds[2])
	}
	if !strings.Contains(cmds[3], "apt-get install") || !strings.Contains(cmds[3], "bash python3 tmux") {
		t.Fatalf("cmd[3]: %s", cmds[3])
	}
}

func TestBuildAllCommandsNoPackages(t *testing.T) {
	cmds := BuildAllCommands(Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
		VMID:         "211500",
	})
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command (create only), got %d", len(cmds))
	}
}

func TestProvisionWithoutPctRecordsError(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0755)

	// Ensure pct is NOT in a restricted PATH
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "/nonexistent")
	defer os.Setenv("PATH", origPath)

	result := Provision(dir, Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
	}, false)

	if result.Error == nil {
		t.Fatal("expected error when pct not found")
	}
	if !strings.Contains(result.Error.Error(), "pct not found") {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// State should record error
	hs, err := state.Read(dir)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if hs.ProvisionStatus != "error" {
		t.Fatalf("expected error status, got %q", hs.ProvisionStatus)
	}
}

func TestSanitizeHostname(t *testing.T) {
	cases := []struct{ in, want string }{
		{"dalcli-agent-coach", "dalcli-agent-coach"},
		{"my_tool.v2", "my-tool-v2"},
		{"a/b/c", "a-b-c"},
	}
	for _, tc := range cases {
		got := sanitizeHostname(tc.in)
		if got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildDestroyCommands(t *testing.T) {
	cmds := BuildDestroyCommands("211500")
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}
	stop := strings.Join(cmds[0], " ")
	if !strings.Contains(stop, "stop 211500 --skiplock") {
		t.Fatalf("unexpected stop: %s", stop)
	}
	destroy := strings.Join(cmds[1], " ")
	if !strings.Contains(destroy, "destroy 211500 --purge") {
		t.Fatalf("unexpected destroy: %s", destroy)
	}
}

func TestDestroyDryRun(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "state"), 0755)
	// Write state with vmid
	state.Write(dir, state.HealthState{VMID: "211500", ProvisionStatus: "provisioned"})

	r := Destroy(dir, true)
	if r.Error != nil {
		t.Fatalf("dry-run error: %v", r.Error)
	}
	if len(r.Commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(r.Commands))
	}
	if !strings.Contains(r.Commands[0], "pct stop 211500") {
		t.Fatalf("unexpected cmd: %s", r.Commands[0])
	}
}

func TestDestroyNoVMIDIsNoop(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "state"), 0755)
	state.Write(dir, state.HealthState{})

	r := Destroy(dir, false)
	if r.Error != nil {
		t.Fatalf("expected no-op (nil error), got: %v", r.Error)
	}
	if len(r.Commands) != 0 {
		t.Fatalf("expected no commands for no-op, got %d", len(r.Commands))
	}
}

func TestDestroyWithoutPctRecordsError(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "state"), 0755)
	state.Write(dir, state.HealthState{VMID: "211500", ProvisionStatus: "provisioned"})

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "/nonexistent")
	defer os.Setenv("PATH", origPath)

	r := Destroy(dir, false)
	if r.Error == nil || !strings.Contains(r.Error.Error(), "pct not found") {
		t.Fatalf("expected pct not found, got: %v", r.Error)
	}

	hs, _ := state.Read(dir)
	if hs.ProvisionStatus != "error" {
		t.Fatalf("expected error status, got %q", hs.ProvisionStatus)
	}
	if hs.VMID != "211500" {
		t.Fatalf("vmid should be preserved on error, got %q", hs.VMID)
	}
}

func TestDryRunWithPackagesShowsRollback(t *testing.T) {
	r := Provision("/nonexistent", Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
		VMID:         "211500",
		Packages:     []string{"bash", "tmux"},
	}, true)
	if r.Error != nil {
		t.Fatalf("dry-run error: %v", r.Error)
	}
	// Should have: create, start, update, install, "# on failure:", stop, destroy = 7
	found := false
	for _, c := range r.Commands {
		if c == "# on failure:" {
			found = true
		}
		if found && strings.Contains(c, "pct destroy 211500 --purge") {
			break
		}
	}
	if !found {
		t.Fatalf("expected rollback commands in dry-run, got: %v", r.Commands)
	}
}

func TestBuildRollbackCommands(t *testing.T) {
	cmds := BuildRollbackCommands("999")
	if len(cmds) != 2 {
		t.Fatalf("expected 2, got %d", len(cmds))
	}
	if !strings.Contains(cmds[0], "stop 999") {
		t.Fatalf("cmd[0]: %s", cmds[0])
	}
	if !strings.Contains(cmds[1], "destroy 999 --purge") {
		t.Fatalf("cmd[1]: %s", cmds[1])
	}
}

func TestProvisionWithPctAbsentRecordsNoRollback(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "state"), 0755)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "/nonexistent")
	defer os.Setenv("PATH", origPath)

	r := Provision(dir, Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
		Packages:     []string{"bash"},
	}, false)

	if r.Error == nil {
		t.Fatal("expected error")
	}
	hs, _ := state.Read(dir)
	if hs.RollbackStatus != "" {
		t.Fatalf("expected empty rollback (pct absent, no create happened), got %q", hs.RollbackStatus)
	}
}

func TestBuildAgentInstallCommands(t *testing.T) {
	cases := []struct {
		agent  export.AgentSpec
		expect string
	}{
		{export.AgentSpec{Name: "claude", Type: "claude_sdk", Package: "@anthropic-ai/claude-code"}, "npm install -g @anthropic-ai/claude-code"},
		{export.AgentSpec{Name: "codex", Type: "codex_appserver", Package: "@openai/codex"}, "npm install -g @openai/codex"},
		{export.AgentSpec{Name: "gemini", Type: "gemini_cli", Package: "gemini-cli"}, "pip3 install gemini-cli"},
		{export.AgentSpec{Name: "gpt4", Type: "openai_compatible", Package: "openai"}, "# gpt4: openai_compatible"},
		{export.AgentSpec{Name: "x", Type: "unknown_type", Package: "x"}, "# x: unknown type"},
	}
	for _, tc := range cases {
		cmd := BuildAgentInstallCommand("211500", tc.agent)
		if !strings.Contains(cmd, tc.expect) {
			t.Errorf("agent %s: expected %q in %q", tc.agent.Name, tc.expect, cmd)
		}
	}
}

func TestBuildAllCommandsWithAgents(t *testing.T) {
	cmds := BuildAllCommands(Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
		VMID:         "211500",
		Packages:     []string{"bash"},
		Agents: []export.AgentSpec{
			{Name: "claude", Type: "claude_sdk", Package: "@anthropic-ai/claude-code"},
			{Name: "gpt4", Type: "openai_compatible", Package: "openai"},
		},
	})

	// create + start + update + install + claude npm + gpt4 comment = 6
	if len(cmds) != 6 {
		t.Fatalf("expected 6 commands, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[4], "npm install -g @anthropic-ai/claude-code") {
		t.Fatalf("cmd[4]: %s", cmds[4])
	}
	if !strings.Contains(cmds[5], "# gpt4: openai_compatible") {
		t.Fatalf("cmd[5]: %s", cmds[5])
	}
}

func TestBuildAllCommandsAgentsOnlyNoPackages(t *testing.T) {
	cmds := BuildAllCommands(Spec{
		Base:         "ubuntu:24.04",
		InstanceName: "test",
		VMID:         "211500",
		Agents: []export.AgentSpec{
			{Name: "claude", Type: "claude_sdk", Package: "@anthropic-ai/claude-code"},
		},
	})
	// create + start + agent install = 3
	if len(cmds) != 3 {
		t.Fatalf("expected 3 commands, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[1], "pct start") {
		t.Fatalf("expected start, got: %s", cmds[1])
	}
}
