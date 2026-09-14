// Package ratelimit provides a stdlib-only token-bucket limiter.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a token-bucket rate limiter. A nil *Limiter allows everything
// (convenient for tests that skip rate limiting).
type Limiter struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

// New returns a token-bucket limiter refilling at rate tokens/sec up to burst.
// rate <= 0 or burst < 1 yields a nil limiter (unlimited).
func New(rate float64, burst int) *Limiter {
	if rate <= 0 || burst < 1 {
		return nil
	}
	b := float64(burst)
	return &Limiter{rate: rate, burst: b, tokens: b, last: time.Now()}
}

// Allow reports whether one event may proceed, consuming a token when true.
func (l *Limiter) Allow() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill(time.Now())
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

func (l *Limiter) refill(now time.Time) {
	elapsed := now.Sub(l.last).Seconds()
	if elapsed > 0 {
		l.tokens += elapsed * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}
}
