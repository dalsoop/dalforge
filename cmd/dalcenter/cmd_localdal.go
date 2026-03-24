package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"

	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/dalsoop/dalcenter/internal/localdal"
	"github.com/spf13/cobra"
)

func localdalRoot() string {
	if p := os.Getenv("DALCENTER_LOCALDAL_PATH"); p != "" {
		return p
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, ".dal")
}

// --- serve ---

func newServeCmd() *cobra.Command {
	var addr, serviceRepo, mmURL, mmToken, mmTeam string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run dalcenter daemon (HTTP API + Docker management)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := localdalRoot()
			var mm *daemon.MattermostConfig
			if mmURL != "" {
				mm = &daemon.MattermostConfig{
					URL:        mmURL,
					AdminToken: mmToken,
					TeamName:   mmTeam,
				}
			}
			d := daemon.New(addr, root, serviceRepo, mm)

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			return d.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":11190", "Listen address")
	cmd.Flags().StringVar(&serviceRepo, "repo", "", "Service repository path to mount as /workspace")
	cmd.Flags().StringVar(&mmURL, "mm-url", os.Getenv("DALCENTER_MM_URL"), "Mattermost URL")
	cmd.Flags().StringVar(&mmToken, "mm-token", os.Getenv("DALCENTER_MM_TOKEN"), "Mattermost admin token")
	cmd.Flags().StringVar(&mmTeam, "mm-team", os.Getenv("DALCENTER_MM_TEAM"), "Mattermost team name")
	return cmd
}

// --- init ---

func newInitCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a localdal repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := localdalRoot()
			if err := localdal.Init(root); err != nil {
				return err
			}
			fmt.Printf("localdal initialized: %s\n", root)

			// soft-serve 레포 생성 + subtree 연결
			if repoPath != "" {
				repoName := filepath.Base(repoPath) + "-localdal"
				if err := daemon.EnsureSoftServeRepo(repoName); err != nil {
					fmt.Fprintf(os.Stderr, "warning: soft-serve repo: %v\n", err)
				} else {
					fmt.Printf("soft-serve repo: %s\n", repoName)
				}
				if err := daemon.SetupSubtree(repoPath, repoName); err != nil {
					fmt.Fprintf(os.Stderr, "warning: subtree: %v\n", err)
				} else {
					fmt.Printf("subtree linked: %s/.dal → %s\n", repoPath, repoName)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", "", "Service repository path")
	return cmd
}

// --- validate ---

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [path]",
		Short: "Validate localdal CUE schema and skill references",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := localdalRoot()
			if len(args) > 0 {
				root = args[0]
			}
			errors := localdal.Validate(root)
			if len(errors) == 0 {
				fmt.Println("ok")
				return nil
			}
			for _, e := range errors {
				fmt.Fprintf(os.Stderr, "error: %s\n", e)
			}
			return fmt.Errorf("%d validation error(s)", len(errors))
		},
	}
}

// --- wake ---

func newWakeCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "wake [dal]",
		Short: "Wake a dal (start Docker container)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			if all {
				root := localdalRoot()
				dals, err := localdal.ListDals(root)
				if err != nil {
					return err
				}
				for _, d := range dals {
					result, err := client.Wake(d.Name)
					if err != nil {
						fmt.Fprintf(os.Stderr, "wake %s: %v\n", d.Name, err)
						continue
					}
					fmt.Printf("wake: %s → %s\n", d.Name, result["container_id"][:12])
				}
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("specify dal name or use --all")
			}
			result, err := client.Wake(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("wake: %s → %s\n", args[0], result["container_id"])
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Wake all dals")
	return cmd
}

// --- sleep ---

func newSleepCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "sleep [dal]",
		Short: "Sleep a dal (stop Docker container)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			if all {
				containers, err := client.Ps()
				if err != nil {
					return err
				}
				for _, c := range containers {
					if _, err := client.Sleep(c.DalName); err != nil {
						fmt.Fprintf(os.Stderr, "sleep %s: %v\n", c.DalName, err)
						continue
					}
					fmt.Printf("sleep: %s\n", c.DalName)
				}
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("specify dal name or use --all")
			}
			if _, err := client.Sleep(args[0]); err != nil {
				return err
			}
			fmt.Printf("sleep: %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Sleep all dals")
	return cmd
}

// --- sync ---

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Sync changes to awake dal containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			result, err := client.Sync()
			if err != nil {
				return err
			}
			fmt.Printf("sync: %v\n", result)
			return nil
		},
	}
}

// --- ps ---

func newPsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "List awake dals",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			containers, err := client.Ps()
			if err != nil {
				return err
			}
			if len(containers) == 0 {
				fmt.Println("no awake dals")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tPLAYER\tROLE\tSTATUS\tCONTAINER")
			for _, c := range containers {
				cid := c.ContainerID
				if len(cid) > 12 {
					cid = cid[:12]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					c.DalName, c.Player, c.Role, c.Status, cid)
			}
			w.Flush()
			return nil
		},
	}
}

// --- status ---

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [dal]",
		Short: "Show dal status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := localdalRoot()
			if len(args) == 0 {
				dals, err := localdal.ListDals(root)
				if err != nil {
					return err
				}
				if len(dals) == 0 {
					fmt.Println("no dals found")
					return nil
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "NAME\tPLAYER\tROLE\tSKILLS\tUUID")
				for _, d := range dals {
					uuid := d.UUID
					if len(uuid) > 8 {
						uuid = uuid[:8] + "..."
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
						d.Name, d.Player, d.Role, len(d.Skills), uuid)
				}
				w.Flush()
				return nil
			}
			dalCue := filepath.Join(root, args[0], "dal.cue")
			p, err := localdal.ReadDalCue(dalCue, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("uuid:    %s\n", p.UUID)
			fmt.Printf("name:    %s\n", p.Name)
			fmt.Printf("version: %s\n", p.Version)
			fmt.Printf("player:  %s\n", p.Player)
			fmt.Printf("role:    %s\n", p.Role)
			fmt.Printf("skills:  %v\n", p.Skills)
			fmt.Printf("hooks:   %v\n", p.Hooks)
			return nil
		},
	}
}

// --- logs ---

func newLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <dal>",
		Short: "Show dal container logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			logs, err := client.Logs(args[0])
			if err != nil {
				return err
			}
			fmt.Print(logs)
			return nil
		},
	}
}

// --- attach ---

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <dal>",
		Short: "Attach to dal container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			containerName := fmt.Sprintf("dal-%s", args[0])
			c := exec.Command("docker", "exec", "-it", containerName, "/bin/bash")
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
}
