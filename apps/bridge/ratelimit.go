package main

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter keeps a per-key counter in memory. It is deliberately simple:
// the bridge is a single process and this only needs to stop a bored person
// from spamming checkout or the WhatsApp bot.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
}

type bucket struct {
	count int
	reset time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: map[string]*bucket{}}
}

// limit wraps a handler, allowing max requests per window per key. The key is
// the client IP unless the handler opts into something else.
func (l *rateLimiter) limit(name string, max int, window time.Duration, next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		key := name + "|" + clientIP(r)
		if !l.allow(key, max, window) {
			w.Header().Set("Retry-After", itoa(int(window.Seconds())))
			writeError(w, http.StatusTooManyRequests, "too many requests, please wait a moment")
			return
		}
		next(w, r)
	}
}

// allowKey is the same check for a business key such as a phone number.
func (l *rateLimiter) allowKey(name, key string, max int, window time.Duration) bool {
	return l.allow(name+"|"+key, max, window)
}

func (l *rateLimiter) allow(key string, max int, window time.Duration) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.gcLocked(now)

	b, ok := l.buckets[key]
	if !ok || now.After(b.reset) {
		l.buckets[key] = &bucket{count: 1, reset: now.Add(window)}
		return true
	}
	if b.count >= max {
		return false
	}
	b.count++
	return true
}

func (l *rateLimiter) gcLocked(now time.Time) {
	if now.Sub(l.lastGC) < 5*time.Minute {
		return
	}
	l.lastGC = now
	for key, b := range l.buckets {
		if now.After(b.reset) {
			delete(l.buckets, key)
		}
	}
}

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if idx := strings.IndexByte(forwarded, ','); idx > 0 {
			return strings.TrimSpace(forwarded[:idx])
		}
		return strings.TrimSpace(forwarded)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
