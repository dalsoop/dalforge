package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dalsoop/dalcenter/internal/localdal"
	"github.com/dalsoop/dalcenter/internal/talk"
)

// MattermostConfig holds Mattermost connection settings.
type MattermostConfig struct {
	URL        string // e.g. http://10.50.0.202:8065
	AdminToken string // PAT with admin access
	TeamName   string // e.g. "prelik"
}

// Daemon is the dalcenter HTTP API server.
type Daemon struct {
	addr         string
	localdalRoot string
	serviceRepo  string // service repo path to mount as /workspace
	mm           *MattermostConfig
	apiToken     string                // Bearer token for write endpoints (empty = no auth)
	channelID    string                // channel for this project
	opsChannelID string                // shared ops channel for credential sync notices
	containers   map[string]*Container // dal name -> container
	mu           sync.RWMutex
	credSyncMu   sync.Mutex
	credSyncLast map[string]time.Time
	escalations  *escalationStore
	claims       *claimStore
	tasks        *taskStore
	feedback     *feedbackStore
	costs        *costStore
	registry     *Registry
	startTime    time.Time
}

// Container tracks a running dal Docker container.
type Container struct {
	DalName     string `json:"dal_name"`
	UUID        string `json:"uuid"`
	Player      string `json:"player"`
	Role        string `json:"role"`
	ContainerID string `json:"container_id"`
	Status      string `json:"status"`    // "running", "stopped"
	Workspace   string `json:"workspace"` // "shared" or "clone"
	Skills      int    `json:"skills"`
	BotToken    string `json:"-"` // Mattermost bot token
	BotUsername string `json:"-"` // Mattermost bot @username
}

// New creates a daemon.
func New(addr, localdalRoot, serviceRepo string, mm *MattermostConfig) *Daemon {
	token := os.Getenv("DALCENTER_TOKEN")
	if token != "" {
		log.Println("[daemon] API token auth enabled for write endpoints")
	}
	inboxDir(serviceRepo)
	historyBufferDir(serviceRepo)
	wisdomInboxDir(serviceRepo)
	return &Daemon{
		addr:         addr,
		localdalRoot: localdalRoot,
		serviceRepo:  serviceRepo,
		mm:           mm,
		apiToken:     token,
		containers:   make(map[string]*Container),
		escalations:  newEscalationStoreWithFile(filepath.Join(dataDir(serviceRepo), "escalations.json")),
		claims:       newClaimStoreWithFile(filepath.Join(dataDir(serviceRepo), "claims.json")),
		tasks:        newTaskStore(),
		feedback:     newFeedbackStoreWithFile(filepath.Join(dataDir(serviceRepo), "feedback.json")),
		costs:        newCostStoreWithFile(filepath.Join(dataDir(serviceRepo), "costs.json"), orchestrationLogDir(serviceRepo)),
		registry:     newRegistry(serviceRepo),
		credSyncLast: newCredentialSyncMap(),
		startTime:    time.Now(),
	}
}

// uuidShort returns a 6-char identifier from a UUID for resource naming.
// Strips hyphens first to avoid ambiguity in container name parsing.
func uuidShort(uuid string) string {
	clean := strings.ReplaceAll(uuid, "-", "")
	if len(clean) > 6 {
		return clean[:6]
	}
	return clean
}

// dalContainerName returns the Docker container name: dal-{name}-{uuid[:6]}
func dalContainerName(name, uuid string) string {
	return containerBasePrefix + name + "-" + uuidShort(uuid)
}

// dalBotUsername returns the MM bot username: dal-{name}
// Uses a stable name (no UUID) so the bot is reused across wake cycles.
func dalBotUsername(name, _ string) string {
	return containerBasePrefix + name
}

