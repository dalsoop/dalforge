package talk

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"dalforge-hub/dalcenter/internal/bridge"
)

// Config for the talk daemon.
type Config struct {
	BridgeType  string // "mattermost"
	URL         string // bridge server URL
	BotToken    string // bot auth token
	ChannelID   string // target channel ID
	BotUsername string // bot username for mention detection (e.g. "dal-200")
	Role        string // dal role description
	Cwd         string // working directory (repo root for code access)
	AskMode     bool   // enable ask mode
	ExecMode    bool   // enable exec mode
	MentionOnly bool   // only respond when @mentioned
	MaxTurns    int    // max response turns per session
	Cooldown    time.Duration
	HookPort    int    // hook server port (default 10200)
	ServeURL    string // dalcenter serve URL for registry
	DalName   string // dal name for registration
}

// HookRequest is the JSON body for direct dal-to-dal calls.
type HookRequest struct {
	Event   string `json:"event"`   // "ask" or "exec"
	From    string `json:"from"`    // sender dal name
	Content string `json:"content"` // message content
}

// HookResponse is the JSON response from a hook call.
type HookResponse struct {
	From       string `json:"from"`
	Content    string `json:"content"`
	DurationMs int64  `json:"duration_ms"`
}

// Daemon runs the talk service.
type Daemon struct {
	cfg       Config
	br        bridge.Bridge
	executor  *Executor
	sanitizer *Sanitizer
	seen      map[string]bool
	mu        sync.Mutex
	wg        sync.WaitGroup
	turnCount int
	lastReply time.Time
}

func NewDaemon(cfg Config) (*Daemon, error) {
	var br bridge.Bridge
	switch cfg.BridgeType {
	case "mattermost", "":
		br = bridge.NewMattermostBridge(cfg.URL, cfg.BotToken, cfg.ChannelID, 2*time.Second)
	default:
		return nil, fmt.Errorf("unsupported bridge type: %s", cfg.BridgeType)
	}

	return &Daemon{
		cfg:       cfg,
		br:        br,
		executor:  NewExecutor(cfg.Role, cfg.Cwd),
		sanitizer: NewSanitizer(),
		seen:      make(map[string]bool),
	}, nil
}

// Run starts the daemon and blocks until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.br.Connect(); err != nil {
		return fmt.Errorf("bridge connect: %w", err)
	}
	defer d.br.Close()

	botUserID := ""
	if mm, ok := d.br.(*bridge.MattermostBridge); ok {
		botUserID = mm.BotUserID
	}
	log.Printf("[talk] connected, channel=%s, role=%q, bot=%s", d.cfg.ChannelID, d.cfg.Role, botUserID)

	// Register with dalcenter serve
	d.register()

	// Start hook server
	hookPort := d.cfg.HookPort
	if hookPort == 0 {
		hookPort = 10200
	}
	go d.serveHook(ctx, hookPort)

	// Main loop
	for {
		select {
		case msg := <-d.br.Listen():
			if msg.From == botUserID {
				continue
			}
			if d.isDuplicate(msg.ID) {
				continue
			}
			// Mention filter: only respond when @mentioned
			if d.cfg.MentionOnly && !d.isMentioned(msg.Content) {
				continue
			}
			if d.cfg.MaxTurns > 0 && d.turnCount >= d.cfg.MaxTurns {
				log.Printf("[talk] max_turns reached (%d), ignoring", d.cfg.MaxTurns)
				continue
			}
			if d.cfg.Cooldown > 0 && time.Since(d.lastReply) < d.cfg.Cooldown {
				continue
			}

			// Strip mention tag from content before processing
			cleaned := d.stripMention(msg.Content)
			msg.Content = cleaned

			d.wg.Add(1)
			go func(m bridge.Message) {
				defer d.wg.Done()
				d.handleMessage(ctx, m)
				d.turnCount++
				d.lastReply = time.Now()
			}(msg)

		case err := <-d.br.Errors():
			log.Printf("[talk] bridge error: %v", err)

		case <-ctx.Done():
			d.deregister()
			d.wg.Wait()
			return nil
		}
	}
}

func (d *Daemon) handleMessage(ctx context.Context, msg bridge.Message) {
	log.Printf("[talk] received from %s: %s", msg.From, truncate(msg.Content, 80))

	mode := ModeAsk
	if d.cfg.ExecMode {
		mode = ModeExec
	}

	response, err := d.executor.Run(ctx, mode, msg.Content)
	if err != nil {
		log.Printf("[talk] claude error: %v", err)
		return
	}
	log.Printf("[talk] responded: %s", truncate(response, 120))

	response = d.sanitizer.Clean(response)

	// Thread: if the message is already in a thread, continue there.
	// Otherwise, start a thread on the original message.
	rootID := msg.RootID
	if rootID == "" {
		rootID = msg.ID
	}

	if err := d.sendWithRetry(bridge.Message{
		Channel: msg.Channel,
		Content: response,
		RootID:  rootID,
	}); err != nil {
		log.Printf("[talk] send error: %v", err)
	}
}

func (d *Daemon) sendWithRetry(msg bridge.Message) error {
	for attempt := 0; attempt < 3; attempt++ {
		if err := d.br.Send(msg); err != nil {
			log.Printf("[talk] send attempt %d failed: %v", attempt+1, err)
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil
	}
	return fmt.Errorf("send failed after 3 attempts")
}

func (d *Daemon) serveHook(ctx context.Context, port int) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hook", func(w http.ResponseWriter, r *http.Request) {
		var req HookRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}

		mode := ModeAsk
		if req.Event == "exec" && d.cfg.ExecMode {
			mode = ModeExec
		}

		start := time.Now()
		response, err := d.executor.Run(ctx, mode, req.Content)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		response = d.sanitizer.Clean(response)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HookResponse{
			From:       d.cfg.DalName,
			Content:    response,
			DurationMs: time.Since(start).Milliseconds(),
		})
	})

	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux}
	log.Printf("[talk] hook server listening on :%d", port)

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Printf("[talk] hook server error: %v", err)
	}
}

func (d *Daemon) register() {
	if d.cfg.ServeURL == "" {
		return
	}
	// TODO: POST to dalcenter serve /api/dals/register
	log.Printf("[talk] registered with %s as %s", d.cfg.ServeURL, d.cfg.DalName)
}

func (d *Daemon) deregister() {
	if d.cfg.ServeURL == "" {
		return
	}
	// TODO: DELETE from dalcenter serve /api/dals/{name}
	log.Printf("[talk] deregistered from %s", d.cfg.ServeURL)
}

// isMentioned checks if the message contains @botUsername or @dalName.
func (d *Daemon) isMentioned(content string) bool {
	lower := strings.ToLower(content)
	if d.cfg.BotUsername != "" && strings.Contains(lower, "@"+strings.ToLower(d.cfg.BotUsername)) {
		return true
	}
	if d.cfg.DalName != "" && strings.Contains(lower, "@"+strings.ToLower(d.cfg.DalName)) {
		return true
	}
	return false
}

// stripMention removes @botUsername and @dalName from content.
func (d *Daemon) stripMention(content string) string {
	result := content
	for _, name := range []string{d.cfg.BotUsername, d.cfg.DalName} {
		if name == "" {
			continue
		}
		result = strings.ReplaceAll(result, "@"+name, "")
		result = strings.ReplaceAll(result, "@"+strings.ToLower(name), "")
	}
	return strings.TrimSpace(result)
}

func (d *Daemon) isDuplicate(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen[id] {
		return true
	}
	d.seen[id] = true
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
