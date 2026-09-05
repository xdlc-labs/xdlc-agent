package authn

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// sessionPayload is the signed cookie's content — a stateless session,
// no server-side store. Role is fixed at issue time; a group membership
// change at the IdP only takes effect on the next login (SessionTTL bounds
// how stale that can get).
type sessionPayload struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Role  string `json:"role"`
	Exp   int64  `json:"exp"`
}

// signValue produces "base64url(json) + . + base64url(hmac-sha256)" —
// the same shape used for both the session cookie and the short-lived
// OAuth flow-state cookie, just with a different payload type.
func signValue(secret []byte, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// verifyValue checks the HMAC and returns the decoded payload bytes.
func verifyValue(secret []byte, value string) ([]byte, error) {
	i := indexByte(value, '.')
	if i < 0 {
		return nil, fmt.Errorf("authn: malformed signed value")
	}
	payloadB64, sigB64 := value[:i], value[i+1:]
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, fmt.Errorf("authn: decode payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, fmt.Errorf("authn: decode signature: %w", err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	want := mac.Sum(nil)
	if subtle.ConstantTimeCompare(sig, want) != 1 {
		return nil, fmt.Errorf("authn: signature mismatch")
	}
	return payload, nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// issueSession signs a session cookie value for (sub, email, role),
// expiring after cfg.SessionTTL.
func (a *Authenticator) issueSession(sub, email, role string) string {
	p := sessionPayload{Sub: sub, Email: email, Role: role, Exp: a.now().Add(a.cfg.SessionTTL).Unix()}
	b, _ := json.Marshal(p) // sessionPayload is always marshalable
	return signValue(a.cfg.SessionSecret, b)
}

// verifySession checks a session cookie value's signature and expiry,
// returning its role on success.
func (a *Authenticator) verifySession(value string) (role string, ok bool) {
	payload, err := verifyValue(a.cfg.SessionSecret, value)
	if err != nil {
		return "", false
	}
	var p sessionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", false
	}
	if p.Exp == 0 || a.now().After(time.Unix(p.Exp, 0)) {
		return "", false
	}
	if p.Role != "operator" && p.Role != "viewer" {
		return "", false
	}
	return p.Role, true
}

// flowPayload is the short-lived cookie carrying OAuth state/nonce/PKCE
// verifier between GET /auth/login and GET /auth/callback — avoids
// needing a server-side session store for an in-flight login.
type flowPayload struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Exp      int64  `json:"exp"`
}

func (a *Authenticator) issueFlow(state, nonce, verifier string) string {
	p := flowPayload{State: state, Nonce: nonce, Verifier: verifier, Exp: a.now().Add(5 * time.Minute).Unix()}
	b, _ := json.Marshal(p)
	return signValue(a.cfg.SessionSecret, b)
}

func (a *Authenticator) verifyFlow(value, wantState string) (flowPayload, error) {
	payload, err := verifyValue(a.cfg.SessionSecret, value)
	if err != nil {
		return flowPayload{}, err
	}
	var p flowPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return flowPayload{}, fmt.Errorf("authn: parse flow cookie: %w", err)
	}
	if p.Exp == 0 || a.now().After(time.Unix(p.Exp, 0)) {
		return flowPayload{}, fmt.Errorf("authn: login flow expired, try again")
	}
	if subtle.ConstantTimeCompare([]byte(p.State), []byte(wantState)) != 1 {
		return flowPayload{}, fmt.Errorf("authn: state mismatch")
	}
	return p, nil
}