// requireAuth is middleware that checks Bearer token for write endpoints.
// If DALCENTER_TOKEN is not set, all requests are allowed.
func (d *Daemon) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.apiToken == "" {
			next(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if auth == "" || token == auth || token != d.apiToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// Run starts the HTTP server.
func (d *Daemon) Run(ctx context.Context) error {
	// Start soft-serve as child process
	if ss, err := startSoftServe(ctx); err != nil {
		log.Printf("[daemon] soft-serve start failed: %v (continuing without)", err)
	} else if ss != nil {
		log.Printf("[daemon] soft-serve started (pid=%d)", ss.Process.Pid)
		defer func() {
			ss.Process.Kill()
			ss.Wait()
		}()
	}

	// Start credential watcher
	go startCredentialWatcher(ctx, d)

	// Start repo watcher (git fetch/pull → sync on .dal/ changes)
	go startRepoWatcher(ctx, d.serviceRepo, func() {
		d.runSync()
	})

	// Start peer health watcher (dalcenter HA)
	go d.startPeerWatcher(ctx)

	// Export MM URL for containers to use
	if d.mm != nil && d.mm.URL != "" {
		os.Setenv("DALCENTER_MM_URL", d.mm.URL)
	}

	// Setup Mattermost channel (repo name = channel name)
	if d.mm != nil && d.mm.URL != "" {
		repoName := filepath.Base(d.serviceRepo)
		if repoName == "" || repoName == "." {
			repoName = "dalcenter"
		}
		teamID, channelID, err := talk.GetTeamAndChannel(d.mm.URL, d.mm.AdminToken, d.mm.TeamName, repoName)
		if err != nil {
			log.Printf("[daemon] mattermost channel setup failed: %v", err)
		} else {
			d.channelID = channelID
			log.Printf("[daemon] mattermost: team=%s channel=%s (%s)", teamID, channelID[:8], repoName)

			// dal-leaders 공유 채널 자동 생성 (cross-project leader 소통)
			if leadersChID, err := talk.FindOrCreateChannel(d.mm.URL, d.mm.AdminToken, teamID, "dal-leaders"); err == nil {
				log.Printf("[daemon] dal-leaders channel ready: %s", leadersChID[:8])
			}
			if credentialOpsEnabled() {
				if opsChID, err := talk.FindOrCreateChannel(d.mm.URL, d.mm.AdminToken, teamID, credentialOpsChannelName()); err == nil {
					d.opsChannelID = opsChID
					log.Printf("[daemon] credential ops channel ready: %s (%s)", opsChID[:8], credentialOpsChannelName())
				} else {
					log.Printf("[daemon] credential ops channel setup failed: %v", err)
				}
			}
		}
	}

	// Reconcile existing containers from a previous daemon run
	d.reconcile()

	// Cleanup orphan bot DMs on startup (delayed to ensure reconcile is complete)
	if d.mm != nil && d.mm.URL != "" {
		go func() {
			// Wait for reconcile to finish populating containers
			time.Sleep(30 * time.Second)

			teamID, _, _ := talk.GetTeamAndChannel(d.mm.URL, d.mm.AdminToken, d.mm.TeamName, filepath.Base(d.serviceRepo))
			if teamID != "" {
				// Collect active bot usernames from running containers
				d.mu.RLock()
				var active []string
				for _, c := range d.containers {
					if c.BotUsername != "" {
						active = append(active, c.BotUsername)
					}
				}
				d.mu.RUnlock()
				// Always keep system bots and dalcenter-admin
				active = append(active, "dalcenter-admin")
				// Only run cleanup if we have at least 1 active container
				// (prevents wiping all bots when reconcile finds nothing)
				if len(active) <= 1 {
					log.Printf("[daemon] orphan cleanup skipped: no active containers yet")
					return
				}
				talk.CleanupOrphanBotDMs(d.mm.URL, d.mm.AdminToken, teamID, active)
				log.Printf("[daemon] orphan bot DM cleanup done (active=%d)", len(active))
			}
		}()
	}

	mux := http.NewServeMux()

	// Read-only endpoints — no auth required
	mux.HandleFunc("GET /api/health", d.handleHealth)
	mux.HandleFunc("GET /api/ps", d.handlePs)
	mux.HandleFunc("GET /api/status", d.handleStatus)
	mux.HandleFunc("GET /api/status/{name}", d.handleStatusOne)
	mux.HandleFunc("POST /api/validate", d.handleValidate)
	mux.HandleFunc("GET /api/logs/{name}", d.handleLogs)
	mux.HandleFunc("GET /runs/{id}", d.handleRunPage)
	// Write endpoints — require auth when DALCENTER_TOKEN is set
	mux.HandleFunc("POST /api/wake/{name}", d.requireAuth(d.handleWake))
	mux.HandleFunc("POST /api/sleep/{name}", d.requireAuth(d.handleSleep))
	mux.HandleFunc("POST /api/restart/{name}", d.requireAuth(d.handleRestart))
	mux.HandleFunc("POST /api/sync", d.requireAuth(d.handleSync))
	mux.HandleFunc("POST /api/message", d.requireAuth(d.handleMessage))
	mux.HandleFunc("GET /api/agent-config/{name}", d.handleAgentConfig)
	// Direct task execution (works without Mattermost)
	mux.HandleFunc("POST /api/task", d.requireAuth(d.handleTask))
	mux.HandleFunc("POST /api/task/start", d.requireAuth(d.handleTaskStart))
	mux.HandleFunc("GET /api/task/{id}", d.handleTaskStatus)
	mux.HandleFunc("POST /api/task/{id}/event", d.requireAuth(d.handleTaskEvent))
	mux.HandleFunc("POST /api/task/{id}/finish", d.requireAuth(d.handleTaskFinish))
	mux.HandleFunc("GET /api/tasks", d.handleTaskList)
	// Claims — dal feedback to host
	mux.HandleFunc("POST /api/claim", d.handleClaim)
	mux.HandleFunc("GET /api/claims", d.handleClaims)
	mux.HandleFunc("GET /api/claims/{id}", d.handleClaimGet)
	mux.HandleFunc("POST /api/claims/{id}/respond", d.requireAuth(d.handleClaimRespond))
	// Feedback — persistent task result tracking
	mux.HandleFunc("POST /api/feedback", d.handleFeedback)
	mux.HandleFunc("GET /api/feedback", d.handleFeedbackList)
	mux.HandleFunc("GET /api/feedback/stats", d.handleFeedbackStats)
	// Cost tracking
	mux.HandleFunc("POST /api/cost", d.requireAuth(d.handleCostRecord))
	mux.HandleFunc("GET /api/costs", d.handleCostList)
	mux.HandleFunc("GET /api/costs/summary", d.handleCostSummary)
	// Escalation endpoints
	mux.HandleFunc("POST /api/escalate", d.requireAuth(d.handleEscalate))
	mux.HandleFunc("GET /api/escalations", d.handleEscalations)
	mux.HandleFunc("POST /api/escalations/{id}/resolve", d.requireAuth(d.handleResolveEscalation))
	// A2A protocol endpoints
	mux.HandleFunc("GET /api/provider-status", d.handleProviderStatus)
	mux.HandleFunc("POST /api/provider-trip", d.handleProviderTrip)

	mux.HandleFunc("GET /.well-known/agent-card.json", d.handleAgentCard)
	mux.HandleFunc("POST /rpc", d.requireAuth(d.handleA2ARPC))

	srv := &http.Server{Addr: d.addr, Handler: mux}
	log.Printf("[daemon] listening on %s", d.addr)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("[daemon] shutdown error: %v", err)
		}
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	dalsRunning := len(d.containers)
	d.mu.RUnlock()

	dals, _ := localdal.ListDals(d.localdalRoot)

	respondJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"uptime":       time.Since(d.startTime).Truncate(time.Second).String(),
		"dals_running": dalsRunning,
		"repo_count":   len(dals),
	})
}

