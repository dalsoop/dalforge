// dalbridge — Mattermost outgoing webhook → SSE stream 릴레이
//
// matterbridge의 websocket 대신 outgoing webhook을 받아
// SSE(Server-Sent Events) stream으로 재전송한다.
// dalroot-listener가 이 stream을 구독하여 멘션을 감지한다.
//
// 환경변수:
//
//	DALBRIDGE_LISTEN       — 리슨 주소 (default: ":4280")
//	DALBRIDGE_WEBHOOK_TOKEN — outgoing webhook 검증 토큰 (optional)
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// webhookPayload is the Mattermost outgoing webhook format.
type webhookPayload struct {
	Token       string `json:"token"`
	ChannelName string `json:"channel_name"`
	UserName    string `json:"user_name"`
	Text        string `json:"text"`
	PostID      string `json:"post_id"`
	Timestamp   int64  `json:"timestamp"`
}

// streamMessage is the normalized message sent to SSE clients.
type streamMessage struct {
	Text      string `json:"text"`
	Username  string `json:"username"`
	Channel   string `json:"channel"`
	Gateway   string `json:"gateway,omitempty"`
	PostID    string `json:"post_id"`
	Timestamp string `json:"timestamp"`
}

// broker manages SSE client connections and message broadcasting.
type broker struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

func newBroker() *broker {
	return &broker{clients: make(map[chan []byte]struct{})}
}

func (b *broker) subscribe() chan []byte {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *broker) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

func (b *broker) broadcast(data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- data:
		default:
			// slow client — drop message
		}
	}
}

func (b *broker) clientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

func main() {
	listen := os.Getenv("DALBRIDGE_LISTEN")
	if listen == "" {
		listen = ":4280"
	}
	webhookToken := os.Getenv("DALBRIDGE_WEBHOOK_TOKEN")

	b := newBroker()
	mux := http.NewServeMux()

	// POST /webhook — Mattermost outgoing webhook 수신
	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var payload webhookPayload
		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(ct, "application/json"):
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
		default:
			// MM outgoing webhook 기본값: application/x-www-form-urlencoded
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
			payload.Token = r.FormValue("token")
			payload.ChannelName = r.FormValue("channel_name")
			payload.UserName = r.FormValue("user_name")
			payload.Text = r.FormValue("text")
			payload.PostID = r.FormValue("post_id")
			if ts := r.FormValue("timestamp"); ts != "" {
				payload.Timestamp, _ = strconv.ParseInt(ts, 10, 64)
			}
		}

		// 토큰 검증 (설정된 경우)
		if webhookToken != "" && payload.Token != webhookToken {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		msg := streamMessage{
			Text:      payload.Text,
			Username:  payload.UserName,
			Channel:   payload.ChannelName,
			PostID:    payload.PostID,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		data, err := json.Marshal(msg)
		if err != nil {
			http.Error(w, "marshal error", http.StatusInternalServerError)
			return
		}

		b.broadcast(data)
		log.Printf("[webhook] %s@%s: %s (%d clients)",
			payload.UserName, payload.ChannelName,
			truncate(payload.Text, 80), b.clientCount())

		// MM outgoing webhook은 응답 본문을 채널에 포스팅하므로 빈 JSON 반환
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{}")
	})

	// GET /stream — SSE stream
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		gatewayFilter := r.URL.Query().Get("gateway")

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		ch := b.subscribe()
		defer b.unsubscribe(ch)

		// 연결 확인 메시지 (api_connected: matterbridge 호환)
		fmt.Fprintf(w, "data: {\"event\":\"api_connected\"}\n\n")
		flusher.Flush()

		log.Printf("[stream] client connected (gateway=%q, %d total)", gatewayFilter, b.clientCount())
		defer func() {
			log.Printf("[stream] client disconnected (%d remaining)", b.clientCount())
		}()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-ch:
				if !ok {
					return
				}
				// gateway 필터링: 파라미터가 있으면 해당 gateway만 전달
				if gatewayFilter != "" {
					var msg struct {
						Gateway string `json:"gateway"`
					}
					if json.Unmarshal(data, &msg) == nil && msg.Gateway != gatewayFilter {
						continue
					}
				}
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	})

	// POST /api/message — dal → stream 메시지 릴레이
	// dalcli-leader, daemon bridgePost 등에서 사용
	mux.HandleFunc("/api/message", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var payload struct {
			Text     string `json:"text"`
			Username string `json:"username"`
			Gateway  string `json:"gateway"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		msg := streamMessage{
			Text:      payload.Text,
			Username:  payload.Username,
			Gateway:   payload.Gateway,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		data, err := json.Marshal(msg)
		if err != nil {
			http.Error(w, "marshal error", http.StatusInternalServerError)
			return
		}

		b.broadcast(data)
		log.Printf("[message] %s@%s: %s (%d clients)",
			payload.Username, payload.Gateway,
			truncate(payload.Text, 80), b.clientCount())

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	// GET /health — 헬스 체크
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","clients":%d}`, b.clientCount())
	})

	log.Printf("[dalbridge] listening on %s", listen)
	if err := http.ListenAndServe(listen, mux); err != nil {
		log.Fatalf("[dalbridge] %v", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
