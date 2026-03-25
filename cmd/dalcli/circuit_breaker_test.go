package main

import (
	"testing"
	"time"
)

func TestCircuitBreaker_StartsClosedT(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	if cb.State() != "closed" {
		t.Errorf("initial state = %q, want closed", cb.State())
	}
	if cb.ShouldFallback() {
		t.Error("closed circuit should not fallback")
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != "closed" {
		t.Errorf("2 failures: state = %q, want closed", cb.State())
	}

	cb.RecordFailure()
	if cb.State() != "open" {
		t.Errorf("3 failures: state = %q, want open", cb.State())
	}
	if !cb.ShouldFallback() {
		t.Error("open circuit should fallback")
	}
}

func TestCircuitBreaker_SuccessResets(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess()

	if cb.State() != "closed" {
		t.Errorf("after success: state = %q, want closed", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenAfterCooldown(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != "open" {
		t.Fatal("should be open")
	}

	time.Sleep(60 * time.Millisecond)

	if cb.ShouldFallback() {
		t.Error("after cooldown, should try primary (half-open)")
	}
	if cb.State() != "half-open" {
		t.Errorf("state = %q, want half-open", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenSuccess(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	cb.ShouldFallback() // triggers half-open
	cb.RecordSuccess()

	if cb.State() != "closed" {
		t.Errorf("half-open + success: state = %q, want closed", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	cb.ShouldFallback() // triggers half-open
	cb.RecordFailure()

	if cb.State() != "open" {
		t.Errorf("half-open + failure: state = %q, want open", cb.State())
	}
}

func TestDetectFallback_Claude(t *testing.T) {
	// codex may or may not be available in test env
	fb := detectFallback("claude")
	// Just verify it doesn't panic and returns string
	_ = fb
}

func TestDetectFallback_Unknown(t *testing.T) {
	fb := detectFallback("gemini")
	if fb != "" {
		t.Errorf("unknown player should have no fallback, got %q", fb)
	}
}

func TestIsRetryable_RateLimit(t *testing.T) {
	if !isRetryable("Error: rate limit exceeded") {
		t.Error("should detect rate limit")
	}
	if !isRetryable("429 Too Many Requests") {
		t.Error("should detect 429")
	}
	if isRetryable("normal error") {
		t.Error("should not retry normal error")
	}
}
