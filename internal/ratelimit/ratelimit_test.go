package ratelimit

import (
	"context"
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
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("nil Limiter.Wait: %v", err)
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

func TestWaitRespectsContext(t *testing.T) {
	l := New(0.001, 1) // tiny rate
	if !l.Allow() {
		t.Fatal("consume sole token")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Fatal("Wait should fail when ctx times out before refill")
	}
}
