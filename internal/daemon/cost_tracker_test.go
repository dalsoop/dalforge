package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeCost_Opus(t *testing.T) {
	cost := computeCost("claude-opus-4-6", 1_000_000, 1_000_000)
	want := 15.0 + 75.0 // $15 input + $75 output
	if cost != want {
		t.Errorf("opus cost: got $%.2f, want $%.2f", cost, want)
	}
}

func TestComputeCost_Sonnet(t *testing.T) {
	cost := computeCost("claude-sonnet-4", 1_000_000, 1_000_000)
	want := 3.0 + 15.0
	if cost != want {
		t.Errorf("sonnet cost: got $%.2f, want $%.2f", cost, want)
	}
}

func TestComputeCost_UnknownModel(t *testing.T) {
	// Unknown models fall back to sonnet pricing.
	cost := computeCost("unknown-model", 1_000_000, 1_000_000)
	want := 3.0 + 15.0
	if cost != want {
		t.Errorf("unknown model cost: got $%.2f, want $%.2f", cost, want)
	}
}

func TestComputeCost_SmallUsage(t *testing.T) {
	cost := computeCost("claude-haiku-3-5", 1000, 500)
	want := float64(1000)/1_000_000*0.80 + float64(500)/1_000_000*4.0
	if cost != want {
		t.Errorf("small usage cost: got $%.6f, want $%.6f", cost, want)
	}
}

func TestCostTracker_Record(t *testing.T) {
	dir := t.TempDir()
	ct := newCostTrackerWithDir(dir)

	usage := ct.Record("task-0001", "dev", "claude-sonnet-4", 50000, 10000)
	if usage.TaskID != "task-0001" {
		t.Errorf("expected task-0001, got %s", usage.TaskID)
	}
	if usage.CostUSD <= 0 {
		t.Error("expected positive cost")
	}

	// Check the JSONL file was written.
	matches, _ := filepath.Glob(filepath.Join(dir, "cost-*.jsonl"))
	if len(matches) == 0 {
		t.Fatal("expected cost JSONL file in orchestration-log")
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "task-0001") {
		t.Error("JSONL file should contain task-0001")
	}
}

func TestCostTracker_SummaryByDal(t *testing.T) {
	dir := t.TempDir()
	ct := newCostTrackerWithDir(dir)

	ct.Record("t1", "dev", "claude-sonnet-4", 100000, 50000)
	ct.Record("t2", "dev", "claude-sonnet-4", 200000, 100000)
	ct.Record("t3", "reviewer", "claude-sonnet-4", 50000, 20000)

	summary := ct.SummaryByDal()
	if len(summary) != 2 {
		t.Fatalf("expected 2 dals, got %d", len(summary))
	}
	devSummary := summary["dev"]
	if devSummary.TaskCount != 2 {
		t.Errorf("dev task count: got %d, want 2", devSummary.TaskCount)
	}
	if devSummary.TotalInput != 300000 {
		t.Errorf("dev total input: got %d, want 300000", devSummary.TotalInput)
	}
}

func TestCostTracker_DailyCost(t *testing.T) {
	dir := t.TempDir()
	ct := newCostTrackerWithDir(dir)

	ct.Record("t1", "dev", "claude-sonnet-4", 1_000_000, 500_000)
	daily := ct.DailyCost()
	if daily <= 0 {
		t.Error("expected positive daily cost")
	}
}

func TestCostTracker_ThresholdAlert(t *testing.T) {
	dir := t.TempDir()
	ct := newCostTrackerWithDir(dir)
	ct.dailyThreshold = 1.0 // Very low threshold for testing.

	// Record enough to exceed threshold.
	ct.Record("t1", "dev", "claude-opus-4-6", 1_000_000, 1_000_000)
	alerts := ct.Alerts()
	if len(alerts) == 0 {
		t.Fatal("expected at least one alert")
	}
	found := false
	for _, a := range alerts {
		if a.Level == "critical" {
			found = true
		}
	}
	if !found {
		t.Error("expected a critical alert")
	}
}

func TestCostTracker_WarningAlert(t *testing.T) {
	dir := t.TempDir()
	ct := newCostTrackerWithDir(dir)
	ct.dailyThreshold = 100.0

	// Record enough to exceed 80% but not 100% of threshold ($80-$99).
	// Opus: 1M tokens = $15 input + $75 output = $90
	ct.Record("t1", "dev", "claude-opus-4-6", 1_000_000, 1_000_000)
	alerts := ct.Alerts()
	found := false
	for _, a := range alerts {
		if a.Level == "warning" {
			found = true
		}
	}
	if !found {
		t.Error("expected a warning alert at 80% threshold")
	}
}

