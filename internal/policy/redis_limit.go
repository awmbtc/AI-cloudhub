package policy

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter is a shared rate limiter using Redis INCR + EXPIRE (fixed window).
// Suitable for multi-instance API. Not a perfect token bucket but simple and effective.
type RedisLimiter struct {
	rdb    *redis.Client
	limit  int           // max requests per window
	window time.Duration
	prefix string
}

// NewRedisLimiter connects to Redis. addr e.g. redis://localhost:6379/0 or host:port.
func NewRedisLimiter(redisURL string, limit int, window time.Duration) (*RedisLimiter, error) {
	if limit <= 0 {
		limit = 100
	}
	if window <= 0 {
		window = time.Minute
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		// bare host:port
		opt = &redis.Options{Addr: redisURL}
	}
	rdb := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisLimiter{rdb: rdb, limit: limit, window: window, prefix: "aihub:rl:"}, nil
}

// Allow implements the same semantics as Limiter.Allow for multi-instance use.
func (r *RedisLimiter) Allow(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	k := r.prefix + key
	n, err := r.rdb.Incr(ctx, k).Result()
	if err != nil {
		// fail open for availability (or fail closed — choose open for MVP control plane)
		return true
	}
	if n == 1 {
		_ = r.rdb.Expire(ctx, k, r.window).Err()
	}
	return n <= int64(r.limit)
}

// Close closes the Redis client.
func (r *RedisLimiter) Close() error {
	if r.rdb == nil {
		return nil
	}
	return r.rdb.Close()
}

// RateLimiter is implemented by both in-process and Redis limiters.
type RateLimiter interface {
	Allow(key string) bool
}

// Ensure *Limiter and *RedisLimiter implement RateLimiter.
var (
	_ RateLimiter = (*Limiter)(nil)
	_ RateLimiter = (*RedisLimiter)(nil)
)

// ParseRedisLimit reads env-style numbers: limit per window seconds.
func ParseRedisLimit(limitStr, windowSecStr string) (int, time.Duration) {
	limit := 100
	if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
		limit = n
	}
	sec := 60
	if n, err := strconv.Atoi(windowSecStr); err == nil && n > 0 {
		sec = n
	}
	return limit, time.Duration(sec) * time.Second
}
