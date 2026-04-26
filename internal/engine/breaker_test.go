package engine

import (
	"testing"
	"time"
)

func TestCircuitBreakerStartsClosed(t *testing.T) {
	b := NewCircuitBreaker("phase")
	if b.State() != BreakerClosed {
		t.Errorf("initial state = %v, want closed", b.State())
	}
	if !b.Allow() {
		t.Error("closed breaker should allow")
	}
}

func TestCircuitBreakerOpensAfter3Failures(t *testing.T) {
	b := NewCircuitBreaker("phase")
	b.RecordFailure()
	if b.State() != BreakerClosed {
		t.Error("after 1 failure should still be closed")
	}
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != BreakerOpen {
		t.Errorf("after 3 failures expected open, got %v", b.State())
	}
	if b.Allow() {
		t.Error("open breaker should deny")
	}
}

func TestCircuitBreakerHalfOpenAfterCooldown(t *testing.T) {
	b := NewCircuitBreaker("phase")
	for i := 0; i < 3; i++ {
		b.RecordFailure()
	}
	// Cooldown for 3 failures is 1m. Manually back-date openedAt so we
	// don't sleep that long.
	b.mu.Lock()
	b.openedAt = time.Now().Add(-2 * time.Minute)
	b.mu.Unlock()

	if !b.Allow() {
		t.Error("after cooldown, expected half_open transition + allow")
	}
	if b.State() != BreakerHalfOpen {
		t.Errorf("expected half_open, got %v", b.State())
	}
}

func TestCircuitBreakerHalfOpenSuccessClosesIt(t *testing.T) {
	b := NewCircuitBreaker("phase")
	for i := 0; i < 3; i++ {
		b.RecordFailure()
	}
	b.mu.Lock()
	b.openedAt = time.Now().Add(-2 * time.Minute)
	b.state = BreakerHalfOpen
	b.mu.Unlock()

	b.RecordSuccess()
	if b.State() != BreakerClosed {
		t.Errorf("expected closed after half_open success, got %v", b.State())
	}
}

func TestCircuitBreakerHalfOpenFailureReopens(t *testing.T) {
	b := NewCircuitBreaker("phase")
	for i := 0; i < 3; i++ {
		b.RecordFailure()
	}
	b.mu.Lock()
	b.state = BreakerHalfOpen
	b.mu.Unlock()

	b.RecordFailure()
	if b.State() != BreakerOpen {
		t.Errorf("half_open + failure should reopen, got %v", b.State())
	}
}

func TestCircuitBreakerCooldownExponentCappedAt20(t *testing.T) {
	// Critical: math.Pow(2, 1024) is +Inf; the exponent cap at 20 prevents
	// overflow and capped at 1h means cooldown stays bounded.
	b := NewCircuitBreaker("phase")
	for i := 0; i < 100; i++ {
		b.RecordFailure()
	}
	b.mu.Lock()
	cd := b.cooldownLocked()
	b.mu.Unlock()

	if cd > time.Hour {
		t.Errorf("cooldown %v exceeds 1h cap", cd)
	}
	if cd <= 0 {
		t.Errorf("cooldown %v non-positive", cd)
	}
}