func (d *Daemon) handlePs(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	containers := make([]*Container, 0, len(d.containers))
	for _, c := range d.containers {
		containers = append(containers, c)
	}
	json.NewEncoder(w).Encode(containers)
}

func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	dals, err := localdal.ListDals(d.localdalRoot)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	type dalStatus struct {
		localdal.DalProfile
		ContainerStatus string `json:"container_status"`
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []dalStatus
	for _, dal := range dals {
		ds := dalStatus{DalProfile: dal, ContainerStatus: "sleeping"}
		if c, ok := d.containers[dal.Name]; ok {
			ds.ContainerStatus = c.Status
		}
		result = append(result, ds)
	}
	json.NewEncoder(w).Encode(result)
}

func (d *Daemon) handleStatusOne(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dal, err := localdal.ReadDalCue(d.dalCuePath(name), name)
	if err != nil {
		http.Error(w, fmt.Sprintf("dal %q not found: %v", name, err), 404)
		return
	}

	d.mu.RLock()
	c := d.containers[name]
	d.mu.RUnlock()

	status := "sleeping"
	containerID := ""
	if c != nil {
		status = c.Status
		containerID = c.ContainerID
	}

	json.NewEncoder(w).Encode(map[string]any{
		"uuid":         dal.UUID,
		"name":         dal.Name,
		"player":       dal.Player,
		"role":         dal.Role,
		"skills":       dal.Skills,
		"status":       status,
		"container_id": containerID,
	})
}

