package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "dalcenter",
		Short: "Dal lifecycle manager — wake, sleep, sync, validate",
	}

	root.AddCommand(
		newServeCmd(),
		newInitCmd(),
		newValidateCmd(),
		newWakeCmd(),
		newSleepCmd(),
		newRestartCmd(),
		newSyncCmd(),
		newPsCmd(),
		newStatusCmd(),
		newLogsCmd(),
		newAttachCmd(),
		newTaskCmd(),
		newTaskStatusCmd(),
		newTaskListCmd(),
		newClaimsCmd(),
		newImageCmd(),
		newRegisterCmd(),
		newTellCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
