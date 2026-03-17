package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"dalforge-hub/dalcenter/internal/cloud"
	"dalforge-hub/dalcenter/internal/export"
	"dalforge-hub/dalcenter/internal/provision"
	"dalforge-hub/dalcenter/internal/registry"
	"dalforge-hub/dalcenter/internal/runner"
	"dalforge-hub/dalcenter/internal/state"
	"dalforge-hub/dalcenter/internal/validate"
	"dalforge-hub/dalcenter/internal/vault"

	"github.com/spf13/cobra"
)

func dataDir() string {
	d := os.Getenv("DALCENTER_DATA")
	if d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dalcenter")
}

func ensureDataDir() error {
	return os.MkdirAll(dataDir(), 0700)
}

func instancesDir() string {
	return filepath.Join(dataDir(), "instances")
}

func specPath() string {
	if p := os.Getenv("DALCENTER_SPEC"); p != "" {
		return p
	}
	if wd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(wd, "dal.spec.cue")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join("/root", "dalforge-dalcenter", "dal.spec.cue")
}

func openVault() (*vault.SecretVault, error) {
	if err := ensureDataDir(); err != nil {
		return nil, err
	}
	return vault.Open(filepath.Join(dataDir(), "vault.db"))
}

func openRegistry() (*registry.Registry, error) {
	if err := ensureDataDir(); err != nil {
		return nil, err
	}
	return registry.Open(filepath.Join(dataDir(), "registry.db"))
}

func main() {
	root := &cobra.Command{
		Use:   "dalcenter",
		Short: "DalForge local dal center CLI",
	}

	root.AddCommand(joinCmd(), listCmd(), statusCmd(), secretCmd(), validateCmd(), exportCmd(), unexportCmd(), startCmd(), stopCmd(), restartCmd(), reconcileCmd(), watchCmd(), provisionCmd(), destroyCmd(), catalogCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func joinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join [repo-or-manifest]",
		Short: "Create a new localdal instance from a .dalfactory manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sourcePath, err := resolveJoinSource(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			manifestPath, err := validate.ResolveManifestPath(sourcePath)
			if err != nil {
				return err
			}
			if _, err := validate.Manifest(specPath(), manifestPath); err != nil {
				return err
			}
			plan, err := export.LoadPlan(manifestPath)
			if err != nil {
				return err
			}
			if err := export.Apply(plan); err != nil {
				return err
			}

			reg, err := openRegistry()
			if err != nil {
				return err
			}
			defer reg.Close()
			inst, err := reg.Join("default", plan.RepoRoot, plan.Manifest, "", export.SkillCount(plan))
			if err != nil {
				return err
			}
			actualRoot := filepath.Join(instancesDir(), inst.DalID)
			if err := createInstanceLayout(actualRoot, plan.Manifest); err != nil {
				return err
			}
			if err := export.ApplyTo(plan, instanceRuntimeHomes(actualRoot)); err != nil {
				return err
			}
			if actualRoot != inst.InstanceRoot {
				inst.InstanceRoot = actualRoot
				if err := reg.UpdateInstanceRoot(inst.DalID, actualRoot); err != nil {
					return err
				}
			}
			fmt.Printf("instance created: %s (template=%s, skills=%d)\n", inst.DalID, inst.Template, inst.ExportedSkills)

			// Health check (1-shot, non-blocking)
			if plan.HealthCheckCmd != "" {
				hs := runHealthCheck(plan.RepoRoot, plan.HealthCheckCmd)
				if err := state.Write(actualRoot, hs); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to write state: %v\n", err)
				}
				if hs.Status == "fail" {
					fmt.Fprintf(os.Stderr, "warning: health check failed (exit %d): %s\n", hs.HealthExit, hs.HealthOutput)
				} else {
					fmt.Printf("health check: ok\n")
				}
			}

			return nil
		},
	}
}

func catalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Browse dalforge cloud packages",
	}
	cmd.AddCommand(catalogSearchCmd())
	return cmd
}

func catalogSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search [query]",
		Short: "Search packages in dalforge cloud",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := cloud.New(cloud.LoadConfig())
			var pkgs []cloud.PackageInfo
			var err error
			if len(args) == 0 {
				pkgs, err = client.ListAll(cmd.Context())
			} else {
				pkgs, err = client.Search(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			if len(pkgs) == 0 {
				fmt.Println("no packages found")
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tBRANCH\tDESCRIPTION")
			for _, pkg := range pkgs {
				desc := strings.TrimSpace(pkg.Description)
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", pkg.Path, pkg.DefaultBranch, desc)
			}
			return tw.Flush()
		},
	}
}

