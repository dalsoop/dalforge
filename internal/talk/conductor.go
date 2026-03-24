package talk

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/dalsoop/dalcenter/internal/bridge"
)

// ConductorConfig for the central orchestrator bot.
type ConductorConfig struct {
	URL        string
	BotToken   string
	ChannelID  string
	BotUsername string
	Dals     []DalInfo
}

// DalInfo describes a registered dal for the conductor.
type DalInfo struct {
	Username string
	Role     string
}

// threadState tracks an active thread.
type threadState struct {
	rootID  string
	topic   string
	done    bool
	turns   int
}

// Conductor is the central orchestrator that routes messages to dals.
type Conductor struct {
	cfg       ConductorConfig
	br        bridge.Bridge
	executor  *Executor
	sanitizer *Sanitizer
	seen       map[string]bool
	threads    map[string]*threadState // rootID → state
	dalBotIDs  map[string]bool         // known dal bot user IDs
	mu         sync.Mutex
	botUserID  string
}

func NewConductor(cfg ConductorConfig) (*Conductor, error) {
	br := bridge.NewMattermostBridge(cfg.URL, cfg.BotToken, cfg.ChannelID, 2*time.Second)
	return &Conductor{
		cfg:       cfg,
		br:        br,
		executor:  NewExecutor("dal 오케스트레이터", ""),
		sanitizer: NewSanitizer(),
		seen:      make(map[string]bool),
		threads:   make(map[string]*threadState),
		dalBotIDs: make(map[string]bool),
	}, nil
}

func (c *Conductor) Run(ctx context.Context) error {
	if err := c.br.Connect(); err != nil {
		return fmt.Errorf("bridge connect: %w", err)
	}
	defer c.br.Close()

	if mm, ok := c.br.(*bridge.MattermostBridge); ok {
		c.botUserID = mm.BotUserID
		for _, d := range c.cfg.Dals {
			uid, err := mm.GetUserIDByUsername(d.Username)
			if err != nil {
				log.Printf("[conductor] warning: could not resolve dal bot %q: %v", d.Username, err)
				continue
			}
			c.dalBotIDs[uid] = true
			log.Printf("[conductor] registered dal bot: @%s → %s", d.Username, uid)
		}
	}

	dalList := c.buildDalList()
	log.Printf("[conductor] started, channel=%s, %d dals", c.cfg.ChannelID, len(c.cfg.Dals))

	for {
		select {
		case msg := <-c.br.Listen():
			if msg.From == c.botUserID {
				continue
			}
			if c.isDalBot(msg.From) {
				continue
			}
			if c.isDuplicate(msg.ID) {
				continue
			}

			if msg.RootID == "" {
				// New root message → start a thread
				go c.startThread(ctx, msg, dalList)
			} else {
				// Reply in existing thread → re-route or close
				go c.handleThreadReply(ctx, msg, dalList)
			}

		case err := <-c.br.Errors():
			if errors.Is(err, bridge.ErrAuthFailed) {
				log.Printf("[conductor] fatal: %v — shutting down", err)
				return err
			}
			log.Printf("[conductor] bridge error: %v", err)

		case <-ctx.Done():
			return nil
		}
	}
}

func (c *Conductor) startThread(ctx context.Context, msg bridge.Message, dalList string) {
	log.Printf("[conductor] new topic: %s", truncate(msg.Content, 80))

	c.mu.Lock()
	c.threads[msg.ID] = &threadState{
		rootID: msg.ID,
		topic:  msg.Content,
	}
	c.mu.Unlock()

	response := c.askRoute(ctx, msg.Content, "", dalList)
	if response == "" {
		return
	}

	c.br.Send(bridge.Message{
		Channel: msg.Channel,
		Content: response,
		RootID:  msg.ID,
	})
}

func (c *Conductor) handleThreadReply(ctx context.Context, msg bridge.Message, dalList string) {
	c.mu.Lock()
	ts, exists := c.threads[msg.RootID]
	c.mu.Unlock()

	if !exists {
		// Thread we didn't start — ignore
		return
	}
	if ts.done {
		return
	}

	// Check if user wants to close
	lower := strings.ToLower(strings.TrimSpace(msg.Content))
	if lower == "끝" || lower == "완료" || lower == "done" || lower == "close" {
		c.closeThread(msg)
		return
	}

	ts.turns++
	log.Printf("[conductor] thread %s turn %d: %s", ts.rootID[:8], ts.turns, truncate(msg.Content, 80))

	response := c.askRoute(ctx, msg.Content, ts.topic, dalList)
	if response == "" {
		return
	}

	c.br.Send(bridge.Message{
		Channel: msg.Channel,
		Content: response,
		RootID:  msg.RootID,
	})
}

func (c *Conductor) closeThread(msg bridge.Message) {
	c.mu.Lock()
	if ts, ok := c.threads[msg.RootID]; ok {
		ts.done = true
	}
	c.mu.Unlock()

	log.Printf("[conductor] thread %s closed", msg.RootID[:8])

	c.br.Send(bridge.Message{
		Channel: msg.Channel,
		Content: "✅ done",
		RootID:  msg.RootID,
	})
}

func (c *Conductor) askRoute(ctx context.Context, message, threadTopic, dalList string) string {
	contextLine := ""
	if threadTopic != "" {
		contextLine = fmt.Sprintf("\n진행 중인 주제: %s\n", threadTopic)
	}

	prompt := fmt.Sprintf(`너는 dal 오케스트레이터야. 사용자의 메시지를 분석해서 적절한 dal에게 작업을 배분해.

등록된 dal:
%s
%s
사용자 메시지: %s

규칙:
- 적절한 dal을 @멘션으로 태그해서 지시해
- 여러 dal이 필요하면 각각 역할을 명시해
- 간결하게 지시해 (2-3문장)
- dal이 없는 작업이면 직접 답변해
- 반드시 @username 형식으로 태그해`, dalList, contextLine, message)

	response, err := c.executor.Run(ctx, ModeAsk, prompt)
	if err != nil {
		log.Printf("[conductor] claude error: %v", err)
		return ""
	}
	response = c.sanitizer.Clean(response)
	log.Printf("[conductor] routing: %s", truncate(response, 120))
	return response
}

func (c *Conductor) buildDalList() string {
	var parts []string
	for _, a := range c.cfg.Dals {
		parts = append(parts, fmt.Sprintf("- @%s: %s", a.Username, a.Role))
	}
	return strings.Join(parts, "\n")
}

func (c *Conductor) isDalBot(userID string) bool {
	return c.dalBotIDs[userID]
}

func (c *Conductor) isDuplicate(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen[id] {
		return true
	}
	c.seen[id] = true
	return false
}
