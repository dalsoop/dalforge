package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func wisdomCmd(dalName string) *cobra.Command {
	var context string
	var isAntiPattern bool
	cmd := &cobra.Command{
		Use:   "wisdom <pattern>",
		Short: "Propose a wisdom pattern (or anti-pattern) for team knowledge",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dalName == "" {
				return fmt.Errorf("DAL_NAME not set")
			}
			return proposeWisdom(dalName, args[0], context, isAntiPattern)
		},
	}
	cmd.Flags().StringVar(&context, "context", "", "Why this pattern matters")
	cmd.Flags().BoolVar(&isAntiPattern, "avoid", false, "Mark as anti-pattern (something to avoid)")
	return cmd
}

// proposeWisdom writes a wisdom proposal to wisdom-inbox.
func proposeWisdom(dalName, pattern, context string, isAntiPattern bool) error {
	inboxDir := "/workspace/wisdom-inbox"
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return fmt.Errorf("wisdom inbox not available: %w", err)
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
	}, pattern)
	if len(slug) > 40 {
		slug = slug[:40]
	}

	filename := fmt.Sprintf("%s-%s-%s.md", dalName, time.Now().Format("20060102"), slug)
	path := filepath.Join(inboxDir, filename)

	var content string
	if isAntiPattern {
		content = fmt.Sprintf("**Avoid:** %s\n**Why:** %s\n**Ref:** \n", pattern, context)
	} else {
		content = fmt.Sprintf("**Pattern:** %s\n**Context:** %s\n", pattern, context)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write wisdom proposal: %w", err)
	}
	fmt.Printf("[wisdom] submitted: %s\n", filename)
	return nil
}
