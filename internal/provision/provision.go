package provision

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"dalforge-hub/dalcenter/internal/export"
	"dalforge-hub/dalcenter/internal/state"
)

// Spec describes what to provision.
type Spec struct {
	Base         string             // e.g. "ubuntu:24.04"
	InstanceName string             // used as hostname/CT description
	VMID         string             // if empty, auto-assigned by pct
	Packages     []string           // apt packages to install after create
	Agents       []export.AgentSpec // agents to install after packages
}

// Result of a provision attempt.
type Result struct {
	VMID     string
	Commands []string // all constructed commands (for dry-run)
	Error    error
}

// BuildCommand constructs the pct create command args without executing.
func BuildCommand(spec Spec) []string {
	args := []string{"create"}

	if spec.VMID != "" {
		args = append(args, spec.VMID)
	} else {
		args = append(args, "0") // 0 = next available
	}

	// Map base to ostemplate path
	ostemplate := resolveTemplate(spec.Base)
	args = append(args, "--ostemplate", ostemplate)
	args = append(args, "--hostname", sanitizeHostname(spec.InstanceName))
	args = append(args, "--storage", "local-lvm")
	args = append(args, "--memory", "512")
	args = append(args, "--rootfs", "local-lvm:4")
	args = append(args, "--unprivileged", "1")
	args = append(args, "--start", "0")

	return args
}

// AgentInstallInner returns the install command to run inside the container.
func AgentInstallInner(agent export.AgentSpec) string {
	switch agent.Type {
	case "claude_sdk":
		return fmt.Sprintf("npm install -g %s", agent.Package)
	case "codex_appserver":
		return fmt.Sprintf("npm install -g %s", agent.Package)
	case "gemini_cli":
		return fmt.Sprintf("pip3 install %s", agent.Package)
	default:
		return ""
	}
}

// BuildAgentInstallCommand returns the full pct exec command string for display/dry-run.
func BuildAgentInstallCommand(vmid string, agent export.AgentSpec) string {
	inner := AgentInstallInner(agent)
	if inner == "" {
		if agent.Type == "openai_compatible" {
			return fmt.Sprintf("# %s: openai_compatible (config-only, package=%s)", agent.Name, agent.Package)
		}
		return fmt.Sprintf("# %s: unknown type %q (skipped)", agent.Name, agent.Type)
	}
	return fmt.Sprintf("pct exec %s -- sh -lc '%s'", vmid, inner)
}

// BuildAgentInstallArgs returns the exec.Command args for pct exec.
func BuildAgentInstallArgs(vmid string, agent export.AgentSpec) []string {
	inner := AgentInstallInner(agent)
	if inner == "" {
		return nil
	}
	return []string{"exec", vmid, "--", "sh", "-lc", inner}
}

// BuildAllCommands returns the full provision command sequence: create + start + install + agents.
func BuildAllCommands(spec Spec) []string {
	createArgs := BuildCommand(spec)
	cmds := []string{"pct " + strings.Join(createArgs, " ")}

	vmid := spec.VMID
	if vmid == "" {
		vmid = "0"
	}

	needsStart := len(spec.Packages) > 0 || len(spec.Agents) > 0
	if needsStart {
		cmds = append(cmds, fmt.Sprintf("pct start %s", vmid))
	}

	if len(spec.Packages) > 0 {
		cmds = append(cmds, fmt.Sprintf("pct exec %s -- apt-get update -qq", vmid))
		cmds = append(cmds, fmt.Sprintf("pct exec %s -- apt-get install -y -qq %s", vmid, strings.Join(spec.Packages, " ")))
	}

	for _, agent := range spec.Agents {
		cmds = append(cmds, BuildAgentInstallCommand(vmid, agent))
	}

	return cmds
}

