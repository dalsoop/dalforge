package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/spf13/cobra"
)

func main() {
	dalName := os.Getenv("DAL_NAME")

	root := &cobra.Command{
		Use:   "dalcli",
		Short: fmt.Sprintf("Dal CLI for member dal (%s)", dalName),
	}

	root.AddCommand(statusCmd(dalName), psCmd(), reportCmd(dalName), claimCmd(dalName), runCmd(dalName), proposeCmd(dalName), wisdomCmd(dalName))

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func statusCmd(dalName string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show my status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dalName == "" {
				return fmt.Errorf("DAL_NAME not set")
			}
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			containers, err := client.Ps()
			if err != nil {
				return err
			}
			for _, c := range containers {
				if c.DalName == dalName {
					fmt.Printf("name:      %s\n", c.DalName)
					fmt.Printf("uuid:      %s\n", c.UUID)
					fmt.Printf("player:    %s\n", c.Player)
					fmt.Printf("role:      %s\n", c.Role)
					fmt.Printf("status:    %s\n", c.Status)
					if c.IdleFor != "" {
						fmt.Printf("idle:      %s\n", c.IdleFor)
					}
					if !c.LastSeenAt.IsZero() {
						fmt.Printf("last_seen: %s\n", c.LastSeenAt.Local().Format(time.RFC3339))
					}
					fmt.Printf("container: %s\n", c.ContainerID[:12])
					return nil
				}
			}
			return fmt.Errorf("dal %q not found in running containers", dalName)
		},
	}
}

func psCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "List team dals",
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
			fmt.Fprintln(w, "NAME\tPLAYER\tROLE\tSTATUS\tIDLE\tLAST SEEN\tDESCRIPTION")
			for _, c := range containers {
				idle := c.IdleFor
				if idle == "" {
					idle = "-"
				}
				lastSeen := "-"
				if !c.LastSeenAt.IsZero() {
					lastSeen = c.LastSeenAt.Local().Format("2006-01-02 15:04:05")
				}
				desc := c.Description
				if desc == "" {
					desc = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.DalName, c.Player, c.Role, c.Status, idle, lastSeen, desc)
			}
			w.Flush()
			return nil
		},
	}
}

func claimCmd(dalName string) *cobra.Command {
	var claimType, detail, context string
	cmd := &cobra.Command{
		Use:   "claim <title>",
		Short: "Submit a claim/improvement request to the host",
		Long: `Submit feedback to the host operator.

Types:
  bug          — something broken
  improvement  — suggestion for better tooling
  blocked      — can't proceed without host action
  env          — environment issue (missing tool, auth, disk)

Examples:
  dalcli claim --type bug "cargo not installed in container"
  dalcli claim --type improvement "need --timeout flag for task execution"
  dalcli claim --type blocked --detail "GH_TOKEN not set, can't push" "GitHub auth missing"
  dalcli claim --type env "disk full, can't build Rust image"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			title := args[0]
			result, err := client.Claim(dalName, claimType, title, detail, context)
			if err != nil {
				return err
			}
			fmt.Printf("[claim] submitted: %s (%s) — %s\n", result.ID, claimType, title)
			return nil
		},
	}
	cmd.Flags().StringVar(&claimType, "type", "improvement", "Claim type: bug|improvement|blocked|env")
	cmd.Flags().StringVar(&detail, "detail", "", "Detailed description")
	cmd.Flags().StringVar(&context, "context", "", "Task or repo context")
	return cmd
}

func reportCmd(dalName string) *cobra.Command {
	return &cobra.Command{
		Use:   "report <message>",
		Short: "Report to leader (via bridge)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			msg := fmt.Sprintf("[%s] 보고: %s", dalName, args[0])
			if _, err := client.Message(dalName, msg); err != nil {
				return err
			}
			fmt.Printf("reported: %s\n", args[0])
			return nil
		},
	}
}
