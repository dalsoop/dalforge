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
)

const (
	// DefaultAddr is the default listen address for the dalcenter daemon.
	DefaultAddr = ":11190"
	// DefaultBridgePort is the default matterbridge API port.
	DefaultBridgePort = "4242"
	// DefaultBridgeURL is the default matterbridge API URL.
	DefaultBridgeURL = "http://localhost:" + DefaultBridgePort
)

// Daemon is the dalcenter HTTP API server.
type Daemon struct {
	addr         string
	localdalRoot string
	serviceRepo  string // service repo path to mount as /workspace
	bridgeURL      string // matterbridge API URL (daemon→MM posting)
	dalbridgeURL   string // dalbridge URL for dal containers (webhook stream)
	bridgeConf     string // matterbridge config path (child process)
	githubRepo   string // GitHub repo for issue polling (e.g., "owner/repo")
	apiToken     string                // Bearer token for write endpoints (empty = no auth)
	containers   map[string]*Container // dal name -> container
	mu           sync.RWMutex
	credSyncMu    sync.Mutex
	credSyncLast  map[string]time.Time
	escNoticeLast map[string]time.Time
	escalations   *escalationStore
	claims       *claimStore
	tasks        *taskStore
	feedback     *feedbackStore
	costs          *costStore
	issues           *issueStore
	issueWorkflows   *issueWorkflowStore
	dalrootNotifier  *dalrootNotifier
	registry         *Registry
	startTime        time.Time
}

// Container tracks a running dal Docker container.
type Container struct {
	DalName     string    `json:"dal_name"`
	UUID        string    `json:"uuid"`
	Player      string    `json:"player"`
	Role        string    `json:"role"`
	Description string    `json:"description,omitempty"`
	ContainerID string    `json:"container_id"`
	Status      string    `json:"status"`    // "running", "stopped"
	Workspace   string    `json:"workspace"` // "shared" or "clone"
	Skills      int       `json:"skills"`
	LastSeenAt  time.Time `json:"last_seen_at,omitempty"`
	IdleFor     string    `json:"idle_for,omitempty"`
}

