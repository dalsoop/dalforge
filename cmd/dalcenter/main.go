package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"dalforge-hub/dalcenter/internal/registry"
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

	root.AddCommand(joinCmd(), listCmd(), statusCmd(), secretCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func joinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join [template]",
		Short: "Create a new localdal instance from a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := openRegistry()
			if err != nil {
				return err
			}
			defer reg.Close()
			inst, err := reg.Join(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("instance created: %s (template=%s)\n", inst.DalID, inst.Template)
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
			fmt.Fprintln(tw, "DAL_ID\tTEMPLATE\tSTATUS\tCREATED")
			for _, i := range instances {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", i.DalID, i.Template, i.Status, i.CreatedAt)
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
			fmt.Printf("dal_id:       %s\ntemplate:     %s\nstatus:       %s\ncontainer_id: %s\ncreated_at:   %s\n",
				inst.DalID, inst.Template, inst.Status, inst.ContainerID, inst.CreatedAt)
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
