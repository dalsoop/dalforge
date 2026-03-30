package bridge

import "time"

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
}
