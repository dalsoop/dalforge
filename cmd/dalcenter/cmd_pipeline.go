package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dalsoop/dalcenter/internal/daemon"
	"github.com/spf13/cobra"
)

// --- restart ---

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <dal>",
		Short: "Restart a dal (sleep + remove + fresh wake with new bot token)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			name := args[0]

			// Server-side restart: sleep + rm + wake (gets fresh bot token)
			result, err := client.Restart(name)
			if err != nil {
				return fmt.Errorf("restart failed: %w", err)
			}
			cid := result["container_id"]
			if len(cid) > 12 {
				cid = cid[:12]
			}
			fmt.Printf("[restart] %s started (container=%s)\n", name, cid)
			if w, ok := result["warnings"]; ok && w != "" {
				fmt.Printf("[restart] ⚠️  %s\n", w)
			}
			return nil
		},
	}
}

// --- task ---

func newTaskCmd() *cobra.Command {
	var async bool
	var poll bool
	var reason string
	cmd := &cobra.Command{
		Use:   "task <dal> <prompt>",
		Short: "Execute a task directly in a dal container",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if reason == "" {
				fmt.Fprintf(os.Stderr, "⚠️  member 직접 작업 투입: --reason 플래그 권장 (leader 경유 권장)\n")
			}
			if reason != "" {
				appendAuditLog("task", args[0], reason)
			}
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			dal := args[0]
			task := strings.Join(args[1:], " ")

			result, err := client.Task(dal, task, async)
			if err != nil {
				return err
			}

			if async {
				fmt.Printf("[task] dispatched: %s (dal=%s)\n", result.ID, dal)
				if !poll {
					return nil
				}
				// Poll until done
				fmt.Print("[task] waiting")
				for {
					time.Sleep(5 * time.Second)
					fmt.Print(".")
					r, err := client.TaskStatus(result.ID)
					if err != nil {
						return err
					}
					if r.Status != "running" {
						fmt.Println()
						printTaskResult(r)
						return nil
					}
				}
			}

			// Synchronous result
			printTaskResult(result)
			return nil
		},
	}
	cmd.Flags().BoolVar(&async, "async", false, "Run task asynchronously")
	cmd.Flags().BoolVar(&poll, "poll", false, "Wait for async task to complete (implies --async)")
	cmd.Flags().StringVar(&reason, "reason", "", "사유 (audit trail)")
	return cmd
}

func printTaskResult(r *daemon.TaskResult) {
	if r.Status == "done" {
		fmt.Println(r.Output)
	} else if r.Status == "failed" {
		fmt.Fprintf(os.Stderr, "[task] failed: %s\n", r.Error)
		if r.Output != "" {
			fmt.Println(r.Output)
		}
		os.Exit(1)
	} else {
		fmt.Printf("[task] status: %s\n", r.Status)
	}
}

// --- task-status ---

func newTaskStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "task-status <task-id>",
		Short: "Check status of a direct task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			r, err := client.TaskStatus(args[0])
			if err != nil {
				return err
			}
			printTaskResult(r)
			return nil
		},
	}
}

// --- task-list ---

func newTaskListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "task-list",
		Short: "List all tracked tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.NewClient()
			if err != nil {
				return err
			}
			tasks, err := client.TaskList()
			if err != nil {
				return err
			}
			if len(tasks) == 0 {
				fmt.Println("No tasks")
				return nil
			}
			fmt.Printf("%-12s %-10s %-10s %s\n", "ID", "DAL", "STATUS", "TASK")
			fmt.Println(strings.Repeat("-", 70))
			for _, t := range tasks {
				task := t.Task
				if len(task) > 40 {
					task = task[:40] + "..."
				}
				fmt.Printf("%-12s %-10s %-10s %s\n", t.ID, t.Dal, t.Status, task)
			}
			return nil
		},
	}
}

// --- image ---

func newImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage dal Docker images",
	}
	cmd.AddCommand(newImageListCmd(), newImageBuildCmd())
	return cmd
}

func newImageListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available dalcenter Docker images",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := exec.Command("docker", "images", "--filter", "reference=dalcenter/*",
				"--format", "{{.Repository}}:{{.Tag}}\t{{.Size}}\t{{.CreatedSince}}").CombinedOutput()
			if err != nil {
				return fmt.Errorf("docker images: %w", err)
			}
			output := strings.TrimSpace(string(out))
			if output == "" {
				fmt.Println("No dalcenter images found")
				return nil
			}
			fmt.Printf("%-30s %-10s %s\n", "IMAGE", "SIZE", "CREATED")
			fmt.Println(strings.Repeat("-", 60))
			for _, line := range strings.Split(output, "\n") {
				parts := strings.SplitN(line, "\t", 3)
				if len(parts) == 3 {
					fmt.Printf("%-30s %-10s %s\n", parts[0], parts[1], parts[2])
				}
			}
			return nil
		},
	}
}

func findDockerfilesDir() (string, error) {
	// 1. DALCENTER_DOCKERFILES environment variable
	if p := os.Getenv("DALCENTER_DOCKERFILES"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// 2. Next to the dalcenter binary (<binary-dir>/dockerfiles/)
	if exe, err := os.Executable(); err == nil {
		binDir := filepath.Dir(exe)
		candidate := filepath.Join(binDir, "dockerfiles")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// 3. Current working directory (./dockerfiles/)
	if wd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(wd, "dockerfiles")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// 4. Relative to localdal root (repo-root/dockerfiles/)
	root := localdalRoot()
	repoRoot := filepath.Dir(root)
	candidate := filepath.Join(repoRoot, "dockerfiles")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	return "", fmt.Errorf("dockerfiles directory not found; searched: $DALCENTER_DOCKERFILES, <binary-dir>/dockerfiles, ./dockerfiles, %s", candidate)
}

func newImageBuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build <dockerfile-tag>",
		Short: "Build a dalcenter Docker image (e.g., 'rust' builds claude-rust.Dockerfile)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tag := args[0]

			// Find Dockerfile directory using fallback chain
			dockerfilesDir, err := findDockerfilesDir()
			if err != nil {
				return err
			}

			// Map tag to Dockerfile: "rust" → "claude-rust.Dockerfile"
			dockerfile := filepath.Join(dockerfilesDir, fmt.Sprintf("claude-%s.Dockerfile", tag))
			if _, err := os.Stat(dockerfile); err != nil {
				// Try exact name
				dockerfile = filepath.Join(dockerfilesDir, fmt.Sprintf("%s.Dockerfile", tag))
				if _, err := os.Stat(dockerfile); err != nil {
					return fmt.Errorf("no Dockerfile found for tag %q", tag)
				}
			}

			imageName := fmt.Sprintf("dalcenter/claude:%s", tag)
			fmt.Printf("[image] building %s from %s\n", imageName, filepath.Base(dockerfile))

			c := exec.Command("docker", "build", "-f", dockerfile, "-t", imageName, dockerfilesDir)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("build failed: %w", err)
			}
			fmt.Printf("[image] ✓ %s built successfully\n", imageName)
			return nil
		},
	}
}
