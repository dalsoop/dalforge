package bridge

import (
	"errors"
	"time"
)

// ErrAuthFailed is returned when the bot token is invalid or expired.
// This is non-retryable — the caller should stop, not retry.
var ErrAuthFailed = errors.New("auth failed (token invalid or expired)")

// DeliveryStatus represents the state of a message delivery attempt.
type DeliveryStatus string

const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryFailed    DeliveryStatus = "failed"
)

// DeliveryRecord tracks the delivery state of a single message.
type DeliveryRecord struct {
	ID        string
	Message   Message
	Status    DeliveryStatus
	Attempts  int
	LastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Message represents a single message exchanged between agents.
type Message struct {
	ID        string
	From      string // sender agent/bot ID
	Channel   string // channel or topic name
	Content   string
	ReplyTo   string // reply target message ID (optional)
	RootID    string // thread root ID (for threaded replies)
	Timestamp time.Time
}

// Bridge is the interface for communication backends.
type Bridge interface {
	Connect() error
	Listen() <-chan Message
	Errors() <-chan error
	Send(msg Message) error
	UpdateToken(token string)
	Close() error

	// BotID returns the bot's own user/identity ID (used to filter self-messages).
	BotID() string
	// GetUsername resolves a user ID to a display username.
	GetUsername(userID string) string
	// GetUserIDByUsername resolves a username to a user ID.
	GetUserIDByUsername(username string) (string, error)
	// SetNoDM disables DM channel polling.
	SetNoDM(noDM bool)

	// SendWithACK sends a message with delivery tracking.
	// Returns a delivery ID for status queries.
	// Retries up to 3 times on failure before returning error.
	SendWithACK(msg Message) (string, error)
	// GetDelivery returns the delivery record for the given ID, or nil if not found.
	GetDelivery(deliveryID string) *DeliveryRecord
}
