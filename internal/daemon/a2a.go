package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/dalsoop/dalcenter/internal/localdal"
)

// A2A Protocol types (Google Agent-to-Agent Protocol v0.2)
// Implemented inline to minimize external dependencies.

// a2aAgentCard is the Agent Card served at GET /.well-known/agent-card.json.
// It describes this dalcenter instance's capabilities for A2A discovery.
type a2aAgentCard struct {
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	URL               string            `json:"url"`
	Version           string            `json:"version"`
	ProtocolVersion   string            `json:"protocolVersion"`
	Provider          *a2aProvider      `json:"provider,omitempty"`
	Capabilities      a2aCapabilities   `json:"capabilities"`
	Skills            []a2aSkill        `json:"skills"`
	DefaultInputModes  []string         `json:"defaultInputModes"`
	DefaultOutputModes []string         `json:"defaultOutputModes"`
}

type a2aProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url,omitempty"`
}

type a2aCapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
}

type a2aSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// JSON-RPC 2.0 types

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string       `json:"jsonrpc"`
	ID      any          `json:"id"`
	Result  any          `json:"result,omitempty"`
	Error   *jsonRPCErr  `json:"error,omitempty"`
}

type jsonRPCErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// A2A Task types

type a2aTask struct {
	ID       string       `json:"id"`
	Status   a2aTaskStatus `json:"status"`
	Messages []a2aMessage `json:"messages,omitempty"`
	Artifacts []a2aArtifact `json:"artifacts,omitempty"`
}

type a2aTaskStatus struct {
	State   string `json:"state"` // "submitted", "working", "completed", "failed", "canceled"
	Message string `json:"message,omitempty"`
}

type a2aMessage struct {
	Role  string     `json:"role"` // "user" or "agent"
	Parts []a2aPart  `json:"parts"`
}

type a2aPart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text,omitempty"`
}

type a2aArtifact struct {
	Name  string    `json:"name,omitempty"`
	Parts []a2aPart `json:"parts"`
}

// handleAgentCard serves GET /.well-known/agent-card.json
func (d *Daemon) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	repoName := filepath.Base(d.serviceRepo)
	if repoName == "" || repoName == "." {
		repoName = "dalcenter"
	}

	// Build skills from registered dals
	dals, _ := localdal.ListDals(d.localdalRoot)
	var skills []a2aSkill
	for _, dal := range dals {
		skills = append(skills, a2aSkill{
			ID:          dal.UUID,
			Name:        dal.Name,
			Description: fmt.Sprintf("%s (%s, %s)", dal.Name, dal.Player, dal.Role),
			Tags:        []string{dal.Player, dal.Role},
		})
	}

	// Determine base URL from env or request
	baseURL := os.Getenv("DALCENTER_BASE_URL")
	if baseURL == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		baseURL = fmt.Sprintf("%s://%s", scheme, r.Host)
	}

	card := a2aAgentCard{
		Name:            repoName + "-dalcenter",
		Description:     fmt.Sprintf("dalcenter agent managing %d dals for %s", len(dals), repoName),
		URL:             baseURL + "/rpc",
		Version:         "0.1.0",
		ProtocolVersion: "0.2.0",
		Provider: &a2aProvider{
			Organization: "dalsoop",
		},
		Capabilities: a2aCapabilities{
			Streaming:         false,
			PushNotifications: false,
		},
		Skills:             skills,
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(card)
}

