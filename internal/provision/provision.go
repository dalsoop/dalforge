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
	Storage      string             // --storage (default: local-lvm)
	Bridge       string             // --bridge network bridge (default: vmbr0)
	Memory       string             // --memory in MB (default: 512)
	Cores        string             // --cores (default: 1)
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

	// ostemplate is a positional arg: pct create <vmid> <ostemplate> [OPTIONS]
	ostemplate := resolveTemplate(spec.Base)
	args = append(args, ostemplate)
	args = append(args, "--hostname", sanitizeHostname(spec.InstanceName))

	storage := spec.Storage
	if storage == "" {
		storage = "local-lvm"
	}
	memory := spec.Memory
	if memory == "" {
		memory = "512"
	}
	cores := spec.Cores
	if cores == "" {
		cores = "1"
	}

	args = append(args, "--storage", storage)
	args = append(args, "--memory", memory)
	args = append(args, "--cores", cores)
	args = append(args, "--rootfs", storage+":4")
	args = append(args, "--unprivileged", "1")
	args = append(args, "--start", "0")

	if spec.Bridge != "" {
		args = append(args, "--net0", fmt.Sprintf("name=eth0,bridge=%s,ip=dhcp", spec.Bridge))
	}

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

	// Credential sync step
	if filter := agentFilter(spec.Agents); filter != "" {
		cmds = append(cmds, fmt.Sprintf("proxmox-host-setup ai mount --vmid %s --agent %s", vmid, filter))
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
		installStep := append([]string{"exec", vmid, "--", "apt-get", "install", "-y", "-qq"}, spec.Packages...)
		for _, step := range [][]string{
			{"start", vmid},
			{"exec", vmid, "--", "apt-get", "update", "-qq"},
			installStep,
		} {
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

	// Sync AI agent credentials from host into container
	syncAgentCredentials(vmid, spec.Agents)

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

// syncAgentCredentials calls proxmox-host-setup to copy host credentials into the container.
func syncAgentCredentials(vmid string, agents []export.AgentSpec) {
	phs, err := exec.LookPath("proxmox-host-setup")
	if err != nil {
		fmt.Println("[provision] proxmox-host-setup not found, skipping credential sync")
		return
	}

	// Determine which agents to mount
	filter := agentFilter(agents)
	if filter == "" {
		return
	}

	fmt.Printf("[provision] syncing agent credentials → LXC %s (filter=%s)\n", vmid, filter)
	cmd := exec.Command(phs, "ai", "mount", "--vmid", vmid, "--agent", filter)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		fmt.Printf("[provision] credential sync warning: %s\n", output)
	} else if output != "" {
		fmt.Println(output)
	}
}

// agentFilter maps dalforge agent types to proxmox-host-setup agent filter values.
func agentFilter(agents []export.AgentSpec) string {
	has := map[string]bool{}
	for _, a := range agents {
		switch a.Type {
		case "claude_sdk":
			has["claude"] = true
		case "codex_appserver":
			has["codex"] = true
		case "gemini_cli":
			has["gemini"] = true
		}
	}
	if len(has) == 0 {
		return ""
	}
	if has["claude"] && has["codex"] && has["gemini"] {
		return "all"
	}
	if len(has) == 1 {
		for k := range has {
			return k
		}
	}
	// Multiple but not all — call with "all" and let proxmox-host-setup handle it
	return "all"
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
	switch {
	case strings.HasPrefix(base, "ubuntu:") || strings.HasPrefix(base, "debian:"):
		// Try to find matching template via pveam list
		if t := findTemplate(base); t != "" {
			return t
		}
		// Fallback to constructed name
		parts := strings.SplitN(base, ":", 2)
		return fmt.Sprintf("local:vztmpl/%s-%s-standard_amd64.tar.zst", parts[0], parts[1])
	default:
		return base
	}
}

func findTemplate(base string) string {
	parts := strings.SplitN(base, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	distro, ver := parts[0], parts[1]
	out, err := exec.Command("pveam", "list", "local").CombinedOutput()
	if err != nil {
		return ""
	}
	// Find line matching distro and version
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, distro+"-"+ver) && strings.Contains(line, "standard") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

func sanitizeHostname(name string) string {
	r := strings.NewReplacer("/", "-", "_", "-", ".", "-")
	h := r.Replace(name)
	if len(h) > 63 {
		h = h[:63]
	}
	return h
}
