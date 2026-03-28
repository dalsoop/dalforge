package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Feedback records the outcome of a completed task for persistent tracking.
type Feedback struct {
	ID         string    `json:"id"`
	Dal        string    `json:"dal"`
	TaskID     string    `json:"task_id"`
	Task       string    `json:"task"`
	Result     string    `json:"result"` // "success", "failure"
	Error      string    `json:"error,omitempty"`
	GitChanges int       `json:"git_changes,omitempty"`
	DurationMs int64     `json:"duration_ms,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// DalStats holds aggregated success/failure counts for a single dal.
type DalStats struct {
	Dal        string  `json:"dal"`
	Total      int     `json:"total"`
	Success    int     `json:"success"`
	Failure    int     `json:"failure"`
	SuccessRate float64 `json:"success_rate"` // 0.0 ~ 1.0
}

type feedbackStore struct {
	mu       sync.RWMutex
	items    []Feedback
	seq      int
	filePath string
}

const maxFeedback = 500

func newFeedbackStore() *feedbackStore {
	return &feedbackStore{items: make([]Feedback, 0)}
}

func newFeedbackStoreWithFile(path string) *feedbackStore {
	s := &feedbackStore{items: make([]Feedback, 0), filePath: path}
	s.load()
	return s
}

func (s *feedbackStore) load() {
	if s.filePath == "" {
		return
	}
	var items []Feedback
	if err := loadJSON(s.filePath, &items); err != nil {
		return
	}
	s.items = items
	for _, f := range items {
		var n int
		fmt.Sscanf(f.ID, "fb-%d", &n)
		if n > s.seq {
			s.seq = n
		}
	}
}

func (s *feedbackStore) save() {
	if s.filePath == "" {
		return
	}
	persistJSON(s.filePath, s.items, nil)
}

func (s *feedbackStore) Add(dal, taskID, task, result, errMsg string, gitChanges int, durationMs int64) Feedback {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++

	if len(task) > 500 {
		task = task[:500]
	}
	if len(errMsg) > 1024 {
		errMsg = errMsg[:1024]
	}

	fb := Feedback{
		ID:         fmt.Sprintf("fb-%04d", s.seq),
		Dal:        dal,
		TaskID:     taskID,
		Task:       task,
		Result:     result,
		Error:      errMsg,
		GitChanges: gitChanges,
		DurationMs: durationMs,
		Timestamp:  time.Now().UTC(),
	}
	s.items = append(s.items, fb)

	if len(s.items) > maxFeedback {
		s.items = s.items[len(s.items)-maxFeedback:]
	}
	s.save()
	return fb
}

func (s *feedbackStore) List(dal string) []Feedback {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Feedback
	for _, f := range s.items {
		if dal == "" || f.Dal == dal {
			result = append(result, f)
		}
	}
	return result
}

func (s *feedbackStore) Stats() []DalStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	counts := make(map[string]*DalStats)
	for _, f := range s.items {
		st, ok := counts[f.Dal]
		if !ok {
			st = &DalStats{Dal: f.Dal}
			counts[f.Dal] = st
		}
		st.Total++
		if f.Result == "success" {
			st.Success++
		} else {
			st.Failure++
		}
	}

	result := make([]DalStats, 0, len(counts))
	for _, st := range counts {
		if st.Total > 0 {
			st.SuccessRate = float64(st.Success) / float64(st.Total)
		}
		result = append(result, *st)
	}
	return result
}

// --- HTTP handlers ---

// POST /api/feedback — record task outcome
func (d *Daemon) handleFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Dal        string `json:"dal"`
		TaskID     string `json:"task_id"`
		Task       string `json:"task"`
		Result     string `json:"result"`
		Error      string `json:"error"`
		GitChanges int    `json:"git_changes"`
		DurationMs int64  `json:"duration_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Dal == "" || req.Result == "" {
		http.Error(w, "dal and result are required", http.StatusBadRequest)
		return
	}
	switch req.Result {
	case "success", "failure":
	default:
		http.Error(w, "result must be success or failure", http.StatusBadRequest)
		return
	}

	fb := d.feedback.Add(req.Dal, req.TaskID, req.Task, req.Result, req.Error, req.GitChanges, req.DurationMs)
	respondJSON(w, http.StatusOK, fb)
}

// GET /api/feedback?dal=dev — list feedback entries
func (d *Daemon) handleFeedbackList(w http.ResponseWriter, r *http.Request) {
	dal := r.URL.Query().Get("dal")
	items := d.feedback.List(dal)
	if items == nil {
		items = []Feedback{}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"feedback": items,
		"count":    len(items),
	})
}

// GET /api/feedback/stats — dal-level success rate aggregation
func (d *Daemon) handleFeedbackStats(w http.ResponseWriter, r *http.Request) {
	stats := d.feedback.Stats()
	if stats == nil {
		stats = []DalStats{}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"stats": stats,
		"count": len(stats),
	})
}