func resolveJoinSource(ctx context.Context, arg string) (string, error) {
	if _, err := os.Stat(arg); err == nil {
		return arg, nil
	}
	if filepath.IsAbs(arg) {
		return "", fmt.Errorf("path %q not found", arg)
	}

	client := cloud.New(cloud.LoadConfig())
	cacheRoot := filepath.Join(dataDir(), "sources")
	staged, pkg, err := client.Stage(ctx, arg, cacheRoot)
	if err != nil {
		return "", err
	}
	fmt.Printf("staged package: %s -> %s\n", pkg.Path, staged)
	return staged, nil
}

func runHealthCheck(repoRoot, command string) state.HealthState {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()

	output := strings.TrimSpace(string(out))
	if len(output) > 200 {
		output = output[:200]
	}

	if err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return state.New("fail", exitCode, output)
	}
	return state.New("ok", 0, output)
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all localdal instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := openRegistry()
			if err != nil {
				return err
			}
			defer reg.Close()
			instances, err := reg.List()
			if err != nil {
				return err
			}
			if len(instances) == 0 {
				fmt.Println("no instances")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "DAL_ID\tTEMPLATE\tSTATUS\tHEALTH\tSKILLS\tCREATED")
			for _, i := range instances {
				health := "unchecked"
				if i.InstanceRoot != "" {
					if hs, err := state.Read(i.InstanceRoot); err == nil {
						health = formatHealth(hs)
					}
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", i.DalID, i.Template, i.Status, health, i.ExportedSkills, i.CreatedAt)
			}
			return tw.Flush()
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show instance status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := openRegistry()
			if err != nil {
				return err
			}
			defer reg.Close()
			result, err := reg.Status(args[0])
			if err != nil {
				return err
			}
			if len(result.Candidates) > 1 {
				fmt.Fprintf(os.Stderr, "warning: %d instances match %q, showing most recent:\n", len(result.Candidates), args[0])
				for _, c := range result.Candidates {
					fmt.Fprintf(os.Stderr, "  %s  %s\n", c.DalID, c.RepoRoot)
				}
			}
			inst := result.Instance
			fmt.Printf("dal_id:         %s\ntemplate:       %s\nstatus:         %s\ncontainer_id:   %s\nrepo_root:      %s\nmanifest_path:  %s\ninstance_root:  %s\nexported_skills:%d\ncreated_at:     %s\n",
				inst.DalID, inst.Template, inst.Status, inst.ContainerID, inst.RepoRoot, inst.ManifestPath, inst.InstanceRoot, inst.ExportedSkills, inst.CreatedAt)

			// Merge state.json if available
			if inst.InstanceRoot != "" {
				if hs, err := runner.Check(inst.InstanceRoot); err == nil {
					fmt.Printf("health_status:  %s\n", hs.Status)
					fmt.Printf("health_exit:    %d\n", hs.HealthExit)
					fmt.Printf("health_checked: %s\n", hs.CheckedAt)
					if hs.Status == "fail" && hs.HealthOutput != "" {
						fmt.Printf("health_output:  %s\n", hs.HealthOutput)
					}
					if hs.RunStatus != "" {
						fmt.Printf("run_status:     %s\n", hs.RunStatus)
					}
					if hs.Pid > 0 {
						fmt.Printf("pid:            %d\n", hs.Pid)
					}
					if hs.StartedAt != "" {
						fmt.Printf("started_at:     %s\n", hs.StartedAt)
					}
				}
			}
			return nil
		},
	}
}

