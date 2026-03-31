package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/spf13/cobra"
)

func main() {
	dalName := os.Getenv("DAL_NAME")

	root := &cobra.Command{
		Use:   "dalcli-leader",
		Short: fmt.Sprintf("Dal CLI for leader dal (%s)", dalName),
	}

	root.AddCommand(wakeCmd(), sleepCmd(), psCmd(), statusCmd(), logsCmd(), syncCmd(), assignCmd(dalName), postCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func wakeCmd() *cobra.Command {
	var issueID string
	cmd := &cobra.Command{
		Use:   "wake <dal>",
		Short: "Wake a team member",
		Long:  "Wake a team member. Use --issue to create an issue branch.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			result, err := client.WakeWithIssue(args[0], issueID)
			if err != nil {
				return err
			}
			cid, ok := result["container_id"].(string)
			if !ok {
				return fmt.Errorf("unexpected response: missing container_id")
			}
			fmt.Printf("wake: %s → %s\n", args[0], cid)
			if issueID != "" {
				fmt.Printf("issue: %s\n", issueID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&issueID, "issue", "", "Issue ID for branch creation (e.g. 489)")
	return cmd
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
			fmt.Fprintln(w, "NAME\tPLAYER\tROLE\tSTATUS\tIDLE\tLAST SEEN\tCONTAINER")
			for _, c := range containers {
				cid := c.ContainerID
				if len(cid) > 12 {
					cid = cid[:12]
				}
				idle := c.IdleFor
				if idle == "" {
					idle = "-"
				}
				lastSeen := "-"
				if !c.LastSeenAt.IsZero() {
					lastSeen = c.LastSeenAt.Local().Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.DalName, c.Player, c.Role, c.Status, idle, lastSeen, cid)
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
					fmt.Printf("name:        %s\n", c.DalName)
					fmt.Printf("uuid:        %s\n", c.UUID)
					fmt.Printf("instance_id: %s\n", c.InstanceID)
					fmt.Printf("player:      %s\n", c.Player)
					fmt.Printf("role:        %s\n", c.Role)
					fmt.Printf("status:      %s\n", c.Status)
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

func postCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "post <message>",
		Short: "Post message to team channel (via matterbridge)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bridgeURL := os.Getenv("DALCENTER_BRIDGE_URL")
			if bridgeURL == "" {
				bridgeURL = "http://localhost:" + os.Getenv("DAL_BRIDGE_PORT")
			}
			dalName := os.Getenv("DAL_NAME")
			gateway := os.Getenv("DAL_GATEWAY")
			if gateway == "" {
				gateway = "dal-team"
			}
			body := fmt.Sprintf(`{"text":%q,"username":%q,"gateway":%q}`, args[0], dalName, gateway)
			resp, err := http.Post(bridgeURL+"/api/message", "application/json", strings.NewReader(body))
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				return fmt.Errorf("bridge post failed: HTTP %d", resp.StatusCode)
			}
			fmt.Println("posted via matterbridge")
			return nil
		},
	}
	return cmd
}

func assignCmd(dalName string) *cobra.Command {
	return &cobra.Command{
		Use:   "assign <dal> <task>",
		Short: "Assign task to team member (via bridge)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			// Find target dal's instance ID from ps to construct mention
			targetName := args[0]
			var targetMention string
			if containers, err := client.Ps(); err == nil {
				for _, c := range containers {
					if c.DalName == targetName {
						// Use instance ID for unique mention (#531)
						id := c.InstanceID
						if id == "" {
							id = c.UUID
						}
						clean := strings.ReplaceAll(id, "-", "")
						short := clean
						if len(clean) > 6 {
							short = clean[:6]
						}
						targetMention = fmt.Sprintf("@dal-%s-%s", targetName, short)
						break
					}
				}
			}
			if targetMention == "" {
				targetMention = fmt.Sprintf("@dal-%s", targetName)
			}
			msg := fmt.Sprintf("%s 작업 지시: %s", targetMention, args[1])
			result, err := client.Message(dalName, msg)
			if err != nil {
				return err
			}
			fmt.Printf("assigned: %s → %s (thread: %s)\n", args[0], args[1], result.PostID)
			return nil
		},
	}
}
