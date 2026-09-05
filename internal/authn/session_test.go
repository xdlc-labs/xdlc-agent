package authn

import (
	"testing"
	"time"
)

func newTestAuthenticatorForSessions(t *testing.T, ttl time.Duration, now func() time.Time) *Authenticator {
	t.Helper()
	a := &Authenticator{
		cfg: Config{SessionSecret: []byte("session-secret-for-tests-32b-ok!"), SessionTTL: ttl},
		now: now,
	}
	if a.now == nil {
		a.now = time.Now
	}
	return a
}

func TestSessionRoundTrip(t *testing.T) {
	a := newTestAuthenticatorForSessions(t, time.Hour, nil)
	v := a.issueSession("alice", "alice@example.com", "operator")

	role, ok := a.verifySession(v)
	if !ok || role != "operator" {
		t.Fatalf("verifySession = %q, %v; want operator, true", role, ok)
	}
}

func TestSessionExpired(t *testing.T) {
	current := time.Now()
	a := newTestAuthenticatorForSessions(t, time.Minute, func() time.Time { return current })
	v := a.issueSession("alice", "a@example.com", "viewer")

	current = current.Add(2 * time.Minute)
	if _, ok := a.verifySession(v); ok {
		t.Fatal("expected expired session to fail verification")
	}
}

func TestSessionTamperedRejected(t *testing.T) {
	a := newTestAuthenticatorForSessions(t, time.Hour, nil)
	v := a.issueSession("alice", "a@example.com", "viewer")

	// Flip a character in the payload — signature must no longer match.
	tampered := v[:5] + "X" + v[6:]
	if _, ok := a.verifySession(tampered); ok {
		t.Fatal("tampered session cookie must not verify")
	}
}

func TestSessionWrongSecretRejected(t *testing.T) {
	a := newTestAuthenticatorForSessions(t, time.Hour, nil)
	v := a.issueSession("alice", "a@example.com", "operator")

	other := newTestAuthenticatorForSessions(t, time.Hour, nil)
	other.cfg.SessionSecret = []byte("a-completely-different-secret!!")
	if _, ok := other.verifySession(v); ok {
		t.Fatal("session signed with a different secret must not verify")
	}
}

func TestSessionInvalidRoleRejected(t *testing.T) {
	a := newTestAuthenticatorForSessions(t, time.Hour, nil)
	v := a.issueSession("alice", "a@example.com", "superadmin") // not a role this package grants
	if _, ok := a.verifySession(v); ok {
		t.Fatal("an unrecognized role must not verify, even with a valid signature")
	}
}

func TestFlowRoundTripAndStateMismatch(t *testing.T) {
	a := newTestAuthenticatorForSessions(t, time.Hour, nil)
	v := a.issueFlow("state-1", "nonce-1", "verifier-1")

	flow, err := a.verifyFlow(v, "state-1")
	if err != nil {
		t.Fatalf("verifyFlow: %v", err)
	}
	if flow.Nonce != "nonce-1" || flow.Verifier != "verifier-1" {
		t.Errorf("flow = %+v", flow)
	}

	if _, err := a.verifyFlow(v, "wrong-state"); err == nil {
		t.Fatal("expected state mismatch error")
	}
}

func TestFlowExpired(t *testing.T) {
	current := time.Now()
	a := newTestAuthenticatorForSessions(t, time.Hour, func() time.Time { return current })
	v := a.issueFlow("state-1", "nonce-1", "verifier-1")

	current = current.Add(10 * time.Minute) // flow cookies are 5-minute TTL, fixed
	if _, err := a.verifyFlow(v, "state-1"); err == nil {
		t.Fatal("expected expired flow error")
	}
}