func (d *Daemon) handleWake(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dal, err := localdal.ReadDalCue(d.dalCuePath(name), name)
	if err != nil {
		http.Error(w, fmt.Sprintf("dal %q not found: %v", name, err), 404)
		return
	}

	// Support multiple instances: dev, dev-2, dev-3, ...
	instanceName := name
	d.mu.Lock()
	if _, ok := d.containers[name]; ok {
		for i := 2; ; i++ {
			candidate := fmt.Sprintf("%s-%d", name, i)
			if _, ok := d.containers[candidate]; !ok {
				instanceName = candidate
				break
			}
		}
	}
	d.mu.Unlock()

	// Clean any stopped container with the same name (best-effort, #33)
	targetContainerName := dalContainerName(instanceName, dal.UUID)
	if err := cleanStaleContainer(targetContainerName); err != nil {
		log.Printf("[daemon] clean stale container %s: %v (continuing)", targetContainerName, err)
	}

	containerID, warnings, err := dockerRun(d.localdalRoot, d.serviceRepo, instanceName, d.addr, dal)
	if err != nil {
		http.Error(w, fmt.Sprintf("wake failed: %v", err), 500)
		return
	}

	// Setup Mattermost bot for this dal
	var botToken string
	if d.mm != nil && d.mm.URL != "" && d.channelID != "" {
		botUsername := dalBotUsername(dal.Name, dal.UUID)
		teamID, _, _ := talk.GetTeamAndChannel(d.mm.URL, d.mm.AdminToken, d.mm.TeamName, filepath.Base(d.serviceRepo))
		bot, err := talk.SetupBot(d.mm.URL, d.mm.AdminToken, teamID, d.channelID, botUsername, dal.Name, fmt.Sprintf("dal %s (%s)", dal.Name, dal.Role))
		if err != nil {
			log.Printf("[daemon] mattermost bot setup for %s: %v (continuing without)", name, err)
		} else {
			botToken = bot.Token
			log.Printf("[daemon] mattermost bot: %s (user=%s)", botUsername, bot.UserID[:8])

			// Leader → dal-leaders 공유 채널에도 자동 참여 (cross-project 소통)
			if dal.Role == "leader" {
				leadersChName := "dal-leaders"
				if leadersChID, err := talk.FindOrCreateChannel(d.mm.URL, d.mm.AdminToken, teamID, leadersChName); err == nil {
					if err := talk.AddUserToChannel(d.mm.URL, d.mm.AdminToken, leadersChID, bot.UserID); err == nil {
						log.Printf("[daemon] leader %s joined dal-leaders channel", botUsername)
					}
				}
			}
		}
	}

	ws := dal.Workspace
	if ws == "" {
		ws = "shared"
	}
	botUser := dalBotUsername(dal.Name, dal.UUID)
	d.mu.Lock()
	d.containers[instanceName] = &Container{
		DalName:     instanceName,
		UUID:        dal.UUID,
		Player:      dal.Player,
		Role:        dal.Role,
		ContainerID: containerID,
		Status:      "running",
		Workspace:   ws,
		Skills:      len(dal.Skills),
		BotToken:    botToken,
		BotUsername: botUser,
	}
	d.mu.Unlock()

	// Persist to registry
	d.registry.Set(dal.UUID, RegistryEntry{
		Name:        instanceName,
		Repo:        d.serviceRepo,
		ContainerID: containerID,
		BotToken:    botToken,
		Status:      "running",
	})

	cid := containerID
	if len(cid) > 12 {
		cid = cid[:12]
	}
	uid := dal.UUID
	if len(uid) > 8 {
		uid = uid[:8]
	}
	log.Printf("[daemon] wake: %s (uuid=%s, container=%s)", instanceName, uid, cid)
	resp := map[string]any{
		"status":       "awake",
		"dal":          instanceName,
		"container_id": containerID,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	json.NewEncoder(w).Encode(resp)
}

func (d *Daemon) handleSleep(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	d.mu.Lock()
	c, ok := d.containers[name]
	if !ok {
		d.mu.Unlock()
		http.Error(w, fmt.Sprintf("dal %q is not awake", name), 404)
		return
	}
	d.mu.Unlock()

	if err := dockerStop(c.ContainerID); err != nil {
		http.Error(w, fmt.Sprintf("sleep failed: %v", err), 500)
		return
	}

	// Remove bot from channel + hide DM on sleep (prevent zombie bots)
	if d.mm != nil && d.mm.URL != "" && d.channelID != "" && c.BotUsername != "" {
		talk.RemoveBotFromChannel(d.mm.URL, d.mm.AdminToken, d.channelID, c.BotUsername)
		teamID, _, _ := talk.GetTeamAndChannel(d.mm.URL, d.mm.AdminToken, d.mm.TeamName, filepath.Base(d.serviceRepo))
		if teamID != "" {
			talk.HideBotDMFromUsers(d.mm.URL, d.mm.AdminToken, teamID, c.BotUsername)
		}
	}

	d.mu.Lock()
	c.Status = "stopped"
	delete(d.containers, name)
	d.mu.Unlock()

	log.Printf("[daemon] sleep: %s", name)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "sleeping",
		"dal":    name,
	})
}

// handleRestart stops a dal, removes container, and wakes fresh (with new bot token).
func (d *Daemon) handleRestart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	d.mu.RLock()
	c, ok := d.containers[name]
	d.mu.RUnlock()

	if ok {
		dockerStop(c.ContainerID)
		if d.mm != nil && d.mm.URL != "" && d.channelID != "" && c.BotUsername != "" {
			talk.RemoveBotFromChannel(d.mm.URL, d.mm.AdminToken, d.channelID, c.BotUsername)
		}
		d.mu.Lock()
		delete(d.containers, name)
		d.mu.Unlock()
	}

	log.Printf("[daemon] restart: %s (clean wake)", name)
	d.handleWake(w, r)
}