// handleRPC handles POST /rpc — JSON-RPC 2.0 dispatcher for A2A protocol.
func (d *Daemon) handleRPC(w http.ResponseWriter, r *http.Request) {
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, -32600, "invalid request: jsonrpc must be \"2.0\"")
		return
	}

	switch req.Method {
	case "tasks/send":
		d.rpcTasksSend(w, req)
	case "tasks/get":
		d.rpcTasksGet(w, req)
	case "tasks/cancel":
		d.rpcTasksCancel(w, req)
	default:
		writeRPCError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// tasks/send — submit a task to a dal
func (d *Daemon) rpcTasksSend(w http.ResponseWriter, req jsonRPCRequest) {
	var params struct {
		ID      string       `json:"id"`
		Message a2aMessage   `json:"message"`
		Dal     string       `json:"dal,omitempty"` // extension: target dal name
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	// Extract text from message parts
	var taskText string
	for _, p := range params.Message.Parts {
		if p.Type == "text" {
			taskText += p.Text
		}
	}
	if taskText == "" {
		writeRPCError(w, req.ID, -32602, "message must contain at least one text part")
		return
	}

	// Resolve target dal
	dalName := params.Dal
	if dalName == "" {
		// Default: first running container (prefer leader)
		d.mu.RLock()
		for name, c := range d.containers {
			if dalName == "" || c.Role == "leader" {
				dalName = name
			}
		}
		d.mu.RUnlock()
	}

	d.mu.RLock()
	c, ok := d.containers[dalName]
	d.mu.RUnlock()
	if !ok {
		writeRPCError(w, req.ID, -32002, fmt.Sprintf("dal %q is not awake", dalName))
		return
	}

	// Create internal task and execute async
	tr := d.tasks.New(c.DalName, taskText)

	// Use caller-provided ID if given
	taskID := tr.ID
	if params.ID != "" {
		taskID = params.ID
	}

	go func() {
		d.execTaskInContainer(c, tr)
		log.Printf("[a2a] tasks/send %s completed (status=%s)", taskID, tr.Status)
	}()

	task := a2aTask{
		ID: taskID,
		Status: a2aTaskStatus{
			State:   "submitted",
			Message: fmt.Sprintf("task dispatched to %s", dalName),
		},
		Messages: []a2aMessage{params.Message},
	}

	writeRPCResult(w, req.ID, task)
}

// tasks/get — poll task status
func (d *Daemon) rpcTasksGet(w http.ResponseWriter, req jsonRPCRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	tr := d.tasks.Get(params.ID)
	if tr == nil {
		writeRPCError(w, req.ID, -32001, fmt.Sprintf("task %q not found", params.ID))
		return
	}

	task := taskResultToA2A(tr)
	writeRPCResult(w, req.ID, task)
}

// tasks/cancel — cancel a running task
func (d *Daemon) rpcTasksCancel(w http.ResponseWriter, req jsonRPCRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, -32602, "invalid params")
		return
	}

	tr := d.tasks.Get(params.ID)
	if tr == nil {
		writeRPCError(w, req.ID, -32001, fmt.Sprintf("task %q not found", params.ID))
		return
	}

	if tr.Status != "running" {
		writeRPCError(w, req.ID, -32003, fmt.Sprintf("task %q is not running (status=%s)", params.ID, tr.Status))
		return
	}

	// Mark as canceled (best-effort — docker exec cannot be interrupted gracefully)
	now := time.Now().UTC()
	tr.Status = "failed"
	tr.Error = "canceled via A2A"
	tr.DoneAt = &now

	task := a2aTask{
		ID: tr.ID,
		Status: a2aTaskStatus{
			State:   "canceled",
			Message: "task canceled",
		},
	}
	writeRPCResult(w, req.ID, task)
}

// taskResultToA2A converts internal taskResult to A2A task representation.
func taskResultToA2A(tr *taskResult) a2aTask {
	stateMap := map[string]string{
		"running": "working",
		"done":    "completed",
		"failed":  "failed",
	}
	state := stateMap[tr.Status]
	if state == "" {
		state = tr.Status
	}

	task := a2aTask{
		ID: tr.ID,
		Status: a2aTaskStatus{
			State:   state,
			Message: tr.Error,
		},
		Messages: []a2aMessage{
			{Role: "user", Parts: []a2aPart{{Type: "text", Text: tr.Task}}},
		},
	}

	if tr.Output != "" {
		task.Artifacts = []a2aArtifact{
			{
				Name:  "output",
				Parts: []a2aPart{{Type: "text", Text: tr.Output}},
			},
		}
	}

	return task
}

func writeRPCResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCErr{Code: code, Message: message},
	})
}