func createInstanceLayout(instanceRoot, manifestPath string) error {
	for _, dir := range []string{
		instanceRoot,
		filepath.Join(instanceRoot, "meta"),
		filepath.Join(instanceRoot, "runtime"),
		filepath.Join(instanceRoot, "logs"),
		filepath.Join(instanceRoot, "state"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(instanceRoot, "meta", "dal.cue"), data, 0644)
}

func instanceRuntimeHomes(instanceRoot string) map[string]string {
	return map[string]string{
		"claude": filepath.Join(instanceRoot, "runtime", "claude"),
		"codex":  filepath.Join(instanceRoot, "runtime", "codex"),
	}
}

func formatHealth(hs *state.HealthState) string {
	ago := relativeTime(hs.CheckedAt)
	switch hs.Status {
	case "ok":
		return fmt.Sprintf("ok(%s)", ago)
	case "fail":
		return fmt.Sprintf("fail(%s)", ago)
	default:
		return "unchecked"
	}
}

func relativeTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func secretCmd() *cobra.Command {
	sec := &cobra.Command{
		Use:   "secret",
		Short: "Manage secrets",
	}
	sec.AddCommand(secretSetCmd(), secretGetCmd(), secretListCmd())
	return sec
}

func secretSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set [name]",
		Short: "Store a secret (reads value from stdin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := openVault()
			if err != nil {
				return err
			}
			defer v.Close()
			fmt.Fprint(os.Stderr, "enter secret value: ")
			var value string
			if _, err := fmt.Scanln(&value); err != nil {
				return fmt.Errorf("read value: %w", err)
			}
			if err := v.Set(args[0], []byte(value)); err != nil {
				return err
			}
			fmt.Printf("secret %q saved\n", args[0])
			return nil
		},
	}
}

func secretGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [name]",
		Short: "Retrieve a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := openVault()
			if err != nil {
				return err
			}
			defer v.Close()
			plain, err := v.Get(args[0])
			if err != nil {
				return err
			}
			fmt.Println(string(plain))
			return nil
		},
	}
}

func secretListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := openVault()
			if err != nil {
				return err
			}
			defer v.Close()
			entries, err := v.List()
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("no secrets")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tUPDATED")
			for _, e := range entries {
				fmt.Fprintf(tw, "%s\t%s\n", e.Name, e.UpdatedAt)
			}
			return tw.Flush()
		},
	}
}

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [repo-or-manifest...]",
		Short: "Validate .dalfactory manifests against dal.spec.cue",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := args
			if len(paths) == 0 {
				paths = []string{"."}
			}

			specPath := specPath()
			hasError := false
			for _, path := range paths {
				manifestPath, err := validate.ResolveManifestPath(path)
				if err != nil {
					hasError = true
					fmt.Fprintf(os.Stderr, "invalid %s: %v\n", path, err)
					continue
				}
				if _, err := validate.Manifest(specPath, manifestPath); err != nil {
					hasError = true
					fmt.Fprintf(os.Stderr, "invalid %s: %v\n", manifestPath, err)
					continue
				}
				fmt.Printf("ok %s\n", manifestPath)
			}
			if hasError {
				return fmt.Errorf("manifest validation failed")
			}
			return nil
		},
	}
}

func exportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export [repo-or-manifest...]",
		Short: "Export declared Claude skills from .dalfactory manifests",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := args
			if len(paths) == 0 {
				paths = []string{"."}
			}

			hasError := false
			for _, path := range paths {
				plan, err := export.LoadPlan(path)
				if err != nil {
					hasError = true
					fmt.Fprintf(os.Stderr, "invalid %s: %v\n", path, err)
					continue
				}
				if err := export.Apply(plan); err != nil {
					hasError = true
					fmt.Fprintf(os.Stderr, "export failed %s: %v\n", plan.Manifest, err)
					continue
				}
				fmt.Printf("ok %s (%d skills)\n", plan.Manifest, export.SkillCount(plan))
			}
			if hasError {
				return fmt.Errorf("export failed")
			}
			return nil
		},
	}
}

func unexportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unexport [repo-or-manifest...]",
		Short: "Remove exported Claude skills declared in .dalfactory manifests",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := args
			if len(paths) == 0 {
				paths = []string{"."}
			}

			hasError := false
			for _, path := range paths {
				plan, err := export.LoadPlan(path)
				if err != nil {
					hasError = true
					fmt.Fprintf(os.Stderr, "invalid %s: %v\n", path, err)
					continue
				}
				if err := export.Remove(plan); err != nil {
					hasError = true
					fmt.Fprintf(os.Stderr, "unexport failed %s: %v\n", plan.Manifest, err)
					continue
				}
				fmt.Printf("ok %s (%d skills)\n", plan.Manifest, export.SkillCount(plan))
			}
			if hasError {
				return fmt.Errorf("unexport failed")
			}
			return nil
		},
	}
}

func resolveInstanceRoot(name string) (string, error) {
	reg, err := openRegistry()
	if err != nil {
		return "", err
	}
	defer reg.Close()
	result, err := reg.Status(name)
	if err != nil {
		return "", err
	}
	if result.Instance.InstanceRoot == "" {
		return "", fmt.Errorf("instance %q has no instance root", name)
	}
	return result.Instance.InstanceRoot, nil
}