func (d *Daemon) handleSync(w http.ResponseWriter, r *http.Request) {
	synced, restarted := d.runSync()

	if w == nil {
		return
	}
	if len(synced) == 0 && len(restarted) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"status": "no running dals"})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"status":    "synced",
		"dals":      synced,
		"restarted": restarted,
	})
}

// runSync checks running containers against their dal.cue definitions
// and restarts containers whose configuration has changed.
func (d *Daemon) runSync() (synced, restarted []string) {
	d.mu.RLock()
	running := make(map[string]*Container)
	for k, v := range d.containers {
		if v.Status == "running" {
			running[k] = v
		}
	}
	d.mu.RUnlock()

	if len(running) == 0 {
		return nil, nil
	}

	for name, c := range running {
		dal, err := localdal.ReadDalCue(d.dalCuePath(name), name)
		if err != nil {
			log.Printf("[daemon] sync skip %s: %v", name, err)
			continue
		}
		needsRestart, reason, err := dockerSync(d.localdalRoot, c.ContainerID, dal)
		if err != nil {
			log.Printf("[daemon] sync error %s: %v", name, err)
			continue
		}
		if needsRestart {
			log.Printf("[daemon] sync restart %s: %s", name, reason)
			oldID := c.ContainerID
			if err := dockerStop(oldID); err != nil {
				log.Printf("[daemon] sync restart stop %s: %v", name, err)
				continue
			}
			newID, _, err := dockerRun(d.localdalRoot, d.serviceRepo, name, d.addr, dal)
			if err != nil {
				log.Printf("[daemon] sync restart run %s: %v", name, err)
				d.mu.Lock()
				delete(d.containers, name)
				d.mu.Unlock()
				continue
			}
			d.mu.Lock()
			d.containers[name] = &Container{
				DalName:     name,
				UUID:        dal.UUID,
				Player:      dal.Player,
				Role:        dal.Role,
				ContainerID: newID,
				Status:      "running",
				Skills:      len(dal.Skills),
				BotToken:    c.BotToken,
			}
			d.mu.Unlock()
			restarted = append(restarted, name)
			cid := newID
			if len(cid) > 12 {
				cid = cid[:12]
			}
			log.Printf("[daemon] sync restarted %s: %s (new container=%s)", name, reason, cid)
		}
		synced = append(synced, name)
	}

	log.Printf("[daemon] sync: synced=%v restarted=%v", synced, restarted)
	return synced, restarted
}

func (d *Daemon) handleValidate(w http.ResponseWriter, r *http.Request) {
	errors := localdal.Validate(d.localdalRoot)
	if len(errors) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	w.WriteHeader(400)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "invalid",
		"errors": errors,
	})
}

func (d *Daemon) handleLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	d.mu.RLock()
	c, ok := d.containers[name]
	d.mu.RUnlock()

	if !ok {
		http.Error(w, fmt.Sprintf("dal %q is not awake", name), 404)
		return
	}

	// Role-aware warning: member dals should be accessed via leader routing
	if c.Role == "member" {
		log.Printf("[scope] ⚠️ direct logs access to member %q — prefer leader routing", name)
	}

	logs, err := dockerLogs(c.ContainerID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(logs))
}

