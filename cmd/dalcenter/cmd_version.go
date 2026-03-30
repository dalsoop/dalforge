package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/dalcenter/
var version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print dalcenter version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	}
}
