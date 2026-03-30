package main

import (
	"fmt"
	"os"
	"strings"



	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/spf13/cobra"
)

func wakeCmd() *cobra.Command {
	var issue string
	cmd := &cobra.Command{
		Use:   "wake <dal>",
		Short: "Wake a team member (leader only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if role := os.Getenv("DAL_ROLE"); role != "leader" {
				return fmt.Errorf("only leader can wake members (current role: %s)", role)
			}
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			name := args[0]
			var result map[string]any
			if issue != "" {
				result, err = client.WakeWithIssue(name, issue)
			} else {
				result, err = client.Wake(name)
			}
			if err != nil {
				return fmt.Errorf("wake %s: %w", name, err)
			}
			cid, _ := result["container_id"].(string)
			if len(cid) > 12 {
				cid = cid[:12]
			}
			fmt.Printf("[wake] %s started (container=%s)\n", name, cid)
			if w, ok := result["warnings"].(string); ok && w != "" {
				fmt.Printf("[wake] warning: %s\n", w)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&issue, "issue", "", "GitHub issue number (creates issue-{N}/{dal} branch)")
	return cmd
}

func sleepCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sleep <dal>",
		Short: "Sleep a team member (leader only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if role := os.Getenv("DAL_ROLE"); role != "leader" {
				return fmt.Errorf("only leader can sleep members (current role: %s)", role)
			}
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			name := args[0]
			_, err = client.Sleep(name)
			if err != nil {
				return fmt.Errorf("sleep %s: %w", name, err)
			}
			fmt.Printf("[sleep] %s stopped\n", name)
			return nil
		},
	}
}

func assignCmd(dalName string) *cobra.Command {
	var issue string
	cmd := &cobra.Command{
		Use:   "assign <dal> <task>",
		Short: "Assign a task to a team member (leader only)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if role := os.Getenv("DAL_ROLE"); role != "leader" {
				return fmt.Errorf("only leader can assign tasks (current role: %s)", role)
			}
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			target := args[0]
			task := strings.Join(args[1:], " ")

			// Wake if not already awake
			ps, _ := client.Ps()
			awake := false
			for _, c := range ps {
				if c.DalName == target {
					awake = true
					break
				}
			}
			if !awake {
				fmt.Printf("[assign] %s is not awake, waking...\n", target)
				var result map[string]any
				if issue != "" {
					result, err = client.WakeWithIssue(target, issue)
				} else {
					result, err = client.Wake(target)
				}
				if err != nil {
					return fmt.Errorf("wake %s: %w", target, err)
				}
				cid, _ := result["container_id"].(string)
				if len(cid) > 12 {
					cid = cid[:12]
				}
				fmt.Printf("[assign] %s woke up (container=%s)\n", target, cid)
			}

			// Send task
			result, err := client.Task(target, task, true)
			if err != nil {
				return fmt.Errorf("task %s: %w", target, err)
			}
			fmt.Printf("[assign] task %s dispatched to %s\n", result.ID, target)
			return nil
		},
	}
	cmd.Flags().StringVar(&issue, "issue", "", "GitHub issue number")
	return cmd
}
