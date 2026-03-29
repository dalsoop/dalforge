package daemon

import (
	"bytes"
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

// ProviderCircuit is a centralized circuit breaker for AI provider rate limits.
// When any dal reports a rate limit, ALL dals switch to fallback for the cooldown period.
type ProviderCircuit struct {
	mu           sync.RWMutex
	primary      string        // default provider (e.g. "claude")
	fallback     string        // fallback provider (e.g. "codex")
	activePlayer string        // currently active provider
	trippedAt    time.Time     // when circuit was tripped
	cooldown     time.Duration // how long to stay on fallback
	trippedBy    string        // which dal triggered it
	reason       string        // error message
}

var globalCircuit = &ProviderCircuit{
	primary:      "claude",
	fallback:     "codex",
	activePlayer: "claude",
	cooldown:     4 * time.Hour,
}

const providerPeerHeader = "X-Dalcenter-Propagated"

func (pc *ProviderCircuit) refreshLocked(now time.Time) {
	if pc.activePlayer == pc.fallback && !pc.trippedAt.IsZero() && now.Sub(pc.trippedAt) > pc.cooldown {
		pc.activePlayer = pc.primary
		pc.trippedAt = time.Time{}
		pc.trippedBy = ""
		pc.reason = ""
		log.Printf("[provider-circuit] cooldown elapsed → back to %s", pc.primary)
	}
}

// Trip switches all dals to fallback provider.
// Returns true only when the circuit transitions to fallback.
func (pc *ProviderCircuit) Trip(dalName, reason string) bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	pc.refreshLocked(time.Now())
	if pc.activePlayer == pc.fallback {
		// Already on fallback
		return false
	}

	pc.activePlayer = pc.fallback
	pc.trippedAt = time.Now()
	pc.trippedBy = dalName
	pc.reason = reason
	log.Printf("[provider-circuit] TRIPPED by %s: %s → %s for %s (reason: %s)",
		dalName, pc.primary, pc.fallback, pc.cooldown, reason)
	return true
}

// ActiveProvider returns the currently active provider, resetting if cooldown elapsed.
func (pc *ProviderCircuit) ActiveProvider() string {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	pc.refreshLocked(time.Now())
	return pc.activePlayer
}

// Status returns circuit state for API response.
func (pc *ProviderCircuit) Status() map[string]interface{} {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	pc.refreshLocked(time.Now())

	result := map[string]interface{}{
		"active_provider": pc.activePlayer,
		"primary":         pc.primary,
		"fallback":        pc.fallback,
		"cooldown":        pc.cooldown.String(),
	}
	if pc.activePlayer == pc.fallback && !pc.trippedAt.IsZero() {
		remaining := pc.cooldown - time.Since(pc.trippedAt)
		if remaining < 0 {
			remaining = 0
		}
		result["tripped_at"] = pc.trippedAt.Format(time.RFC3339)
		result["tripped_by"] = pc.trippedBy
		result["reason"] = pc.reason
		result["resets_in"] = remaining.String()
	}
	return result
}

// handleProviderStatus returns the current provider circuit state.
func (d *Daemon) handleProviderStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(globalCircuit.Status())
}

// handleProviderTrip allows a dal to report a rate limit, tripping the circuit.
func (d *Daemon) handleProviderTrip(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DalName string `json:"dal_name"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.DalName == "" {
		http.Error(w, "dal_name required", 400)
		return
	}

	tripped := globalCircuit.Trip(req.DalName, req.Reason)
	if r.Header.Get(providerPeerHeader) != "1" {
		d.propagateProviderTrip(req.DalName, req.Reason)
	}

	// Notify via Mattermost if available
	if tripped && d.mm != nil && d.channelID != "" {
		msg := fmt.Sprintf("⚡ **Provider Circuit Tripped** by %s\n전체 dal이 **%s → %s**로 전환 (4시간)\n사유: %s",
			req.DalName, globalCircuit.primary, globalCircuit.fallback, req.Reason)
		body := fmt.Sprintf(`{"channel_id":%q,"message":%q}`, d.channelID, msg)
		r2, _ := http.NewRequest("POST", d.mm.URL+"/api/v4/posts", strings.NewReader(body))
		r2.Header.Set("Authorization", "Bearer "+d.mm.AdminToken)
		r2.Header.Set("Content-Type", "application/json")
		http.DefaultClient.Do(r2)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(globalCircuit.Status())
}

func (d *Daemon) propagateProviderTrip(dalName, reason string) {
	peers := providerPeerURLs(d.addr)
	if len(peers) == 0 {
		return
	}

	body, _ := json.Marshal(map[string]string{
		"dal_name": dalName,
		"reason":   reason,
	})
	client := &http.Client{Timeout: 2 * time.Second}
	for _, peer := range peers {
		req, err := http.NewRequest(http.MethodPost, peer+"/api/provider-trip", bytes.NewReader(body))
		if err != nil {
			log.Printf("[provider-circuit] peer trip request build failed for %s: %v", peer, err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(providerPeerHeader, "1")
		if d.apiToken != "" {
			req.Header.Set("Authorization", "Bearer "+d.apiToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[provider-circuit] peer trip failed for %s: %v", peer, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			log.Printf("[provider-circuit] peer trip failed for %s: status=%d", peer, resp.StatusCode)
			continue
		}
		log.Printf("[provider-circuit] propagated trip to %s", peer)
	}
}

func providerPeerURLs(selfAddr string) []string {
	if raw := strings.TrimSpace(os.Getenv("DALCENTER_PROVIDER_PEERS")); raw != "" {
		return parseProviderPeers(raw, selfAddr)
	}

	selfPort := addrPort(selfAddr)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var peers []string
	for port := 11190; port <= 11250; port++ {
		if port == selfPort {
			continue
		}
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		resp, err := client.Get(base + "/api/health")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			peers = append(peers, base)
		}
	}
	return peers
}

func parseProviderPeers(raw, selfAddr string) []string {
	selfPort := addrPort(selfAddr)
	seen := make(map[string]bool)
	var peers []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		base := normalizeProviderPeer(part)
		if base == "" {
			continue
		}
		if port := addrPort(base); port != 0 && port == selfPort {
			continue
		}
		if seen[base] {
			continue
		}
		seen[base] = true
		peers = append(peers, base)
	}
	return peers
}

func normalizeProviderPeer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "http://127.0.0.1:" + s
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return strings.TrimRight(s, "/")
	}
	if strings.HasPrefix(s, ":") {
		return "http://127.0.0.1" + s
	}
	return ""
}

func addrPort(addr string) int {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return 0
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		if idx := strings.LastIndex(addr, ":"); idx >= 0 && idx+1 < len(addr) {
			if port, err := strconv.Atoi(addr[idx+1:]); err == nil {
				return port
			}
		}
		return 0
	}
	addr = strings.TrimPrefix(addr, ":")
	if port, err := strconv.Atoi(addr); err == nil {
		return port
	}
	return 0
}
