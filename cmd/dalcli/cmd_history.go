package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// appendHistoryBuffer writes a session record to history-buffer.
// Scribe dal will later merge this into .dal/{name}/history.md.
func appendHistoryBuffer(dalName, task, result, status string) {
	bufferDir := os.Getenv("DALCLI_HISTORY_BUFFER")
	if bufferDir == "" {
		bufferDir = "/workspace/history-buffer"
	}
	if _, err := os.Stat(bufferDir); err != nil {
		return // history-buffer not mounted, skip silently
	}

	filename := fmt.Sprintf("%s.md", dalName)
	path := filepath.Join(bufferDir, filename)

	entry := fmt.Sprintf("\n### %s: %s\n**상태:** %s\n**결과:** %s\n**다음:** \n**주의:** \n",
		time.Now().Format("2006-01-02"), truncate(task, 80), status, truncate(result, 200))

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return // append failure must not block report
	}
	defer f.Close()
	f.WriteString(entry)
}
