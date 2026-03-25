package main

import (
	"log"
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // normal — provider is healthy
	CircuitOpen                         // tripped — provider is failing, use fallback
	CircuitHalfOpen                     // testing — trying primary again
)

// CircuitBreaker tracks provider health and triggers fallback.
type CircuitBreaker struct {
	mu           sync.Mutex
	state        CircuitState
	failures     int
	threshold    int           // failures before opening
	cooldown     time.Duration // how long to stay open before half-open
	lastFailure  time.Time
	halfOpenMax  int // max attempts in half-open before re-opening
	halfOpenTries int
}

// NewCircuitBreaker creates a circuit breaker.
// threshold: consecutive failures to trigger open.
// cooldown: wait before trying primary again.
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:       CircuitClosed,
		threshold:   threshold,
		cooldown:    cooldown,
		halfOpenMax: 1,
	}
}

// ShouldFallback returns true if the primary provider should be skipped.
func (cb *CircuitBreaker) ShouldFallback() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return false
	case CircuitOpen:
		if time.Since(cb.lastFailure) > cb.cooldown {
			cb.state = CircuitHalfOpen
			cb.halfOpenTries = 0
			log.Printf("[circuit] open → half-open (cooldown elapsed)")
			return false // try primary once
		}
		return true // still in cooldown
	case CircuitHalfOpen:
		return false // let it try
	}
	return false
}

// RecordSuccess resets the circuit breaker to closed.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == CircuitHalfOpen {
		log.Printf("[circuit] half-open → closed (success)")
	}
	cb.state = CircuitClosed
	cb.failures = 0
	cb.halfOpenTries = 0
}

// RecordFailure increments failure count and potentially opens the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	switch cb.state {
	case CircuitClosed:
		if cb.failures >= cb.threshold {
			cb.state = CircuitOpen
			log.Printf("[circuit] closed → open (failures=%d >= threshold=%d)", cb.failures, cb.threshold)
		}
	case CircuitHalfOpen:
		cb.halfOpenTries++
		if cb.halfOpenTries >= cb.halfOpenMax {
			cb.state = CircuitOpen
			log.Printf("[circuit] half-open → open (half-open failed)")
		}
	}
}

// State returns the current state as a string.
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	}
	return "unknown"
}