func (d *Daemon) handleRunPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tr := d.tasks.Get(id)
	if tr == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	formatTimeline := func(events []taskEvent) string {
		if len(events) == 0 {
			return "(no events yet)"
		}
		var b strings.Builder
		for _, ev := range events {
			at := ev.At.Format(time.RFC3339)
			if ev.At.IsZero() {
				at = "unknown-time"
			}
			line := fmt.Sprintf("[%s] %s", at, ev.Message)
			if ev.Kind != "" {
				line = fmt.Sprintf("[%s] (%s) %s", at, ev.Kind, ev.Message)
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(line)
		}
		return b.String()
	}

	const page = `<!doctype html>
<html lang="ko">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>dalcenter run {{.ID}}</title>
  <style>
    :root { color-scheme: light; --bg:#f6f4ed; --panel:#fffdf7; --ink:#1a1a1a; --muted:#6b675d; --line:#d9d1bd; --accent:#0f766e; --warn:#b45309; --fail:#b91c1c; }
    body { margin:0; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background:linear-gradient(180deg,#f3efe2,#f9f8f3); color:var(--ink); }
    .wrap { max-width: 980px; margin: 0 auto; padding: 28px 20px 40px; }
    .hero { display:flex; justify-content:space-between; gap:16px; align-items:flex-start; margin-bottom:20px; }
    .title { font-size: 28px; font-weight: 700; letter-spacing: -0.02em; }
    .meta { color: var(--muted); font-size: 13px; margin-top: 8px; }
    .badge { display:inline-block; padding: 4px 10px; border-radius: 999px; border:1px solid var(--line); background:var(--panel); font-size:12px; }
    .badge.running { color: var(--accent); border-color: #8fd4ce; }
    .badge.done { color: var(--accent); border-color: #8fd4ce; }
    .badge.failed { color: var(--fail); border-color: #f0a7a7; }
    .badge.blocked { color: var(--warn); border-color: #efc17e; }
    .card { background:var(--panel); border:1px solid var(--line); border-radius:16px; padding:16px; margin-bottom:16px; box-shadow:0 6px 24px rgba(44,31,0,.06); }
    .label { font-size:12px; text-transform:uppercase; letter-spacing:.08em; color:var(--muted); margin-bottom:8px; }
    pre { margin:0; white-space:pre-wrap; word-break:break-word; font:inherit; line-height:1.5; }
    a { color:#0b4f6c; text-decoration:none; }
    a:hover { text-decoration:underline; }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="hero">
      <div>
        <div class="title">run {{.ID}}</div>
        <div class="meta">dal <strong>{{.Dal}}</strong> · started <span id="startedAt">{{.StartedAt}}</span></div>
      </div>
      <div class="badge {{.Status}}" id="status">{{.Status}}</div>
    </div>

    <div class="card">
      <div class="label">Task</div>
      <pre id="task">{{.Task}}</pre>
    </div>

    <div class="card">
      <div class="label">Output</div>
      <pre id="output">{{.Output}}</pre>
    </div>

    <div class="card">
      <div class="label">Error</div>
      <pre id="error">{{.Error}}</pre>
    </div>

    <div class="card">
      <div class="label">Links</div>
      <a id="logsLink" href="/api/logs/{{.Dal}}" target="_blank" rel="noreferrer">dal logs</a>
    </div>

    <div class="card">
      <div class="label">Summary</div>
      <pre id="summary">{{if .DoneAt}}done={{.DoneAt}}{{else}}done=pending{{end}}
git_changes={{.GitChanges}}
verified={{if .Verified}}{{.Verified}}{{else}}pending{{end}}</pre>
    </div>

    <div class="card">
      <div class="label">Timeline</div>
      <pre id="timeline">{{timeline .Events}}</pre>
    </div>

    <div class="card">
      <div class="label">Verification</div>
      <pre id="verification">{{if .Completion}}{{if .Completion.Skipped}}skipped: {{.Completion.SkipReason}}{{else}}build={{.Completion.BuildOK}} test={{.Completion.TestOK}} duration={{.Completion.Duration}}{{end}}{{else}}pending{{end}}</pre>
    </div>

    <div class="card">
      <div class="label">Git Diff</div>
      <pre id="gitdiff">{{if .GitDiff}}{{.GitDiff}}{{else}}(none){{end}}</pre>
    </div>

    <div class="card">
      <div class="label">Build Output</div>
      <pre id="buildOutput">{{if and .Completion .Completion.BuildOutput}}{{.Completion.BuildOutput}}{{else}}(none){{end}}</pre>
    </div>

    <div class="card">
      <div class="label">Test Output</div>
      <pre id="testOutput">{{if and .Completion .Completion.TestOutput}}{{.Completion.TestOutput}}{{else}}(none){{end}}</pre>
    </div>
  </div>

  <script>
    const taskId = "{{.ID}}";
    const terminal = new Set(["done","failed","blocked","noop"]);
    function formatSummary(data) {
      const doneAt = data.done_at || "pending";
      const gitChanges = Number.isFinite(data.git_changes) ? data.git_changes : 0;
      const verified = data.verified || "pending";
      return "done=" + doneAt + "\n" + "git_changes=" + gitChanges + "\n" + "verified=" + verified;
    }
    function formatTimeline(events) {
      if (!events || events.length === 0) return "(no events yet)";
      return events.map((event) => {
        const at = event.at || "unknown-time";
        const kind = event.kind ? " (" + event.kind + ")" : "";
        return "[" + at + "]" + kind + " " + (event.message || "");
      }).join("\n");
    }
    async function refresh() {
      const res = await fetch("/api/task/" + taskId, {headers: {"Accept":"application/json"}});
      if (!res.ok) return;
      const data = await res.json();
      document.getElementById("status").textContent = data.status || "";
      document.getElementById("status").className = "badge " + (data.status || "");
      document.getElementById("task").textContent = data.task || "";
      document.getElementById("output").textContent = data.output || "";
      document.getElementById("error").textContent = data.error || "";
      document.getElementById("summary").textContent = formatSummary(data);
      document.getElementById("timeline").textContent = formatTimeline(data.events || []);
      document.getElementById("gitdiff").textContent = data.git_diff || "(none)";
      const completion = data.completion || null;
      let verification = "pending";
      let buildOutput = "(none)";
      let testOutput = "(none)";
      if (completion) {
        if (completion.skipped) {
          verification = "skipped: " + (completion.skip_reason || "");
        } else {
          verification = "build=" + Boolean(completion.build_ok) + " test=" + Boolean(completion.test_ok) + " duration=" + (completion.duration || "");
        }
        if (completion.build_output) buildOutput = completion.build_output;
        if (completion.test_output) testOutput = completion.test_output;
      }
      document.getElementById("verification").textContent = verification;
      document.getElementById("buildOutput").textContent = buildOutput;
      document.getElementById("testOutput").textContent = testOutput;
      if (data.started_at) document.getElementById("startedAt").textContent = data.started_at;
      if (!terminal.has(data.status)) window.setTimeout(refresh, 2000);
    }
    window.setTimeout(refresh, 1500);
  </script>
</body>
</html>`

	tpl := template.Must(template.New("run").Funcs(template.FuncMap{"timeline": formatTimeline}).Parse(page))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tpl.Execute(w, tr)
}