// New creates a daemon.
func New(addr, localdalRoot, serviceRepo, bridgeURL, dalbridgeURL, bridgeConf, githubRepo string) *Daemon {
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
		bridgeURL:    strings.TrimRight(bridgeURL, "/"),
		dalbridgeURL: strings.TrimRight(dalbridgeURL, "/"),
		bridgeConf:   bridgeConf,
		githubRepo:   githubRepo,
		apiToken:     token,
		containers:   make(map[string]*Container),
		escalations:  newEscalationStoreWithFile(filepath.Join(dataDir(serviceRepo), "escalations.json")),
		claims:       newClaimStoreWithFile(filepath.Join(dataDir(serviceRepo), "claims.json")),
		tasks:        newTaskStoreWithFile(filepath.Join(dataDir(serviceRepo), "tasks.json")),
		feedback:     newFeedbackStoreWithFile(filepath.Join(dataDir(serviceRepo), "feedback.json")),
		costs:          newCostStoreWithFile(filepath.Join(dataDir(serviceRepo), "costs.json"), orchestrationLogDir(serviceRepo)),
		issues:         newIssueStore(filepath.Join(dataDir(serviceRepo), "issues_seen.json")),
		issueWorkflows: newIssueWorkflowStore(),
		registry:       newRegistry(serviceRepo),
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

	// Start matterbridge as child process
	if d.bridgeConf != "" {
		mb, err := startMatterbridge(ctx, d.bridgeConf)
		if err != nil {
			log.Printf("[daemon] matterbridge start failed: %v (continuing without)", err)
		} else if mb != nil {
			defer func() {
				mb.Process.Kill()
				mb.Wait()
			}()
		}
		// Auto-set bridgeURL from config regardless of child process ownership
		if d.bridgeURL == "" {
			if port := parseBridgePort(d.bridgeConf); port != "" {
				d.bridgeURL = "http://localhost:" + port
			} else {
				d.bridgeURL = DefaultBridgeURL
			}
		}
	}

	// Start credential watcher
	go startCredentialWatcher(ctx, d)

	// Start repo watcher (git fetch/pull → sync on .dal/ changes)
	go startRepoWatcher(ctx, d.serviceRepo, func() {
		d.runSync()
	})

	// Start peer health watcher (dalcenter HA)
	go d.startPeerWatcher(ctx)

	// Start GitHub issue watcher
	go d.startIssueWatcher(ctx, d.githubRepo, defaultIssuePollInterval)

	// Start dalroot notifier (issue close → dalroot alert + reminders)
	go d.startDalrootNotifier(ctx, d.githubRepo, defaultNotifyPollInterval)

	// Start leader health watcher (auto-recovery)
	go d.startLeaderWatcher(ctx)

	// Start queue manager (task expiry + concurrency limits)
	go d.startQueueManager(ctx)

	// Start ops watcher (poll all teams, auto-wake empty teams)
	if opsWatcherEnabled() {
		go d.startOpsWatcher(ctx)
	}

	if d.bridgeURL != "" {
		log.Printf("[daemon] matterbridge URL: %s", d.bridgeURL)
	}
	if d.dalbridgeURL != "" {
		log.Printf("[daemon] dalbridge URL: %s (containers)", d.dalbridgeURL)
	}

	// Reconcile existing containers from a previous daemon run
	d.reconcile()

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
	mux.HandleFunc("POST /api/replace/{name}", d.requireAuth(d.handleReplace))
	mux.HandleFunc("POST /api/sync", d.requireAuth(d.handleSync))
	mux.HandleFunc("POST /api/message", d.requireAuth(d.handleMessage))
	mux.HandleFunc("POST /api/activity/{name}", d.requireAuth(d.handleActivity))
	mux.HandleFunc("GET /api/agent-config/{name}", d.handleAgentConfig)
	// Direct task execution (works without bridge)
	mux.HandleFunc("POST /api/task", d.requireAuth(d.handleTask))
	mux.HandleFunc("POST /api/task/start", d.requireAuth(d.handleTaskStart))
	mux.HandleFunc("GET /api/task/{id}", d.handleTaskStatus)
	mux.HandleFunc("POST /api/task/{id}/event", d.requireAuth(d.handleTaskEvent))
	mux.HandleFunc("POST /api/task/{id}/metadata", d.requireAuth(d.handleTaskMetadata))
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
	// GitHub issue tracking
	mux.HandleFunc("GET /api/issues", d.handleIssues)
	// A2A protocol endpoints
	mux.HandleFunc("GET /api/provider-status", d.handleProviderStatus)
	mux.HandleFunc("POST /api/provider-trip", d.handleProviderTrip)

	// Issue workflow — full orchestration endpoints
	mux.HandleFunc("POST /api/issue-workflow", d.requireAuth(d.handleIssueWorkflow))
	mux.HandleFunc("GET /api/issue-workflow/{id}", d.handleIssueWorkflowStatus)
	mux.HandleFunc("GET /api/issue-workflows", d.handleIssueWorkflowList)

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
	// Find leader status
	leaderStatus := "not_configured"
	for _, c := range d.containers {
		if c.Role == "leader" {
			leaderStatus = c.Status
			break
		}
	}
	d.mu.RUnlock()

	// If no leader is running but one is defined, mark as sleeping
	if leaderStatus == "not_configured" {
		dals, _ := localdal.ListDals(d.localdalRoot)
		for _, dal := range dals {
			if dal.Role == "leader" {
				leaderStatus = "sleeping"
				break
			}
		}
	}

	dals, _ := localdal.ListDals(d.localdalRoot)

	respondJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"uptime":        time.Since(d.startTime).Truncate(time.Second).String(),
		"dals_running":  dalsRunning,
		"repo_count":    len(dals),
		"leader_status": leaderStatus,
	})
}

func (d *Daemon) handlePs(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	now := time.Now()
	containers := make([]*Container, 0, len(d.containers))
	for _, c := range d.containers {
		snapshot := *c
		if !snapshot.LastSeenAt.IsZero() {
			snapshot.IdleFor = now.Sub(snapshot.LastSeenAt).Truncate(time.Second).String()
		}
		containers = append(containers, &snapshot)
	}
	json.NewEncoder(w).Encode(containers)
}

func (d *Daemon) markActivity(name string, seenAt time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[name]
	if !ok {
		return false
	}
	c.LastSeenAt = seenAt
	return true
}

func (d *Daemon) handleActivity(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	seenAt := time.Now().UTC()
	if !d.markActivity(name, seenAt) {
		http.Error(w, fmt.Sprintf("dal %q is not awake", name), http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"dal":          name,
		"last_seen_at": seenAt,
	})
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
		"description":  dal.Description,
		"player":       dal.Player,
		"role":         dal.Role,
		"skills":       dal.Skills,
		"status":       status,
		"container_id": containerID,
	})
}

