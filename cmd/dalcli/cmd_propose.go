package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func proposeCmd(dalName string) *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "propose <title>",
		Short: "Propose a decision to the leader",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dalName == "" {
				return fmt.Errorf("DAL_NAME not set")
			}
			return proposeDecision(dalName, args[0], body)
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "Proposal body/description")
	return cmd
}

func proposeDecision(dalName, title, body string) error {
	inboxDir := "/workspace/decisions/inbox"
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return fmt.Errorf("inbox not available: %w", err)
	}

	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		return -1
	}, title)
	if len(slug) > 40 {
		slug = slug[:40]
	}

	filename := fmt.Sprintf("%s-%s-%s.md", dalName, time.Now().Format("20060102"), slug)
	path := filepath.Join(inboxDir, filename)

	content := fmt.Sprintf("### %s: %s\n**By:** %s\n**What:** %s\n**Why:** \n",
		time.Now().Format("2006-01-02"), title, dalName, body)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write proposal: %w", err)
	}
	fmt.Printf("[propose] submitted: %s\n", filename)
	return nil
}
