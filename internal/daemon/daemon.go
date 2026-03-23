package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/dalsoop/dalcenter/internal/localdal"
)

// Daemon is the dalcenter HTTP API server.
type Daemon struct {
	addr         string
	localdalRoot string
	serviceRepo  string // service repo path to mount as /workspace
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
}

// New creates a daemon.
func New(addr, localdalRoot, serviceRepo string) *Daemon {
	return &Daemon{
		addr:         addr,
		localdalRoot: localdalRoot,
		serviceRepo:  serviceRepo,
		containers:   make(map[string]*Container),
	}
}

// Run starts the HTTP server.
func (d *Daemon) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/ps", d.handlePs)
	mux.HandleFunc("GET /api/status", d.handleStatus)
	mux.HandleFunc("GET /api/status/{name}", d.handleStatusOne)
	mux.HandleFunc("POST /api/wake/{name}", d.handleWake)
	mux.HandleFunc("POST /api/sleep/{name}", d.handleSleep)
	mux.HandleFunc("POST /api/sync", d.handleSync)
	mux.HandleFunc("POST /api/validate", d.handleValidate)
	mux.HandleFunc("GET /api/logs/{name}", d.handleLogs)

	srv := &http.Server{Addr: d.addr, Handler: mux}
	log.Printf("[daemon] listening on %s", d.addr)

	go func() {
		<-ctx.Done()
		srv.Close()
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

	d.mu.Lock()
	if c, ok := d.containers[name]; ok && c.Status == "running" {
		d.mu.Unlock()
		http.Error(w, fmt.Sprintf("dal %q is already awake", name), 409)
		return
	}
	d.mu.Unlock()

	containerID, err := dockerRun(d.localdalRoot, d.serviceRepo, dal)
	if err != nil {
		http.Error(w, fmt.Sprintf("wake failed: %v", err), 500)
		return
	}

	d.mu.Lock()
	d.containers[name] = &Container{
		DalName:     dal.Name,
		UUID:        dal.UUID,
		Player:      dal.Player,
		Role:        dal.Role,
		ContainerID: containerID,
		Status:      "running",
		Skills:      len(dal.Skills),
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
	log.Printf("[daemon] wake: %s (uuid=%s, container=%s)", name, uid, cid)
	json.NewEncoder(w).Encode(map[string]string{
		"status":       "awake",
		"dal":          name,
		"container_id": containerID,
	})
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
	for name, c := range running {
		dal, err := localdal.ReadDalCue(d.dalCuePath(name), name)
		if err != nil {
			log.Printf("[daemon] sync skip %s: %v", name, err)
			continue
		}
		if err := dockerSync(d.localdalRoot, c.ContainerID, dal); err != nil {
			log.Printf("[daemon] sync error %s: %v", name, err)
			continue
		}
		synced = append(synced, name)
	}

	log.Printf("[daemon] sync: %v", synced)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "synced",
		"dals":   synced,
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

func (d *Daemon) dalCuePath(name string) string {
	return fmt.Sprintf("%s/%s/dal.cue", d.localdalRoot, name)
}
