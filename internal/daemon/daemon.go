package daemon

import (
	"context"
	"encoding/json"
	"fmt"
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
	URL       string // e.g. http://10.50.0.202:8065
	AdminToken string // PAT with admin access
	TeamName  string // e.g. "prelik"
}

// Daemon is the dalcenter HTTP API server.
type Daemon struct {
	addr         string
	localdalRoot string
	serviceRepo  string // service repo path to mount as /workspace
	mm           *MattermostConfig
	apiToken     string // Bearer token for write endpoints (empty = no auth)
	channelID    string // channel for this project
	containers   map[string]*Container // dal name -> container
	mu           sync.RWMutex
}

// Container tracks a running dal Docker container.
type Container struct {
	DalName     string `json:"dal_name"`
	UUID        string `json:"uuid"`
	Player      string `json:"player"`
	Role        string `json:"role"`
	ContainerID string `json:"container_id"`
	Status      string `json:"status"` // "running", "stopped"
	Skills      int    `json:"skills"`
	BotToken    string `json:"-"` // Mattermost bot token
}

// New creates a daemon.
func New(addr, localdalRoot, serviceRepo string, mm *MattermostConfig) *Daemon {
	token := os.Getenv("DALCENTER_TOKEN")
	if token != "" {
		log.Println("[daemon] API token auth enabled for write endpoints")
	}
	return &Daemon{
		addr:         addr,
		localdalRoot: localdalRoot,
		serviceRepo:  serviceRepo,
		mm:           mm,
		apiToken:     token,
		containers:   make(map[string]*Container),
	}
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
		}
	}

	// Reconcile existing containers from a previous daemon run
	d.reconcile()

	mux := http.NewServeMux()

	// Read-only endpoints — no auth required
	mux.HandleFunc("GET /api/ps", d.handlePs)
	mux.HandleFunc("GET /api/status", d.handleStatus)
	mux.HandleFunc("GET /api/status/{name}", d.handleStatusOne)
	mux.HandleFunc("POST /api/validate", d.handleValidate)
	mux.HandleFunc("GET /api/logs/{name}", d.handleLogs)
	// Write endpoints — require auth when DALCENTER_TOKEN is set
	mux.HandleFunc("POST /api/wake/{name}", d.requireAuth(d.handleWake))
	mux.HandleFunc("POST /api/sleep/{name}", d.requireAuth(d.handleSleep))
	mux.HandleFunc("POST /api/sync", d.requireAuth(d.handleSync))
	mux.HandleFunc("POST /api/message", d.requireAuth(d.handleMessage))
	mux.HandleFunc("GET /api/agent-config/{name}", d.handleAgentConfig)

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
	targetContainerName := containerPrefix + instanceName
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
		botUsername := containerPrefix + dal.Name
		teamID, _, _ := talk.GetTeamAndChannel(d.mm.URL, d.mm.AdminToken, d.mm.TeamName, filepath.Base(d.serviceRepo))
		bot, err := talk.SetupBot(d.mm.URL, d.mm.AdminToken, teamID, d.channelID, botUsername, dal.Name, fmt.Sprintf("dal %s (%s)", dal.Name, dal.Role))
		if err != nil {
			log.Printf("[daemon] mattermost bot setup for %s: %v (continuing without)", name, err)
		} else {
			botToken = bot.Token
			log.Printf("[daemon] mattermost bot: %s (user=%s)", botUsername, bot.UserID[:8])
		}
	}

	d.mu.Lock()
	d.containers[instanceName] = &Container{
		DalName:     instanceName,
		UUID:        dal.UUID,
		Player:      dal.Player,
		Role:        dal.Role,
		ContainerID: containerID,
		Status:      "running",
		Skills:      len(dal.Skills),
		BotToken:    botToken,
	}
	d.mu.Unlock()

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

func (d *Daemon) handleSync(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	running := make(map[string]*Container)
	for k, v := range d.containers {
		if v.Status == "running" {
			running[k] = v
		}
	}
	d.mu.RUnlock()

	if len(running) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"status": "no running dals"})
		return
	}

	var synced []string
	var restarted []string
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
			// Stop old container
			oldID := c.ContainerID
			if err := dockerStop(oldID); err != nil {
				log.Printf("[daemon] sync restart stop %s: %v", name, err)
				continue
			}
			// Start new container
			newID, _, err := dockerRun(d.localdalRoot, d.serviceRepo, name, d.addr, dal)
			if err != nil {
				log.Printf("[daemon] sync restart run %s: %v", name, err)
				// Remove from containers map since old one is stopped
				d.mu.Lock()
				delete(d.containers, name)
				d.mu.Unlock()
				continue
			}
			// Update containers map
			d.mu.Lock()
			d.containers[name] = &Container{
				DalName:     name,
				UUID:        dal.UUID,
				Player:      dal.Player,
				Role:        dal.Role,
				ContainerID: newID,
				Status:      "running",
				Skills:      len(dal.Skills),
				BotToken:    c.BotToken, // preserve bot token
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
	json.NewEncoder(w).Encode(map[string]any{
		"status":    "synced",
		"dals":      synced,
		"restarted": restarted,
	})
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

	logs, err := dockerLogs(c.ContainerID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(logs))
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
		http.Error(w, "mattermost not configured", 503)
		return
	}

	// Find the bot token for the sender
	d.mu.RLock()
	c, ok := d.containers[req.From]
	d.mu.RUnlock()

	token := d.mm.AdminToken // fallback to admin token
	if ok && c.BotToken != "" {
		token = c.BotToken
	}

	// Post message (with optional thread)
	var body string
	if req.ThreadID != "" {
		body = fmt.Sprintf(`{"channel_id":%q,"message":%q,"root_id":%q}`, d.channelID, req.Message, req.ThreadID)
	} else {
		body = fmt.Sprintf(`{"channel_id":%q,"message":%q}`, d.channelID, req.Message)
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
	containers, err := discoverContainers()
	if err != nil {
		log.Printf("[daemon] reconcile: %v", err)
		return
	}
	if len(containers) == 0 {
		return
	}

	var running, stopped int
	for _, c := range containers {
		if !strings.HasPrefix(c.Name, containerPrefix) {
			continue
		}
		instanceName := strings.TrimPrefix(c.Name, containerPrefix)

		if c.Running {
			// Try to read dal.cue — for multi-instance like "dev-2", derive base name
			dalName := instanceName
			dal, err := localdal.ReadDalCue(d.dalCuePath(dalName), dalName)
			if err != nil {
				// Try stripping instance suffix: "dev-2" -> "dev"
				if idx := strings.LastIndex(dalName, "-"); idx > 0 {
					baseName := dalName[:idx]
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
				ContainerID: c.ID,
				Status:      "running",
				Skills:      len(dal.Skills),
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
		"bot_token":  c.BotToken,
		"channel_id": d.channelID,
	}
	if d.mm != nil {
		resp["mm_url"] = d.mm.URL
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (d *Daemon) dalCuePath(name string) string {
	return fmt.Sprintf("%s/%s/dal.cue", d.localdalRoot, name)
}
