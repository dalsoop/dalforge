package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Default cost thresholds (USD).
const (
	defaultDailyThreshold   = 50.0
	defaultMonthlyThreshold = 500.0
)

// Approximate per-token costs by model (USD). Updated as of 2026-03.
var modelCosts = map[string]tokenCost{
	"claude-opus-4-6":  {InputPer1M: 15.0, OutputPer1M: 75.0},
	"claude-sonnet-4":  {InputPer1M: 3.0, OutputPer1M: 15.0},
	"claude-haiku-3-5": {InputPer1M: 0.80, OutputPer1M: 4.0},
	"codex":            {InputPer1M: 3.0, OutputPer1M: 15.0},
}

// tokenCost holds per-million-token prices.
type tokenCost struct {
	InputPer1M  float64
	OutputPer1M float64
}

// TokenUsage represents a single token usage record from a task execution.
type TokenUsage struct {
	TaskID       string    `json:"task_id"`
	Dal          string    `json:"dal"`
	Model        string    `json:"model"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	Timestamp    time.Time `json:"timestamp"`
}

// CostSummary aggregates cost data for a dal or repo.
type CostSummary struct {
	Dal          string  `json:"dal"`
	TotalInput   int64   `json:"total_input_tokens"`
	TotalOutput  int64   `json:"total_output_tokens"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	TaskCount    int     `json:"task_count"`
}

