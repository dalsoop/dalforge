package daemon

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// WebhookEvent represents a task lifecycle event sent to external systems.
type WebhookEvent struct {
	Event      string   `json:"event"`       // "task_complete", "task_failed", "escalation"
	Dal        string   `json:"dal"`
	Task       string   `json:"task"`
	OutputSize int      `json:"output_bytes"`
	GitChanges []string `json:"git_changes,omitempty"`
	PRUrl      string   `json:"pr_url,omitempty"`
	Error      string   `json:"error,omitempty"`
	Timestamp  string   `json:"timestamp"`
}

// dispatchWebhook sends a webhook event to DALCENTER_WEBHOOK_URL if configured.
func dispatchWebhook(event WebhookEvent) {
	url := os.Getenv("DALCENTER_WEBHOOK_URL")
	if url == "" {
		return
	}

	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[webhook] marshal error: %v", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		log.Printf("[webhook] dispatch failed: %v", err)
		return
	}
	resp.Body.Close()
	log.Printf("[webhook] dispatched %s for %s (HTTP %d)", event.Event, event.Dal, resp.StatusCode)
}

// DispatchTaskComplete sends a task_complete webhook.
func DispatchTaskComplete(dal, task string, outputSize int, gitChanges []string, prURL string) {
	dispatchWebhook(WebhookEvent{
		Event:      "task_complete",
		Dal:        dal,
		Task:       task,
		OutputSize: outputSize,
		GitChanges: gitChanges,
		PRUrl:      prURL,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
}

// DispatchTaskFailed sends a task_failed webhook.
func DispatchTaskFailed(dal, task, errMsg string, outputSize int) {
	dispatchWebhook(WebhookEvent{
		Event:      "task_failed",
		Dal:        dal,
		Task:       task,
		OutputSize: outputSize,
		Error:      errMsg,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
}
