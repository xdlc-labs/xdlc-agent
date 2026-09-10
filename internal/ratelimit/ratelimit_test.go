package ratelimit

import (
	"testing"
	"time"
)

func TestAllowBurstThenReject(t *testing.T) {
	l := New(10, 3) // 10/sec, burst 3
	for i := 0; i < 3; i++ {
		if !l.Allow() {
			t.Fatalf("Allow #%d = false, want true (burst)", i)
		}
	}
	if l.Allow() {
		t.Fatal("Allow after burst = true, want false")
	}
}

func TestAllowRefills(t *testing.T) {
	l := New(100, 1) // fast refill
	if !l.Allow() {
		t.Fatal("first Allow failed")
	}
	if l.Allow() {
		t.Fatal("second Allow should fail before refill")
	}
	time.Sleep(20 * time.Millisecond)
	if !l.Allow() {
		t.Fatal("Allow after refill failed")
	}
}

func TestNilLimiterAllows(t *testing.T) {
	var l *Limiter
	if !l.Allow() {
		t.Fatal("nil Limiter.Allow should be true")
	}
}

func TestNewUnlimited(t *testing.T) {
	if New(0, 10) != nil {
		t.Fatal("rate 0 should yield nil")
	}
	if New(10, 0) != nil {
		t.Fatal("burst 0 should yield nil")
	}
}
