package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/spf13/cobra"
)

func main() {
	dalName := os.Getenv("DAL_NAME")

	root := &cobra.Command{
		Use:   "dalcli-leader",
		Short: fmt.Sprintf("Dal CLI for leader dal (%s)", dalName),
	}

	root.AddCommand(wakeCmd(), sleepCmd(), psCmd(), statusCmd(), logsCmd(), syncCmd(), assignCmd(dalName))

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func wakeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wake <dal>",
		Short: "Wake a team member",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			result, err := client.Wake(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("wake: %s → %s\n", args[0], result["container_id"])
			return nil
		},
	}
}

func sleepCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sleep <dal>",
		Short: "Sleep a team member",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			if _, err := client.Sleep(args[0]); err != nil {
				return err
			}
			fmt.Printf("sleep: %s\n", args[0])
			return nil
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
			fmt.Fprintln(w, "NAME\tPLAYER\tROLE\tSTATUS\tCONTAINER")
			for _, c := range containers {
				cid := c.ContainerID
				if len(cid) > 12 {
					cid = cid[:12]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.DalName, c.Player, c.Role, c.Status, cid)
			}
			w.Flush()
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <dal>",
		Short: "Show team member status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			containers, err := client.Ps()
			if err != nil {
				return err
			}
			for _, c := range containers {
				if c.DalName == args[0] {
					fmt.Printf("name:      %s\n", c.DalName)
					fmt.Printf("uuid:      %s\n", c.UUID)
					fmt.Printf("player:    %s\n", c.Player)
					fmt.Printf("role:      %s\n", c.Role)
					fmt.Printf("status:    %s\n", c.Status)
					fmt.Printf("container: %s\n", c.ContainerID[:12])
					return nil
				}
			}
			return fmt.Errorf("dal %q not found", args[0])
		},
	}
}

func logsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <dal>",
		Short: "Show team member logs",
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

func syncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Sync changes to team",
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

func assignCmd(dalName string) *cobra.Command {
	return &cobra.Command{
		Use:   "assign <dal> <task>",
		Short: "Assign task to team member (via Mattermost)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			msg := fmt.Sprintf("@dal-%s 작업 지시: %s", args[0], args[1])
			result, err := client.Message(dalName, msg)
			if err != nil {
				return err
			}
			fmt.Printf("assigned: %s → %s (thread: %s)\n", args[0], args[1], result.PostID)
			return nil
		},
	}
}