// resolveDefaultCmd returns (command, repoRoot) from the manifest.
func resolveDefaultCmd(name string) (string, string) {
	reg, err := openRegistry()
	if err != nil {
		return "", ""
	}
	defer reg.Close()
	result, err := reg.Status(name)
	if err != nil || result.Instance.ManifestPath == "" {
		return "", ""
	}
	plan, err := export.LoadPlan(result.Instance.ManifestPath)
	if err != nil {
		return "", ""
	}
	// Priority 1: build.entry
	if plan.DefaultCmd != "" {
		return plan.DefaultCmd, plan.RepoRoot
	}
	// Priority 2: first executable agent package in repo_root
	for _, pkg := range plan.AgentPackages {
		candidate := filepath.Join(plan.RepoRoot, pkg)
		if info, err := os.Stat(candidate); err == nil && info.Mode()&0111 != 0 {
			return pkg, plan.RepoRoot
		}
		if _, err := exec.LookPath(pkg); err == nil {
			return pkg, plan.RepoRoot
		}
	}
	return "", plan.RepoRoot
}

func startCmd() *cobra.Command {
	var command string
	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "Start a process in a localdal instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveInstanceRoot(args[0])
			if err != nil {
				return err
			}
			var workDir string
			if command == "" {
				command, workDir = resolveDefaultCmd(args[0])
			}
			if command == "" {
				return fmt.Errorf("no --command given and no build.entry or executable agents in .dalfactory")
			}
			pid, err := runner.Start(root, command, workDir)
			if err != nil {
				return err
			}
			fmt.Printf("started %q (pid %d)\n", command, pid)
			return nil
		},
	}
	cmd.Flags().StringVarP(&command, "command", "c", "", "command to run (default: build.entry from .dalfactory)")
	return cmd
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop the running process in a localdal instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveInstanceRoot(args[0])
			if err != nil {
				return err
			}
			if err := runner.Stop(root); err != nil {
				return err
			}
			fmt.Println("stopped")
			return nil
		},
	}
}

func restartCmd() *cobra.Command {
	var command string
	cmd := &cobra.Command{
		Use:   "restart <name>",
		Short: "Restart the process in a localdal instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveInstanceRoot(args[0])
			if err != nil {
				return err
			}
			runner.Stop(root)
			var workDir string
			if command == "" {
				command, workDir = resolveDefaultCmd(args[0])
			}
			if command == "" {
				return fmt.Errorf("no --command given and no build.entry or executable agents in .dalfactory")
			}
			pid, err := runner.Start(root, command, workDir)
			if err != nil {
				return err
			}
			fmt.Printf("restarted %q (pid %d)\n", command, pid)
			return nil
		},
	}
	cmd.Flags().StringVarP(&command, "command", "c", "", "command to run")
	return cmd
}

func reconcileAll(quiet bool) error {
	reg, err := openRegistry()
	if err != nil {
		return err
	}
	defer reg.Close()

	instances, err := reg.List()
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		if !quiet {
			fmt.Println("no instances to reconcile")
		}
		return nil
	}

	hasError := false
	for _, inst := range instances {
		if inst.ManifestPath == "" {
			continue
		}
		plan, err := export.LoadPlan(inst.ManifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: error loading manifest: %v\n", inst.DalID, err)
			hasError = true
			continue
		}

		homes := map[string]string{}
		if inst.InstanceRoot != "" {
			homes = instanceRuntimeHomes(inst.InstanceRoot)
		}

		result := export.Reconcile(plan, homes)

		if inst.InstanceRoot != "" {
			if hs, err := runner.Check(inst.InstanceRoot); err == nil {
				if hs.RunStatus == "stopped" && hs.Pid == 0 && hs.StartedAt != "" {
					result.Repairs = append(result.Repairs, "process: stale pid corrected")
					if result.Status == "ok" {
						result.Status = "repaired"
					}
				}
			}
		}

		if quiet && result.Status == "ok" {
			continue
		}

		switch result.Status {
		case "ok":
			fmt.Printf("  %s: ok\n", inst.DalID)
		case "repaired":
			fmt.Printf("  %s: repaired\n", inst.DalID)
			for _, r := range result.Repairs {
				fmt.Printf("    - %s\n", r)
			}
		case "error":
			hasError = true
			fmt.Fprintf(os.Stderr, "  %s: error\n", inst.DalID)
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "    - %s\n", e)
			}
			for _, r := range result.Repairs {
				fmt.Printf("    - %s\n", r)
			}
		}
	}

	if hasError {
		return fmt.Errorf("reconcile completed with errors")
	}
	return nil
}

func reconcileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile",
		Short: "Check and repair all instance exports (skills, hooks, settings)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return reconcileAll(false)
		},
	}
}

func watchPidPath() string {
	return filepath.Join(dataDir(), "watch.pid")
}

func acquireWatchLock() error {
	pidPath := watchPidPath()
	data, err := os.ReadFile(pidPath)
	if err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 && runner.IsAlive(pid) {
			return fmt.Errorf("watch already running (pid %d)", pid)
		}
	}
	return os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func releaseWatchLock() {
	os.Remove(watchPidPath())
}

func watchCmd() *cobra.Command {
	var interval int
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Continuously reconcile all instances on a timer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureDataDir(); err != nil {
				return err
			}
			if err := acquireWatchLock(); err != nil {
				return err
			}
			defer releaseWatchLock()

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			dur := time.Duration(interval) * time.Second
			fmt.Printf("watch started (interval=%ds, pid=%d)\n", interval, os.Getpid())

			// Run once immediately
			if err := reconcileAll(true); err != nil {
				fmt.Fprintf(os.Stderr, "reconcile: %v\n", err)
			}

			ticker := time.NewTicker(dur)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					fmt.Println("\nwatch stopped")
					return nil
				case <-ticker.C:
					if err := reconcileAll(true); err != nil {
						fmt.Fprintf(os.Stderr, "reconcile: %v\n", err)
					}
				}
			}
		},
	}
	cmd.Flags().IntVar(&interval, "interval", 60, "reconcile interval in seconds")
	return cmd
}

func provisionCmd() *cobra.Command {
	var vmid, storage, bridge, memory, cores string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "provision <name>",
		Short: "Provision a container for a localdal instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := openRegistry()
			if err != nil {
				return err
			}
			defer reg.Close()
			result, err := reg.Status(args[0])
			if err != nil {
				return err
			}
			inst := result.Instance
			if inst.ManifestPath == "" {
				return fmt.Errorf("instance %q has no manifest", args[0])
			}
			plan, err := export.LoadPlan(inst.ManifestPath)
			if err != nil {
				return err
			}
			if plan.ContainerBase == "" {
				return fmt.Errorf("no container.base defined in manifest")
			}

			spec := provision.Spec{
				Base:         plan.ContainerBase,
				InstanceName: inst.DalID,
				VMID:         vmid,
				Packages:     plan.ContainerPackages,
				Agents:       plan.Agents,
				Storage:      storage,
				Bridge:       bridge,
				Memory:       memory,
				Cores:        cores,
			}

			r := provision.Provision(inst.InstanceRoot, spec, dryRun)
			if dryRun {
				for _, c := range r.Commands {
					fmt.Printf("dry-run: %s\n", c)
				}
				return nil
			}
			if r.Error != nil {
				return r.Error
			}
			fmt.Printf("provisioned (vmid=%s)\n", r.VMID)
			return nil
		},
	}
	cmd.Flags().StringVar(&vmid, "vmid", "", "explicit VMID (default: auto)")
	cmd.Flags().StringVar(&storage, "storage", "", "storage pool (default: local-lvm)")
	cmd.Flags().StringVar(&bridge, "bridge", "", "network bridge (e.g. vmbr0)")
	cmd.Flags().StringVar(&memory, "memory", "", "memory in MB (default: 512)")
	cmd.Flags().StringVar(&cores, "cores", "", "CPU cores (default: 1)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print command without executing")
	return cmd
}

func destroyCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "destroy <name>",
		Short: "Stop and destroy a provisioned container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveInstanceRoot(args[0])
			if err != nil {
				return err
			}
			r := provision.Destroy(root, dryRun)
			if dryRun {
				if len(r.Commands) == 0 {
					fmt.Println("nothing to destroy")
				} else {
					for _, c := range r.Commands {
						fmt.Printf("dry-run: %s\n", c)
					}
				}
				return nil
			}
			if r.Error != nil {
				return r.Error
			}
			if len(r.Commands) == 0 {
				fmt.Println("nothing to destroy (no provisioned container)")
			} else {
				fmt.Println("destroyed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print commands without executing")
	return cmd
}
