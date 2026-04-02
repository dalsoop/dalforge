package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// NotifyPayload is the JSON body sent to DALCENTER_NOTIFY_URL.
type NotifyPayload struct {
	Event      string `json:"event"`               // "task_done", "task_failed"
	Dal        string `json:"dal"`
	InstanceID string `json:"instance_id,omitempty"`
	TaskID     string `json:"task_id"`
	Task       string `json:"task"`
	Status     string `json:"status"`
	PRUrl      string `json:"pr_url,omitempty"`
	Error      string `json:"error,omitempty"`
	Output     string `json:"output,omitempty"`
	Changes    int    `json:"git_changes"`
	Verified   string `json:"verified,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// notifyTaskComplete sends a notification when a task finishes.
// It tries three channels in order:
//  1. DALCENTER_NOTIFY_URL — HTTP POST with JSON payload
//  2. notify-dalroot CLI — if CallbackPane is set
//  3. Neither — log only
func notifyTaskComplete(dalName, instanceID string, tr *taskResult, repo string) {
	payload := buildNotifyPayload(dalName, instanceID, tr)

	// 1. HTTP notification via DALCENTER_NOTIFY_URL
	if url := os.Getenv("DALCENTER_NOTIFY_URL"); url != "" {
		go sendNotifyHTTP(url, payload)
	}

	// 2. CLI notification via notify-dalroot (backward compat)
	if tr.CallbackPane != "" {
		go sendNotifyCLI(dalName, tr, repo)
	}
}

// buildNotifyPayload constructs the notification payload from a task result.
func buildNotifyPayload(dalName, instanceID string, tr *taskResult) NotifyPayload {
	event := "task_done"
	if tr.Status == "failed" || tr.Status == "blocked" {
		event = "task_failed"
	}

	p := NotifyPayload{
		Event:      event,
		Dal:        dalName,
		InstanceID: instanceID,
		TaskID:     tr.ID,
		Task:      truncateStr(tr.Task, 200),
		Status:    tr.Status,
		Changes:   tr.GitChanges,
		Verified:  tr.Verified,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	if tr.Status == "failed" || tr.Status == "blocked" {
		p.Error = truncateStr(tr.Error, 500)
	}

	// Extract PR URL from output if present
	if prURL := extractPRUrl(tr.Output); prURL != "" {
		p.PRUrl = prURL
	}

	return p
}

// sendNotifyHTTP posts the payload to the given URL.
func sendNotifyHTTP(url string, payload NotifyPayload) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[notify] marshal error: %v", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		log.Printf("[notify] HTTP POST to %s failed: %v", url, err)
		return
	}
	resp.Body.Close()
	log.Printf("[notify] HTTP POST %s → %d (%s %s)", url, resp.StatusCode, payload.Event, payload.Dal)
}

// sendNotifyCLI calls the notify-dalroot CLI tool for pane-based notification.
func sendNotifyCLI(dalName string, tr *taskResult, repo string) {
	msg := fmt.Sprintf("[%s] task %s: %s", dalName, tr.Status, truncateStr(tr.Task, 80))
	if prURL := extractPRUrl(tr.Output); prURL != "" {
		msg += " → " + prURL
	}
	if tr.Status == "failed" && tr.Error != "" {
		msg += " | error: " + truncateStr(tr.Error, 100)
	}
	cmd := exec.Command("notify-dalroot", repo, msg, tr.CallbackPane)
	if err := cmd.Run(); err != nil {
		log.Printf("[notify] dalroot CLI failed: %v", err)
	}
}

// extractPRUrl scans output for a GitHub PR URL.
func extractPRUrl(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "github.com/") && strings.Contains(line, "/pull/") {
			// Find the URL within the line
			for _, word := range strings.Fields(line) {
				if strings.Contains(word, "github.com/") && strings.Contains(word, "/pull/") {
					return strings.TrimRight(word, ".,;:!?\"'`)")
				}
			}
		}
	}
	return ""
}
