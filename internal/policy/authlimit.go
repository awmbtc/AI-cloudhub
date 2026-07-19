package policy

import (
	"sync"
	"time"
)

// AuthLimiter rate-limits unauthenticated auth endpoints (login/register)
// by client key (typically IP). Uses a token bucket with a small burst.
type AuthLimiter struct {
	inner *Limiter
}

// NewAuthLimiter creates an auth-endpoint limiter.
// Default: 10 req/min with burst 5 when rate/burst are zero.
func NewAuthLimiter(perMinute, burst float64) *AuthLimiter {
	if perMinute <= 0 {
		perMinute = 10
	}
	if burst <= 0 {
		burst = 5
	}
	// Convert per-minute to per-second for the existing bucket.
	return &AuthLimiter{inner: NewLimiter(perMinute/60.0, burst)}
}

// Allow reports whether the key may attempt login/register.
func (a *AuthLimiter) Allow(key string) bool {
	if a == nil || a.inner == nil {
		return true
	}
	if key == "" {
		key = "unknown"
	}
	return a.inner.Allow(key)
}

// FailureTracker locks out a key after consecutive failures within a window.
// Successful login should call Clear.
type FailureTracker struct {
	mu       sync.Mutex
	maxFail  int
	window   time.Duration
	failures map[string]*failEntry
}

type failEntry struct {
	count int
	first time.Time
}

// NewFailureTracker creates a lockout tracker (default 8 fails / 15 min).
func NewFailureTracker(maxFail int, window time.Duration) *FailureTracker {
	if maxFail <= 0 {
		maxFail = 8
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	return &FailureTracker{
		maxFail:  maxFail,
		window:   window,
		failures: make(map[string]*failEntry),
	}
}

// Locked returns true if key is currently locked out.
func (t *FailureTracker) Locked(key string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.failures[key]
	if !ok {
		return false
	}
	if time.Since(e.first) > t.window {
		delete(t.failures, key)
		return false
	}
	return e.count >= t.maxFail
}

// Fail records a failed attempt. Returns true if now locked.
func (t *FailureTracker) Fail(key string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	e, ok := t.failures[key]
	if !ok || now.Sub(e.first) > t.window {
		t.failures[key] = &failEntry{count: 1, first: now}
		return false
	}
	e.count++
	return e.count >= t.maxFail
}

// Clear resets failures after successful auth.
func (t *FailureTracker) Clear(key string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, key)
}
