package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// DalEntry represents a registered dal in the registry.
type DalEntry struct {
	Name         string    `json:"name"`
	IP           string    `json:"ip"`
	Port         int       `json:"port"`
	Role         string    `json:"role"`
	Status       string    `json:"status"` // "online", "offline"
	RegisteredAt time.Time `json:"registered_at"`
	LastSeen     time.Time `json:"last_seen"`
}

// Registry holds registered dals.
type Registry struct {
	dals map[string]DalEntry
	mu     sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{dals: make(map[string]DalEntry)}
}

func (r *Registry) Register(entry DalEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry.RegisteredAt = time.Now()
	entry.LastSeen = time.Now()
	entry.Status = "online"
	r.dals[entry.Name] = entry
	log.Printf("[serve] registered dal: %s (%s:%d)", entry.Name, entry.IP, entry.Port)
}

func (r *Registry) Deregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.dals, name)
	log.Printf("[serve] deregistered dal: %s", name)
}

func (r *Registry) Get(name string) (DalEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.dals[name]
	return e, ok
}

func (r *Registry) List() []DalEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []DalEntry
	for _, e := range r.dals {
		list = append(list, e)
	}
	return list
}

// Server is the dalcenter serve API server.
type Server struct {
	registry *Registry
	port     int
}

func NewServer(port int) *Server {
	return &Server{
		registry: NewRegistry(),
		port:     port,
	}
}

// Run starts the API server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/dals", s.handleList)
	mux.HandleFunc("GET /api/dals/{name}", s.handleGet)
	mux.HandleFunc("POST /api/dals/register", s.handleRegister)
	mux.HandleFunc("DELETE /api/dals/{name}", s.handleDeregister)
	mux.HandleFunc("POST /api/dals/{name}/hook", s.handleProxy)

	srv := &http.Server{Addr: fmt.Sprintf(":%d", s.port), Handler: mux}
	log.Printf("[serve] listening on :%d", s.port)

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.registry.List())
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entry, ok := s.registry.Get(name)
	if !ok {
		http.Error(w, fmt.Sprintf("dal %q not found", name), 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entry)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var entry DalEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if entry.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	s.registry.Register(entry)
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(entry)
}

func (s *Server) handleDeregister(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.registry.Deregister(name)
	w.WriteHeader(204)
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entry, ok := s.registry.Get(name)
	if !ok {
		http.Error(w, fmt.Sprintf("dal %q not found", name), 404)
		return
	}

	// Forward request to dal's hook server
	targetURL := fmt.Sprintf("http://%s:%d/hook", entry.IP, entry.Port)
	proxyReq, _ := http.NewRequestWithContext(r.Context(), "POST", targetURL, r.Body)
	proxyReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("proxy to %s failed: %v", name, err), 502)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}