// BuildRollbackCommands returns stop + destroy for a failed provision.
func BuildRollbackCommands(vmid string) []string {
	return []string{
		fmt.Sprintf("pct stop %s --skiplock", vmid),
		fmt.Sprintf("pct destroy %s --purge", vmid),
	}
}

// BuildAllCommandsWithRollback returns provision commands + rollback suffix for dry-run display.
func BuildAllCommandsWithRollback(spec Spec) []string {
	cmds := BuildAllCommands(spec)
	vmid := spec.VMID
	if vmid == "" {
		vmid = "0"
	}
	if len(spec.Packages) > 0 {
		cmds = append(cmds, "# on failure:")
		cmds = append(cmds, BuildRollbackCommands(vmid)...)
	}
	return cmds
}

// Provision runs pct create (+ optional package install) and records result in state.json.
func Provision(instanceRoot string, spec Spec, dryRun bool) Result {
	if dryRun {
		cmds := BuildAllCommandsWithRollback(spec)
		return Result{Commands: cmds}
	}

	cmds := BuildAllCommands(spec)

	// Check pct exists
	if _, err := exec.LookPath("pct"); err != nil {
		r := Result{Commands: cmds, Error: fmt.Errorf("pct not found in PATH")}
		writeProvisionState(instanceRoot, "", "error", r.Error.Error(), "")
		return r
	}

	// Execute create
	createArgs := BuildCommand(spec)
	cmd := exec.Command("pct", createArgs...)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		errMsg := output
		if errMsg == "" {
			errMsg = err.Error()
		}
		r := Result{Commands: cmds, Error: fmt.Errorf("pct create failed: %s", errMsg)}
		writeProvisionState(instanceRoot, "", "error", errMsg, "")
		return r
	}

	vmid := spec.VMID
	if vmid == "" || vmid == "0" {
		vmid = strings.TrimSpace(output)
	}

	// Install packages if any
	if len(spec.Packages) > 0 {
		for _, step := range [][]string{
			{"start", vmid},
			{"exec", vmid, "--", "apt-get", "update", "-qq"},
			{"exec", vmid, "--", "apt-get", "install", "-y", "-qq"},
		} {
			if step[0] == "exec" && step[len(step)-1] == "-qq" {
				step = append(step, spec.Packages...)
			}
			stepCmd := exec.Command("pct", step...)
			if stepOut, err := stepCmd.CombinedOutput(); err != nil {
				errMsg := strings.TrimSpace(string(stepOut))
				// Rollback: stop + destroy
				rbStatus := rollback(vmid)
				writeProvisionState(instanceRoot, vmid, "error", "package install: "+errMsg, rbStatus)
				return Result{VMID: vmid, Commands: cmds, Error: fmt.Errorf("package install failed (rollback %s): %s", rbStatus, errMsg)}
			}
		}
	}

	// Install agents
	agentStatuses := map[string]string{}
	for _, agent := range spec.Agents {
		args := BuildAgentInstallArgs(vmid, agent)
		if args == nil {
			agentStatuses[agent.Name] = "skipped"
			continue
		}
		stepCmd := exec.Command("pct", args...)
		if stepOut, err := stepCmd.CombinedOutput(); err != nil {
			errMsg := strings.TrimSpace(string(stepOut))
			agentStatuses[agent.Name] = "error: " + errMsg
			continue
		}
		agentStatuses[agent.Name] = "installed"
	}

	writeProvisionState(instanceRoot, vmid, "provisioned", "", "")
	// Record agent statuses
	if len(agentStatuses) > 0 {
		if hs, err := state.Read(instanceRoot); err == nil {
			hs.AgentStatuses = agentStatuses
			state.Write(instanceRoot, *hs)
		}
	}
	return Result{VMID: vmid, Commands: cmds}
}

func rollback(vmid string) string {
	for _, args := range [][]string{
		{"stop", vmid, "--skiplock"},
		{"destroy", vmid, "--purge"},
	} {
		cmd := exec.Command("pct", args...)
		if _, err := cmd.CombinedOutput(); err != nil {
			return "failed"
		}
	}
	return "attempted"
}