func (d *Daemon) handleWake(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	issueID := r.URL.Query().Get("issue")

	dal, err := localdal.ReadDalCue(d.dalCuePath(name), name)
	if err != nil {
		http.Error(w, fmt.Sprintf("dal %q not found: %v", name, err), 404)
		return
	}

	// Validate member count limit: find the leader's max_members setting
	if dal.Role == "member" || dal.Role == "ops" {
		if err := d.validateMemberLimit(name); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}

	// Prepare issue workspace if --issue is provided
	if issueID != "" {
		members := []string{name}
		if _, err := localdal.PrepareIssue(d.localdalRoot, issueID, members); err != nil {
			log.Printf("[daemon] prepare issue %s: %v (continuing)", issueID, err)
		}
	}

	// Reject wake if this dal is already running (#532)
	d.mu.RLock()
	if existing, ok := d.containers[name]; ok && existing.Status == "running" {
		d.mu.RUnlock()
		http.Error(w, fmt.Sprintf("dal %q is already running (container=%s)", name, existing.ContainerID), http.StatusConflict)
		return
	}
	d.mu.RUnlock()

	instanceName := name

	// Clean any stopped container with the same name (best-effort, #33)
	targetContainerName := dalContainerName(instanceName, dal.UUID)
	if err := cleanStaleContainer(targetContainerName); err != nil {
		log.Printf("[daemon] clean stale container %s: %v (continuing)", targetContainerName, err)
	}

	containerID, warnings, err := dockerRun(d.localdalRoot, d.serviceRepo, instanceName, d.addr, d.bridgeURL, d.dalbridgeURL, dal)
	if err != nil {
		http.Error(w, fmt.Sprintf("wake failed: %v", err), 500)
		return
	}

	// Setup workspace: branch checkout + dependency install
	if setupWarnings := setupWorkspace(containerID, dal, issueID); len(setupWarnings) > 0 {
		warnings = append(warnings, setupWarnings...)
	}

	ws := dal.Workspace
	if ws == "" {
		ws = "shared"
	}
	d.mu.Lock()
	d.containers[instanceName] = &Container{
		DalName:     instanceName,
		UUID:        dal.UUID,
		Player:      dal.Player,
		Role:        dal.Role,
		Description: dal.Description,
		ContainerID: containerID,
		Status:      "running",
		Workspace:   ws,
		Skills:      len(dal.Skills),
		LastSeenAt:  time.Now().UTC(),
	}
	d.mu.Unlock()

	// Persist to registry
	d.registry.Set(dal.UUID, RegistryEntry{
		Name:        instanceName,
		Repo:        d.serviceRepo,
		ContainerID: containerID,
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
		d.mu.Lock()
		delete(d.containers, name)
		d.mu.Unlock()
	}

	log.Printf("[daemon] restart: %s (clean wake)", name)
	d.handleWake(w, r)
}

// handleReplace creates fresh replacement containers for a dal.
// Used when stagnation detection triggers a dal rotation.
// Creates 2 new instances from template, then sleeps the original.
func (d *Daemon) handleReplace(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	issueID := r.URL.Query().Get("issue")

	d.mu.RLock()
	original, exists := d.containers[name]
	d.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("dal %q is not awake", name), 404)
		return
	}

	log.Printf("[daemon] replace triggered for %s (stagnation/rotation)", name)

	// Wake 2 new instances from template
	var newInstances []string
	for i := 0; i < 2; i++ {
		// Build a synthetic wake request
		path := fmt.Sprintf("/api/wake/%s", name)
		if issueID != "" {
			path += "?issue=" + issueID
		}
		wakeReq, _ := http.NewRequest("POST", path, nil)
		wakeReq.SetPathValue("name", name)
		if issueID != "" {
			q := wakeReq.URL.Query()
			q.Set("issue", issueID)
			wakeReq.URL.RawQuery = q.Encode()
		}
		rec := &discardResponseWriter{}
		d.handleWake(rec, wakeReq)
		if rec.code >= 400 {
			log.Printf("[daemon] replace: failed to wake replacement %d for %s", i+1, name)
			continue
		}
		newInstances = append(newInstances, fmt.Sprintf("%s-%d", name, i+2))
	}

	// Sleep the original
	if original.ContainerID != "" {
		dockerStop(original.ContainerID)
		d.mu.Lock()
		delete(d.containers, name)
		d.mu.Unlock()
		log.Printf("[daemon] replace: disposed original %s", name)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"status":        "replaced",
		"dal":           name,
		"new_instances": newInstances,
	})
}

// discardResponseWriter captures status code without writing to real response.
type discardResponseWriter struct {
	code int
}

