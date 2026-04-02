package main

import (
	"fmt"
	"strings"

	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/spf13/cobra"
)

func newChannelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channel",
		Short: "Manage MM channels and pane↔channel mappings",
	}
	cmd.AddCommand(
		newChannelCreateCmd(),
		newChannelDeleteCmd(),
		newChannelMapCmd(),
		newChannelUnmapCmd(),
		newChannelListCmd(),
		newChannelSyncCmd(),
	)
	return cmd
}

func newChannelCreateCmd() *cobra.Command {
	var purpose string
	cmd := &cobra.Command{
		Use:   "create <channel-name>",
		Short: "Create an MM channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			entry, err := client.ChannelCreate(args[0], purpose)
			if err != nil {
				return err
			}
			fmt.Printf("[channel] created: %s (id=%s)\n", entry.ChannelName, entry.ChannelID)
			return nil
		},
	}
	cmd.Flags().StringVar(&purpose, "purpose", "", "Channel purpose/description")
	return cmd
}

func newChannelDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <channel-name>",
		Short: "Delete (archive) an MM channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			if err := client.ChannelDelete(args[0]); err != nil {
				return err
			}
			fmt.Printf("[channel] deleted: %s\n", args[0])
			return nil
		},
	}
}

func newChannelMapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "map <pane> <channel-name>",
		Short: "Bind a pane to an existing MM channel",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			entry, err := client.ChannelMap(args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Printf("[channel] mapped: %s → %s\n", entry.Pane, entry.ChannelName)
			return nil
		},
	}
}

func newChannelUnmapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unmap <pane>",
		Short: "Remove a pane→channel binding",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			if err := client.ChannelUnmap(args[0]); err != nil {
				return err
			}
			fmt.Printf("[channel] unmapped: %s\n", args[0])
			return nil
		},
	}
}

func newChannelListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List pane→channel mappings",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			entries, err := client.ChannelList()
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("No channel mappings")
				return nil
			}
			fmt.Printf("%-20s %-30s %s\n", "PANE", "CHANNEL", "ID")
			fmt.Println(strings.Repeat("-", 80))
			for _, e := range entries {
				fmt.Printf("%-20s %-30s %s\n", e.Pane, e.ChannelName, e.ChannelID)
			}
			return nil
		},
	}
}

func newChannelSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync <pane-id> [pane-id...]",
		Short: "Auto-map dalroot panes (dalroot-{s}-{w}-{p} pattern)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			entries, err := client.ChannelSync(args)
			if err != nil {
				return err
			}
			for _, e := range entries {
				fmt.Printf("[channel] synced: %s → %s\n", e.Pane, e.ChannelName)
			}
			if len(entries) == 0 {
				fmt.Println("[channel] no panes synced")
			}
			return nil
		},
	}
}
