package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter is a simple fixed-window limiter keyed by client IP.
//
// LIMITATION (documented, not silently accepted): this state is in-process
// memory, not shared across replicas. It is adequate for a single-instance
// deployment or local development but does not enforce a correct limit
// across multiple API replicas — a distributed limiter (e.g. Redis-backed)
// is required before running more than one API replica behind a load
// balancer. See docs/implementation-status.md.
type RateLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	limit    int
	counters map[string]*bucket
}

// maxTrackedKeys bounds map growth: past it, expired windows are swept.
const maxTrackedKeys = 10000

type bucket struct {
	count       int
	windowStart time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		window:   window,
		limit:    limit,
		counters: make(map[string]*bucket),
	}
}

func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if len(rl.counters) > maxTrackedKeys {
		for k, b := range rl.counters {
			if now.Sub(b.windowStart) > rl.window {
				delete(rl.counters, k)
			}
		}
	}
	b, ok := rl.counters[key]
	if !ok || now.Sub(b.windowStart) > rl.window {
		rl.counters[key] = &bucket{count: 1, windowStart: now}
		return true
	}
	if b.count >= rl.limit {
		return false
	}
	b.count++
	return true
}

// Middleware rejects requests over the limit with 429. Keying is by client
// IP; routes mounting this middleware should be ones where slowing down a
// single IP is an acceptable cost of defending against credential
// stuffing / brute force (login, password reset, verification resend).
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !rl.allow(host) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"too many requests"}}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
