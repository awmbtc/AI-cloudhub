package policy

import (
	"sync"
	"time"
)

// Limiter is a simple per-key token bucket (in-process).
type Limiter struct {
	rate   float64 // tokens per second
	burst  float64
	mu     sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter creates a limiter. rate = permits/sec, burst = max burst.
func NewLimiter(rate, burst float64) *Limiter {
	if rate <= 0 {
		rate = 10
	}
	if burst <= 0 {
		burst = rate
	}
	return &Limiter{
		rate:    rate,
		burst:   burst,
		buckets: make(map[string]*bucket),
	}
}

// Allow returns true if key may proceed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