func (d *Daemon) handleMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From     string `json:"from"`
		Message  string `json:"message"`
		ThreadID string `json:"thread_id,omitempty"` // reply to thread
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if d.mm == nil || d.channelID == "" {
		// Fallback: if Mattermost is not configured, try direct task execution
		// Extract task from message and execute in container
		d.mu.RLock()
		_, hasDal := d.containers[req.From]
		d.mu.RUnlock()
		if !hasDal {
			// Find first running dal to target (from message content heuristics)
			d.mu.RLock()
			for name := range d.containers {
				req.From = name
				break
			}
			d.mu.RUnlock()
		}
		if req.From != "" {
			d.mu.RLock()
			c, ok := d.containers[req.From]
			d.mu.RUnlock()
			if ok {
				log.Printf("[scope] ⚠️ message fallback to direct task — Mattermost not configured, bypassing leader routing")
				tr := d.tasks.New(c.DalName, req.Message)
				go d.execTaskInContainer(c, tr)
				respondJSON(w, http.StatusAccepted, map[string]string{
					"status":  "task_dispatched",
					"task_id": tr.ID,
					"note":    "mattermost unavailable — task dispatched directly to container",
				})
				return
			}
		}
		http.Error(w, "mattermost not configured and no running dals", 503)
		return
	}

	// Use admin token when posting TO a dal (so the dal sees it as external message).
	// Bot token would make the dal's bridge skip the message as "self".
	// Bot token is only for the dal's OWN responses — if we use the dal's bot token here,
	// the dal's bridge will skip the message as "self".
	token := d.mm.AdminToken

	msg := req.Message
	d.mu.RLock()
	_, fromRunningDal := d.containers[req.From]
	if !fromRunningDal {
		// Auto-prefix leader mention only for external/user-originated messages.
		// Internal dal notices (claims, reports, status) should reach humans without
		// being re-consumed by the leader as a new task.
		for _, c := range d.containers {
			if c.Role == "leader" {
				// Use the stable Mattermost bot username if available.
				mention := "@"
				if c.BotUsername != "" {
					mention += c.BotUsername
				} else {
					mention += dalBotUsername(c.DalName, c.UUID)
				}
				legacyMention := fmt.Sprintf("@dal-%s-%s", c.DalName, uuidShort(c.UUID))
				altMention := "@" + c.DalName
				if !strings.Contains(msg, mention) && !strings.Contains(msg, legacyMention) && !strings.Contains(msg, altMention) {
					msg = mention + " " + msg
				}
				break
			}
		}
	}
	d.mu.RUnlock()

	// Post message (with optional thread)
	var body string
	if req.ThreadID != "" {
		body = fmt.Sprintf(`{"channel_id":%q,"message":%q,"root_id":%q}`, d.channelID, msg, req.ThreadID)
	} else {
		body = fmt.Sprintf(`{"channel_id":%q,"message":%q}`, d.channelID, msg)
	}
	respBody, err := mmPost(d.mm.URL, token, "/api/v4/posts", body)
	if err != nil {
		http.Error(w, fmt.Sprintf("post failed: %v", err), 500)
		return
	}

	// Return post ID so caller can start/continue thread
	postID := ""
	var postResp map[string]any
	if json.Unmarshal(respBody, &postResp) == nil {
		if id, ok := postResp["id"].(string); ok {
			postID = id
		}
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":  "sent",
		"post_id": postID,
	})
}

