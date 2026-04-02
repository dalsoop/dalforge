package daemon

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

const (
	// messageWatchdogInterval is how often the watchdog checks for timed-out messages.
	messageWatchdogInterval = 1 * time.Minute
)

// handleACK receives an acknowledgment from a dal that a message was received.
// Called by the receiving dal: POST /api/ack/{id}
func (d *Daemon) handleACK(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "message id required", http.StatusBadRequest)
		return
	}

	if !d.messages.MarkAcked(id) {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	log.Printf("[ack] message %s acknowledged", id)
	respondJSON(w, http.StatusOK, map[string]string{
		"status":     "acked",
		"message_id": id,
	})
}

// handleMessageList returns all buffered messages.
// GET /api/messages?status=pending
func (d *Daemon) handleMessageList(w http.ResponseWriter, r *http.Request) {
	msgs := d.messages.List()

	// Filter by status if requested
	if s := r.URL.Query().Get("status"); s != "" {
		var filtered []*BufferedMessage
		status := MessageStatus(s)
		for _, m := range msgs {
			if m.Status == status {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"messages": msgs,
		"count":    len(msgs),
	})
}

// handleMessageStatus returns a single message by ID.
// GET /api/messages/{id}
func (d *Daemon) handleMessageStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m := d.messages.Get(id)
	if m == nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	respondJSON(w, http.StatusOK, m)
}

// startMessageWatchdog periodically checks for messages that were sent but
// never acknowledged, and schedules retries with auto-wake.
func (d *Daemon) startMessageWatchdog(ctx context.Context) {
	ticker := time.NewTicker(messageWatchdogInterval)
	defer ticker.Stop()

	log.Printf("[watchdog] message watchdog started (interval=%s, ack_timeout=%s)", messageWatchdogInterval, messageACKTimeout)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[watchdog] stopped")
			return
		case <-ticker.C:
			d.processTimedOutMessages()
			d.replayPendingMessages()
		}
	}
}

// processTimedOutMessages finds sent-but-unacked messages and queues them for retry.
func (d *Daemon) processTimedOutMessages() {
	timedOut := d.messages.TimedOut()
	for _, m := range timedOut {
		retries := d.messages.IncrRetry(m.ID)
		if retries > maxRetries {
			d.messages.MarkFailed(m.ID, "max retries exceeded")
			log.Printf("[watchdog] message %s failed — max retries exceeded (to=%s)", m.ID, m.To)
			continue
		}
		log.Printf("[watchdog] message %s timed out — queued for retry %d/%d (to=%s)", m.ID, retries, maxRetries, m.To)
	}
}

// replayPendingMessages attempts to deliver pending messages.
// This handles both initial delivery on daemon restart and retries after timeout.
func (d *Daemon) replayPendingMessages() {
	pending := d.messages.Pending()
	for _, m := range pending {
		log.Printf("[watchdog] replaying message %s to %s (retry=%d)", m.ID, m.To, m.Retries)

		if err := d.deliverMessage(m); err != nil {
			log.Printf("[watchdog] replay %s failed: %v", m.ID, err)
			if m.Retries >= maxRetries {
				d.messages.MarkFailed(m.ID, err.Error())
			}
		}
	}
}

// deliverMessage sends a buffered message to its target.
func (d *Daemon) deliverMessage(m *BufferedMessage) error {
	// Post to the local channel (bridge or MM)
	dalName := m.From
	if dalName == "" {
		dalName = "dalcenter"
	}

	// Tag message with ID for ACK tracking
	taggedMsg := "[" + m.ID + "] " + m.Message

	if d.bridgeURL != "" {
		if err := d.mmPost(taggedMsg); err != nil {
			if err2 := d.bridgePost(taggedMsg, dalName); err2 != nil {
				return err2
			}
		}
	}

	d.messages.MarkSent(m.ID)
	return nil
}

// respondJSON is a helper to write JSON responses with a given status code.
func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
