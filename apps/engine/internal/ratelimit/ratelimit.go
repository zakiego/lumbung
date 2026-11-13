// Package ratelimit is a token-bucket limiter keyed by string: per token for
// requests, per address for failed authentication (PLAN 6.8).
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows rate events per second per key, with bursts up to burst.
type Limiter struct {
	rate  float64
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	sweep   time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a limiter. A rate of zero or less allows everything.
func New(rate float64, burst int) *Limiter {
	return &Limiter{rate: rate, burst: float64(burst), now: time.Now, buckets: map[string]*bucket{}}
}

// Allow takes one event for key and reports whether it fits. When it
// doesn't, retryAfter says when the next one will.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	if l.rate <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweepLocked(now)
	b, found := l.buckets[key]
	if !found {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// sweepLocked drops full buckets once a minute so idle keys don't pile up.
func (l *Limiter) sweepLocked(now time.Time) {
	if now.Sub(l.sweep) < time.Minute {
		return
	}
	l.sweep = now
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// Peek reports whether key has an event left, without taking it.
func (l *Limiter) Peek(key string) (ok bool, retryAfter time.Duration) {
	if l.rate <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, found := l.buckets[key]
	if !found {
		return true, 0
	}
	tokens := min(l.burst, b.tokens+l.now().Sub(b.last).Seconds()*l.rate)
	if tokens >= 1 {
		return true, 0
	}
	return false, time.Duration((1 - tokens) / l.rate * float64(time.Second))
}
