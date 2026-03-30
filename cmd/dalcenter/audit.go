package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dalsoop/dalcenter/internal/paths"
)

// appendAuditLog writes an audit entry to the state directory.
func appendAuditLog(action, target, reason string) {
	base := paths.StateBaseDir()
	// Use current working directory's .dal to derive repo name
	wd, _ := os.Getwd()
	repoName := filepath.Base(wd)

	logDir := filepath.Join(base, repoName)
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "audit.log")

	entry := fmt.Sprintf("%s\t%s\t%s\t%s\n",
		time.Now().Format(time.RFC3339), action, target, reason)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return // audit failure must not block operation
	}
	defer f.Close()
	f.WriteString(entry)
}
