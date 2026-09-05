package ratelimit

import "testing"

func BenchmarkAllowUnlimited(b *testing.B) {
	var l *Limiter // nil = rate limit disabled
	b.ReportAllocs()
	for b.Loop() {
		if !l.Allow() {
			b.Fatal("nil Limiter.Allow returned false")
		}
	}
}

func BenchmarkAllow(b *testing.B) {
	// Burst covers b.N growth across Loop phases (elapsed may be 0 in a tight loop).
	l := New(1, 1<<30)
	b.ReportAllocs()
	for b.Loop() {
		if !l.Allow() {
			b.Fatal("Allow returned false")
		}
	}
}