func TestSuggestDowngrade(t *testing.T) {
	tests := []struct {
		model     string
		dailyCost float64
		threshold float64
		want      string
	}{
		{"claude-opus-4-6", 45.0, 50.0, "claude-sonnet-4"},
		{"claude-sonnet-4", 45.0, 50.0, "claude-haiku-3-5"},
		{"claude-haiku-3-5", 45.0, 50.0, ""},
		{"claude-opus-4-6", 10.0, 50.0, ""}, // Below 80%, no suggestion.
	}
	for _, tc := range tests {
		got := SuggestDowngrade(tc.model, tc.dailyCost, tc.threshold)
		if got != tc.want {
			t.Errorf("SuggestDowngrade(%s, %.1f, %.1f) = %q, want %q",
				tc.model, tc.dailyCost, tc.threshold, got, tc.want)
		}
	}
}

func TestParseTokenUsage_TopLevel(t *testing.T) {
	raw := `{"usage":{"input_tokens":1500,"output_tokens":300}}`
	in, out, ok := ParseTokenUsage([]byte(raw))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if in != 1500 || out != 300 {
		t.Errorf("got in=%d out=%d, want 1500/300", in, out)
	}
}

func TestParseTokenUsage_Nested(t *testing.T) {
	raw := `{"result":{"usage":{"input_tokens":2000,"output_tokens":500}}}`
	in, out, ok := ParseTokenUsage([]byte(raw))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if in != 2000 || out != 500 {
		t.Errorf("got in=%d out=%d, want 2000/500", in, out)
	}
}

func TestParseTokenUsage_Invalid(t *testing.T) {
	raw := `{"message":"hello"}`
	_, _, ok := ParseTokenUsage([]byte(raw))
	if ok {
		t.Error("expected ok=false for missing usage")
	}
}

func TestParseTokenUsage_BadJSON(t *testing.T) {
	_, _, ok := ParseTokenUsage([]byte(`not json`))
	if ok {
		t.Error("expected ok=false for bad json")
	}
}

func TestCostTracker_LoadRecords(t *testing.T) {
	dir := t.TempDir()
	ct := newCostTrackerWithDir(dir)

	// Record some data, then create a new tracker from same dir to test loading.
	ct.Record("t1", "dev", "claude-sonnet-4", 100000, 50000)
	ct.Record("t2", "dev", "claude-sonnet-4", 200000, 100000)

	ct2 := newCostTrackerWithDir(dir)
	ct2.loadRecords()
	summary := ct2.SummaryByDal()
	devSummary := summary["dev"]
	if devSummary == nil {
		t.Fatal("expected dev summary after loading")
	}
	if devSummary.TaskCount != 2 {
		t.Errorf("loaded task count: got %d, want 2", devSummary.TaskCount)
	}
}

func TestSplitLines(t *testing.T) {
	data := []byte("line1\nline2\nline3\n")
	lines := splitLines(data)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
}

func TestSplitLines_NoTrailingNewline(t *testing.T) {
	data := []byte("a\nb")
	lines := splitLines(data)
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
}

func TestHandleCostSummary_NoTracker(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	d.costTracker = nil // explicitly disable
	req := httptest.NewRequest("GET", "/api/cost/summary", nil)
	w := httptest.NewRecorder()
	d.handleCostSummary(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestHandleCostSummary_Empty(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	req := httptest.NewRequest("GET", "/api/cost/summary", nil)
	w := httptest.NewRecorder()
	d.handleCostSummary(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleCostAlerts_Empty(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	req := httptest.NewRequest("GET", "/api/cost/alerts", nil)
	w := httptest.NewRecorder()
	d.handleCostAlerts(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleCostRecord_MissingFields(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	body := `{"dal":"","task_id":""}`
	req := httptest.NewRequest("POST", "/api/cost/record", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleCostRecord(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleCostRecord_Success(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	body := `{"task_id":"t1","dal":"dev","model":"claude-sonnet-4","input_tokens":1000,"output_tokens":500}`
	req := httptest.NewRequest("POST", "/api/cost/record", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleCostRecord(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var usage TokenUsage
	json.NewDecoder(w.Body).Decode(&usage)
	if usage.TaskID != "t1" {
		t.Errorf("expected task_id=t1, got %s", usage.TaskID)
	}
	if usage.CostUSD <= 0 {
		t.Error("expected positive cost")
	}
}

func TestCostTracker_AlertEviction(t *testing.T) {
	dir := t.TempDir()
	ct := newCostTrackerWithDir(dir)
	ct.dailyThreshold = 0.0001 // Trigger alerts on every record.

	for i := 0; i < 120; i++ {
		ct.Record("t", "dev", "claude-sonnet-4", 1000, 1000)
	}
	alerts := ct.Alerts()
	if len(alerts) > 100 {
		t.Errorf("expected <=100 alerts after eviction, got %d", len(alerts))
	}
}