// CostAlert is emitted when a threshold is exceeded.
type CostAlert struct {
	Level      string    `json:"level"`     // "warning", "critical"
	Dal        string    `json:"dal"`       // dal that triggered, or "all"
	Message    string    `json:"message"`
	CostUSD    float64   `json:"cost_usd"`
	Threshold  float64   `json:"threshold"`
	Suggestion string    `json:"suggestion,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// CostTracker monitors API token usage per dal and per repo.
type CostTracker struct {
	mu               sync.RWMutex
	records          []TokenUsage
	dailyThreshold   float64
	monthlyThreshold float64
	alerts           []CostAlert
	logDir           string // orchestration-log directory
}

// NewCostTracker creates a CostTracker. serviceRepo is used to derive the log path.
func NewCostTracker(serviceRepo string) *CostTracker {
	logDir := filepath.Join(stateDir(serviceRepo), "orchestration-log")
	os.MkdirAll(logDir, 0o755)

	ct := &CostTracker{
		records:          make([]TokenUsage, 0),
		alerts:           make([]CostAlert, 0),
		dailyThreshold:   defaultDailyThreshold,
		monthlyThreshold: defaultMonthlyThreshold,
		logDir:           logDir,
	}

	ct.loadRecords()
	return ct
}

// newCostTrackerWithDir creates a CostTracker with an explicit log directory (for testing).
func newCostTrackerWithDir(logDir string) *CostTracker {
	os.MkdirAll(logDir, 0o755)
	return &CostTracker{
		records:          make([]TokenUsage, 0),
		alerts:           make([]CostAlert, 0),
		dailyThreshold:   defaultDailyThreshold,
		monthlyThreshold: defaultMonthlyThreshold,
		logDir:           logDir,
	}
}

// Record adds a token usage entry and checks thresholds.
func (ct *CostTracker) Record(taskID, dal, model string, inputTokens, outputTokens int64) TokenUsage {
	cost := computeCost(model, inputTokens, outputTokens)
	usage := TokenUsage{
		TaskID:       taskID,
		Dal:          dal,
		Model:        model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CostUSD:      cost,
		Timestamp:    time.Now().UTC(),
	}

	ct.mu.Lock()
	ct.records = append(ct.records, usage)
	ct.mu.Unlock()

	ct.persistRecord(usage)
	ct.checkThresholds(dal, model)

	log.Printf("[cost] %s dal=%s model=%s in=%d out=%d cost=$%.4f",
		taskID, dal, model, inputTokens, outputTokens, cost)
	return usage
}

// SummaryByDal returns aggregated cost data grouped by dal.
func (ct *CostTracker) SummaryByDal() map[string]*CostSummary {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	result := make(map[string]*CostSummary)
	for _, r := range ct.records {
		s, ok := result[r.Dal]
		if !ok {
			s = &CostSummary{Dal: r.Dal}
			result[r.Dal] = s
		}
		s.TotalInput += r.InputTokens
		s.TotalOutput += r.OutputTokens
		s.TotalCostUSD += r.CostUSD
		s.TaskCount++
	}
	return result
}

// DailyCost returns total cost for today across all dals.
func (ct *CostTracker) DailyCost() float64 {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	var total float64
	for _, r := range ct.records {
		if !r.Timestamp.Before(today) {
			total += r.CostUSD
		}
	}
	return total
}

// MonthlyCost returns total cost for the current month.
func (ct *CostTracker) MonthlyCost() float64 {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var total float64
	for _, r := range ct.records {
		if !r.Timestamp.Before(monthStart) {
			total += r.CostUSD
		}
	}
	return total
}

// Alerts returns all emitted cost alerts.
func (ct *CostTracker) Alerts() []CostAlert {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	out := make([]CostAlert, len(ct.alerts))
	copy(out, ct.alerts)
	return out
}

// SuggestDowngrade returns a model downgrade suggestion based on current usage.
// If cost exceeds 80% of threshold, it suggests a cheaper model.
func SuggestDowngrade(currentModel string, dailyCost, threshold float64) string {
	if dailyCost < threshold*0.8 {
		return "" // no suggestion needed
	}
	switch currentModel {
	case "claude-opus-4-6":
		return "claude-sonnet-4"
	case "claude-sonnet-4":
		return "claude-haiku-3-5"
	default:
		return "" // already cheapest or unknown
	}
}

// computeCost calculates USD cost for token usage.
func computeCost(model string, inputTokens, outputTokens int64) float64 {
	tc, ok := modelCosts[model]
	if !ok {
		// Fall back to sonnet pricing for unknown models.
		tc = modelCosts["claude-sonnet-4"]
	}
	return float64(inputTokens)/1_000_000*tc.InputPer1M +
		float64(outputTokens)/1_000_000*tc.OutputPer1M
}

// checkThresholds evaluates daily/monthly cost and emits alerts.
func (ct *CostTracker) checkThresholds(dal, model string) {
	daily := ct.DailyCost()
	monthly := ct.MonthlyCost()

	ct.mu.Lock()
	defer ct.mu.Unlock()

	if daily > ct.dailyThreshold {
		suggestion := SuggestDowngrade(model, daily, ct.dailyThreshold)
		alert := CostAlert{
			Level:      "critical",
			Dal:        dal,
			Message:    fmt.Sprintf("daily cost $%.2f exceeds threshold $%.2f", daily, ct.dailyThreshold),
			CostUSD:    daily,
			Threshold:  ct.dailyThreshold,
			Suggestion: suggestion,
			Timestamp:  time.Now().UTC(),
		}
		ct.alerts = append(ct.alerts, alert)
		log.Printf("[cost] ALERT: %s (suggest: %s)", alert.Message, suggestion)

		dispatchWebhook(WebhookEvent{
			Event:     "cost_alert",
			Dal:       dal,
			Task:      alert.Message,
			Timestamp: alert.Timestamp.Format(time.RFC3339),
		})
	} else if daily > ct.dailyThreshold*0.8 {
		suggestion := SuggestDowngrade(model, daily, ct.dailyThreshold)
		alert := CostAlert{
			Level:      "warning",
			Dal:        dal,
			Message:    fmt.Sprintf("daily cost $%.2f approaching threshold $%.2f (80%%)", daily, ct.dailyThreshold),
			CostUSD:    daily,
			Threshold:  ct.dailyThreshold,
			Suggestion: suggestion,
			Timestamp:  time.Now().UTC(),
		}
		ct.alerts = append(ct.alerts, alert)
		log.Printf("[cost] WARNING: %s", alert.Message)
	}

	if monthly > ct.monthlyThreshold {
		alert := CostAlert{
			Level:     "critical",
			Dal:       "all",
			Message:   fmt.Sprintf("monthly cost $%.2f exceeds threshold $%.2f", monthly, ct.monthlyThreshold),
			CostUSD:   monthly,
			Threshold: ct.monthlyThreshold,
			Timestamp: time.Now().UTC(),
		}
		ct.alerts = append(ct.alerts, alert)
		log.Printf("[cost] ALERT: %s", alert.Message)
	}

	// Evict old alerts (keep last 100).
	if len(ct.alerts) > 100 {
		ct.alerts = ct.alerts[len(ct.alerts)-100:]
	}
}

// persistRecord appends a usage record to the orchestration-log as JSONL.
func (ct *CostTracker) persistRecord(u TokenUsage) {
	date := u.Timestamp.Format("2006-01-02")
	path := filepath.Join(ct.logDir, "cost-"+date+".jsonl")

	data, err := json.Marshal(u)
	if err != nil {
		log.Printf("[cost] marshal error: %v", err)
		return
	}
	data = append(data, '\n')

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[cost] write error: %v", err)
		return
	}
	defer f.Close()
	f.Write(data)
}

// loadRecords reads existing JSONL files from the orchestration-log.
func (ct *CostTracker) loadRecords() {
	matches, err := filepath.Glob(filepath.Join(ct.logDir, "cost-*.jsonl"))
	if err != nil || len(matches) == 0 {
		return
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range splitLines(data) {
			if len(line) == 0 {
				continue
			}
			var u TokenUsage
			if err := json.Unmarshal(line, &u); err == nil {
				ct.records = append(ct.records, u)
			}
		}
	}
	log.Printf("[cost] loaded %d historical records", len(ct.records))
}

// splitLines splits byte data into lines.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// handleCostSummary returns per-dal cost summaries.
// GET /api/cost/summary
func (d *Daemon) handleCostSummary(w http.ResponseWriter, r *http.Request) {
	if d.costTracker == nil {
		http.Error(w, "cost tracking not enabled", http.StatusServiceUnavailable)
		return
	}
	respondJSON(w, http.StatusOK, d.costTracker.SummaryByDal())
}

// handleCostAlerts returns cost threshold alerts.
// GET /api/cost/alerts
func (d *Daemon) handleCostAlerts(w http.ResponseWriter, r *http.Request) {
	if d.costTracker == nil {
		http.Error(w, "cost tracking not enabled", http.StatusServiceUnavailable)
		return
	}
	respondJSON(w, http.StatusOK, d.costTracker.Alerts())
}

// handleCostRecord records token usage from a task.
// POST /api/cost/record
func (d *Daemon) handleCostRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if d.costTracker == nil {
		http.Error(w, "cost tracking not enabled", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		TaskID       string `json:"task_id"`
		Dal          string `json:"dal"`
		Model        string `json:"model"`
		InputTokens  int64  `json:"input_tokens"`
		OutputTokens int64  `json:"output_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Dal == "" || req.TaskID == "" {
		http.Error(w, "dal and task_id are required", http.StatusBadRequest)
		return
	}

	usage := d.costTracker.Record(req.TaskID, req.Dal, req.Model, req.InputTokens, req.OutputTokens)
	respondJSON(w, http.StatusOK, usage)
}

// ParseTokenUsage extracts token counts from Claude API JSON output.
// It looks for the standard {"usage":{"input_tokens":N,"output_tokens":N}} shape.
func ParseTokenUsage(output []byte) (inputTokens, outputTokens int64, ok bool) {
	// Try top-level usage field.
	var wrapper struct {
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(output, &wrapper); err == nil &&
		(wrapper.Usage.InputTokens > 0 || wrapper.Usage.OutputTokens > 0) {
		return wrapper.Usage.InputTokens, wrapper.Usage.OutputTokens, true
	}

	// Try result field (some API responses nest under "result").
	var nested struct {
		Result struct {
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &nested); err == nil &&
		(nested.Result.Usage.InputTokens > 0 || nested.Result.Usage.OutputTokens > 0) {
		return nested.Result.Usage.InputTokens, nested.Result.Usage.OutputTokens, true
	}

	return 0, 0, false
}