func (d *discardResponseWriter) Header() http.Header        { return http.Header{} }
func (d *discardResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardResponseWriter) WriteHeader(code int)        { d.code = code }

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
			newID, _, err := dockerRun(d.localdalRoot, d.serviceRepo, name, d.addr, d.bridgeURL, d.dalbridgeURL, dal)
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
				Description: dal.Description,
				ContainerID: newID,
				Status:      "running",
				Skills:      len(dal.Skills),
				LastSeenAt:  time.Now().UTC(),
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

	if d.bridgeURL == "" {
		// Fallback: if bridge is not configured, try direct task execution
		d.mu.RLock()
		_, hasDal := d.containers[req.From]
		d.mu.RUnlock()
		if !hasDal {
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
				log.Printf("[scope] message fallback to direct task — bridge not configured")
				tr := d.tasks.New(c.DalName, req.Message)
				go d.execTaskInContainer(c, tr)
				respondJSON(w, http.StatusAccepted, map[string]string{
					"status":  "task_dispatched",
					"task_id": tr.ID,
					"note":    "bridge unavailable — task dispatched directly to container",
				})
				return
			}
		}
		http.Error(w, "bridge not configured and no running dals", 503)
		return
	}

	dalName := req.From
	if dalName == "" {
		dalName = "dalcenter"
	}
	// Post directly to MM (not via matterbridge) to avoid self-skip
	if err := d.mmPost(req.Message); err != nil {
		// Fallback to bridge
		if err2 := d.bridgePost(req.Message, dalName); err2 != nil {
			http.Error(w, fmt.Sprintf("post failed: mm=%v bridge=%v", err, err2), 500)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status": "sent",
	})
}

// bridgePost sends a message via matterbridge API.
func (d *Daemon) bridgePost(text, username string) error {
	if d.bridgeURL == "" {
		return fmt.Errorf("bridge URL not configured")
	}
	body := fmt.Sprintf(`{"text":%q,"username":%q,"gateway":%q}`, text, username, gatewayName(d.serviceRepo))
	req, _ := http.NewRequest("POST", d.bridgeURL+"/api/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bridge %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// gatewayName returns the matterbridge gateway name for a service repo path.
// It ensures exactly one "dal-" prefix even if the repo basename already starts with "dal-".
func gatewayName(serviceRepo string) string {
	base := filepath.Base(serviceRepo)
	if strings.HasPrefix(base, "dal-") {
		return base
	}
	return "dal-" + base
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

			d.mu.Lock()
			d.containers[instanceName] = &Container{
				DalName:     instanceName,
				UUID:        dal.UUID,
				Player:      dal.Player,
				Role:        dal.Role,
				Description: dal.Description,
				ContainerID: c.ID,
				Status:      "running",
				Skills:      len(dal.Skills),
				LastSeenAt:  time.Now().UTC(),
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

// handleAgentConfig returns bridge connection info for a dal to run its agent loop.
// Called by dalcli run inside the container.
func (d *Daemon) handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, ok := d.agentContainer(name)
	if !ok {
		http.Error(w, "dal not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(d.agentConfigResponse(name, c))
}

func (d *Daemon) agentContainer(name string) (*Container, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	c, ok := d.containers[name]
	return c, ok
}

func (d *Daemon) agentConfigResponse(name string, c *Container) map[string]string {
	resp := map[string]string{
		"dal_name":   c.DalName,
		"uuid":       c.UUID,
		"bridge_url": bridgeURLForContainer(d.bridgeURL),
		"gateway":    gatewayName(d.serviceRepo),
	}

	// Inject team member names so leader can mention them correctly
	d.mu.RLock()
	var teammates []string
	for n, cc := range d.containers {
		if n != name {
			teammates = append(teammates, fmt.Sprintf("%s=%s", n, cc.DalName))
		}
	}
	d.mu.RUnlock()
	if len(teammates) > 0 {
		resp["team_members"] = strings.Join(teammates, ",")
	}
	return resp
}

func (d *Daemon) dalCuePath(name string) string {
	tplRoot := localdal.ResolveTemplateRoot(d.localdalRoot)
	path := fmt.Sprintf("%s/%s/dal.cue", tplRoot, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	// Fallback: look directly in .dal/<name>/dal.cue
	return fmt.Sprintf("%s/%s/dal.cue", d.localdalRoot, name)
}

// validateMemberLimit checks the leader's max_members setting and rejects
// wake requests that would exceed the limit.
func (d *Daemon) validateMemberLimit(name string) error {
	// Find leader profile to read max_members
	dals, err := localdal.ListDals(d.localdalRoot)
	if err != nil {
		return nil // cannot validate, allow
	}

	var maxMembers int
	for _, dal := range dals {
		if dal.Role == "leader" && dal.MaxMembers > 0 {
			maxMembers = dal.MaxMembers
			break
		}
	}
	if maxMembers == 0 {
		return nil // no limit configured
	}

	// Count currently running member containers (excluding the one being waked if it's a re-wake)
	d.mu.RLock()
	var memberCount int
	for n, c := range d.containers {
		if c.Role == "member" || c.Role == "ops" {
			if n != name { // don't count self if re-waking
				memberCount++
			}
		}
	}
	d.mu.RUnlock()

	if memberCount >= maxMembers {
		return fmt.Errorf("member limit reached: %d/%d members running (max_members=%d in leader config)",
			memberCount, maxMembers, maxMembers)
	}
	return nil
}
