package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaimStore_Add(t *testing.T) {
	s := newClaimStore()
	c := s.Add("dev", ClaimBug, "cargo not found", "command not found: cargo", "build task")
	if c.ID != "claim-0001" {
		t.Errorf("expected claim-0001, got %s", c.ID)
	}
	if c.Status != "open" {
		t.Errorf("expected open, got %s", c.Status)
	}
	if c.Type != ClaimBug {
		t.Errorf("expected bug, got %s", c.Type)
	}
}

func TestClaimStore_List(t *testing.T) {
	s := newClaimStore()
	s.Add("dev", ClaimBug, "bug1", "", "")
	s.Add("leader", ClaimImprovement, "idea1", "", "")

	all := s.List("")
	if len(all) != 2 {
		t.Errorf("expected 2, got %d", len(all))
	}

	open := s.List("open")
	if len(open) != 2 {
		t.Errorf("expected 2 open, got %d", len(open))
	}
}

func TestClaimStore_Respond(t *testing.T) {
	s := newClaimStore()
	c := s.Add("dev", ClaimBlocked, "need auth", "", "")
	if !s.Respond(c.ID, "resolved", "GH_TOKEN added") {
		t.Error("respond should return true")
	}
	got := s.Get(c.ID)
	if got.Status != "resolved" {
		t.Errorf("expected resolved, got %s", got.Status)
	}
	if got.Response != "GH_TOKEN added" {
		t.Errorf("expected response text, got %q", got.Response)
	}
}

func TestClaimStore_RespondNotFound(t *testing.T) {
	s := newClaimStore()
	if s.Respond("claim-9999", "resolved", "") {
		t.Error("should return false for missing claim")
	}
}

func TestClaimStore_FilterByStatus(t *testing.T) {
	s := newClaimStore()
	s.Add("dev", ClaimBug, "bug1", "", "")
	c2 := s.Add("dev", ClaimBug, "bug2", "", "")
	s.Respond(c2.ID, "resolved", "fixed")

	open := s.List("open")
	if len(open) != 1 {
		t.Errorf("expected 1 open, got %d", len(open))
	}
	resolved := s.List("resolved")
	if len(resolved) != 1 {
		t.Errorf("expected 1 resolved, got %d", len(resolved))
	}
}

func TestHandleClaim_Post(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	body := `{"dal":"dev","type":"bug","title":"cargo missing","detail":"command not found"}`
	req := httptest.NewRequest("POST", "/api/claim", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleClaim(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var claim Claim
	json.NewDecoder(w.Body).Decode(&claim)
	if claim.ID != "claim-0001" {
		t.Errorf("expected claim-0001, got %s", claim.ID)
	}
}

func TestHandleClaims_Empty(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	req := httptest.NewRequest("GET", "/api/claims", nil)
	w := httptest.NewRecorder()
	d.handleClaims(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleClaim_MissingFields(t *testing.T) {
	d := New(":0", "/tmp/test", t.TempDir(), nil)
	body := `{"dal":"","title":""}`
	req := httptest.NewRequest("POST", "/api/claim", strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleClaim(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestClaimStore_Eviction(t *testing.T) {
	s := newClaimStore()
	for i := 0; i < 210; i++ {
		s.Add("dev", ClaimBug, "bug", "", "")
	}
	all := s.List("")
	if len(all) > maxClaims {
		t.Errorf("expected max %d, got %d", maxClaims, len(all))
	}
}