func mmPost(mmURL, token, path, body string) ([]byte, error) {
	req, _ := http.NewRequest("POST", mmURL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// reconcile discovers existing dal-* containers and restores daemon state.
// This is best-effort: running containers with matching dal.cue are added
// to d.containers; orphans and stopped containers are logged only.
func (d *Daemon) reconcile() {
	// Load registry for bot token restoration
	d.registry.Load()

	// Build a set of known UUIDs from this daemon's .dal/ directory
	knownUUIDs := d.collectUUIDs()

	// Discover containers that belong to any known UUID
	containers, err := discoverContainersByUUIDs(knownUUIDs)
	if err != nil {
		log.Printf("[daemon] reconcile: %v", err)
		return
	}
	if len(containers) == 0 {
		return
	}

	var running, stopped int
	for _, c := range containers {
		if c.Running {
			// Find dal name from registry or container name
			entry := d.registry.GetByContainerID(c.ID)
			instanceName := ""
			if entry != nil {
				instanceName = entry.Name
			} else {
				// Derive from container name: dal-{name}-{uuid[:6]} → {name}
				instanceName = extractInstanceName(c.Name)
			}
			if instanceName == "" {
				log.Printf("[daemon] reconcile: orphan container %s (%s)", c.Name, c.ID[:12])
				continue
			}

			dal, err := localdal.ReadDalCue(d.dalCuePath(instanceName), instanceName)
			if err != nil {
				// Try stripping instance suffix: "dev-2" -> "dev"
				if idx := strings.LastIndex(instanceName, "-"); idx > 0 {
					baseName := instanceName[:idx]
					dal, err = localdal.ReadDalCue(d.dalCuePath(baseName), baseName)
				}
			}
			if err != nil {
				log.Printf("[daemon] reconcile: orphan running container %s (%s) — no matching dal.cue", c.Name, c.ID[:12])
				continue
			}

			botToken := ""
			if entry != nil {
				botToken = entry.BotToken
			}

			d.mu.Lock()
			d.containers[instanceName] = &Container{
				DalName:     instanceName,
				UUID:        dal.UUID,
				Player:      dal.Player,
				Role:        dal.Role,
				ContainerID: c.ID,
				Status:      "running",
				Skills:      len(dal.Skills),
				BotToken:    botToken,
			}
			d.mu.Unlock()
			running++
			log.Printf("[daemon] reconcile: restored %s (container=%s)", instanceName, c.ID[:12])
		} else {
			stopped++
			log.Printf("[daemon] reconcile: stopped container %s (%s) — skipping", c.Name, c.ID[:12])
		}
	}

	if running > 0 || stopped > 0 {
		log.Printf("[daemon] reconcile: found %d running, %d stopped dal containers", running, stopped)
	}
}

// collectUUIDs reads all dal.cue files and returns their UUIDs.
func (d *Daemon) collectUUIDs() []string {
	entries, err := os.ReadDir(d.localdalRoot)
	if err != nil {
		return nil
	}
	var uuids []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "skills" || e.Name() == "hooks" {
			continue
		}
		dal, err := localdal.ReadDalCue(d.dalCuePath(e.Name()), e.Name())
		if err != nil {
			continue
		}
		uuids = append(uuids, dal.UUID)
	}
	return uuids
}

// extractInstanceName derives the dal name from container name: dal-{name}-{uuid[:6]}
func extractInstanceName(containerName string) string {
	s := strings.TrimPrefix(containerName, containerBasePrefix)
	// Remove last segment (uuid[:6]): "leader-a1b2c3" → "leader"
	if idx := strings.LastIndex(s, "-"); idx > 0 {
		return s[:idx]
	}
	return s
}

// handleAgentConfig returns MM connection info for a dal to run its agent loop.
// Called by dalcli run inside the container.
func (d *Daemon) handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	d.mu.RLock()
	c, ok := d.containers[name]
	d.mu.RUnlock()
	if !ok {
		http.Error(w, "dal not found", 404)
		return
	}

	resp := map[string]string{
		"dal_name":   c.DalName,
		"uuid":       c.UUID,
		"bot_token":  c.BotToken,
		"channel_id": d.channelID,
	}
	if d.mm != nil {
		resp["mm_url"] = d.mm.URL
	}

	// Inject team member bot names so leader can mention them correctly
	d.mu.RLock()
	var teammates []string
	for n, cc := range d.containers {
		if n != name && cc.BotUsername != "" {
			teammates = append(teammates, fmt.Sprintf("%s=%s", n, cc.BotUsername))
		}
	}
	d.mu.RUnlock()
	if len(teammates) > 0 {
		resp["team_members"] = strings.Join(teammates, ",")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (d *Daemon) dalCuePath(name string) string {
	return fmt.Sprintf("%s/%s/dal.cue", d.localdalRoot, name)
}