func writeProvisionState(instanceRoot, vmid, status, errMsg, rollbackStatus string) {
	hs, _ := state.Read(instanceRoot)
	if hs == nil {
		hs = &state.HealthState{}
	}
	hs.VMID = vmid
	hs.ProvisionStatus = status
	hs.ProvisionedAt = time.Now().UTC().Format(time.RFC3339)
	hs.ProvisionError = errMsg
	hs.RollbackStatus = rollbackStatus
	state.Write(instanceRoot, *hs)
}

// DestroyResult of a destroy attempt.
type DestroyResult struct {
	Commands []string // constructed commands (for dry-run)
	Error    error
}

// BuildDestroyCommands constructs pct stop + pct destroy commands.
func BuildDestroyCommands(vmid string) [][]string {
	return [][]string{
		{"stop", vmid, "--skiplock"},
		{"destroy", vmid, "--purge"},
	}
}

// Destroy stops and destroys a provisioned container, clearing state.
func Destroy(instanceRoot string, dryRun bool) DestroyResult {
	hs, err := state.Read(instanceRoot)
	if err != nil {
		return DestroyResult{Error: fmt.Errorf("read state: %w", err)}
	}
	if hs.VMID == "" {
		return DestroyResult{} // no-op, nothing to destroy
	}

	cmds := BuildDestroyCommands(hs.VMID)
	var cmdStrs []string
	for _, args := range cmds {
		cmdStrs = append(cmdStrs, "pct "+strings.Join(args, " "))
	}

	if dryRun {
		return DestroyResult{Commands: cmdStrs}
	}

	if _, err := exec.LookPath("pct"); err != nil {
		r := DestroyResult{Commands: cmdStrs, Error: fmt.Errorf("pct not found in PATH")}
		hs.ProvisionStatus = "error"
		hs.ProvisionError = "destroy failed: pct not found"
		state.Write(instanceRoot, *hs)
		return r
	}

	// Stop (ignore error — may already be stopped)
	stopCmd := exec.Command("pct", cmds[0]...)
	stopCmd.CombinedOutput()

	// Destroy
	destroyCmd := exec.Command("pct", cmds[1]...)
	out, err := destroyCmd.CombinedOutput()
	if err != nil {
		errMsg := strings.TrimSpace(string(out))
		if errMsg == "" {
			errMsg = err.Error()
		}
		hs.ProvisionStatus = "error"
		hs.ProvisionError = "destroy failed: " + errMsg
		state.Write(instanceRoot, *hs)
		return DestroyResult{Commands: cmdStrs, Error: fmt.Errorf("pct destroy failed: %s", errMsg)}
	}

	// Clear provision state
	hs.VMID = ""
	hs.ProvisionStatus = ""
	hs.ProvisionedAt = ""
	hs.ProvisionError = ""
	state.Write(instanceRoot, *hs)
	return DestroyResult{Commands: cmdStrs}
}

func resolveTemplate(base string) string {
	// Map common short names to Proxmox template paths
	switch {
	case strings.HasPrefix(base, "ubuntu:"):
		ver := strings.TrimPrefix(base, "ubuntu:")
		return fmt.Sprintf("local:vztmpl/ubuntu-%s-standard_amd64.tar.zst", strings.ReplaceAll(ver, ".", ""))
	case strings.HasPrefix(base, "debian:"):
		ver := strings.TrimPrefix(base, "debian:")
		return fmt.Sprintf("local:vztmpl/debian-%s-standard_amd64.tar.zst", ver)
	default:
		return base // pass through as-is (full template path)
	}
}

func sanitizeHostname(name string) string {
	r := strings.NewReplacer("/", "-", "_", "-", ".", "-")
	h := r.Replace(name)
	if len(h) > 63 {
		h = h[:63]
	}
	return h
}
