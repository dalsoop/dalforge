package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"dalforge-hub/dalcenter/internal/export"
	"dalforge-hub/dalcenter/internal/registry"
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

	root.AddCommand(joinCmd(), listCmd(), statusCmd(), secretCmd(), validateCmd(), exportCmd(), unexportCmd())

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
			manifestPath, err := validate.ResolveManifestPath(args[0])
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
			inst, err := reg.Join("default", plan.RepoRoot, plan.Manifest, export.SkillCount(plan))
			if err != nil {
				return err
			}
			fmt.Printf("instance created: %s (template=%s, skills=%d)\n", inst.DalID, inst.Template, inst.ExportedSkills)
			return nil
		},
	}
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
			fmt.Fprintln(tw, "DAL_ID\tTEMPLATE\tSTATUS\tSKILLS\tREPO\tCREATED")
			for _, i := range instances {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", i.DalID, i.Template, i.Status, i.ExportedSkills, i.RepoRoot, i.CreatedAt)
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
			inst, err := reg.Status(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("dal_id:         %s\ntemplate:       %s\nstatus:         %s\ncontainer_id:   %s\nrepo_root:      %s\nmanifest_path:  %s\nexported_skills:%d\ncreated_at:     %s\n",
				inst.DalID, inst.Template, inst.Status, inst.ContainerID, inst.RepoRoot, inst.ManifestPath, inst.ExportedSkills, inst.CreatedAt)
			return nil
		},
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
