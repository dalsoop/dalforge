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
		Use:   "dalcli",
		Short: fmt.Sprintf("Dal CLI for member dal (%s)", dalName),
	}

	root.AddCommand(statusCmd(dalName), psCmd(), reportCmd(dalName), runCmd(dalName))

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
			fmt.Fprintln(w, "NAME\tPLAYER\tROLE\tSTATUS")
			for _, c := range containers {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.DalName, c.Player, c.Role, c.Status)
			}
			w.Flush()
			return nil
		},
	}
}

func reportCmd(dalName string) *cobra.Command {
	return &cobra.Command{
		Use:   "report <message>",
		Short: "Report to leader (via Mattermost)",
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
