package authn

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testIdP runs a minimal OIDC provider: discovery, JWKS, and a token
// endpoint that mints a real RS256 ID token for whatever's requested,
// enough to exercise Authenticator's actual verification path rather
// than mocking it away.
type testIdP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	nextID func() (sub, email string, groups []string)
}

func newTestIdP(t *testing.T) *testIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &testIdP{key: key, kid: "test-kid"}
	idp.nextID = func() (string, string, []string) { return "user-1", "user@example.com", nil }

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 base,
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
			"jwks_uri":               base + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(idp.key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(bigEndianBytes(idp.key.E))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{"kty": "RSA", "kid": idp.kid, "n": n, "e": e}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// exchangeAndVerify (oidc_test.go) posts nonce_for_test directly;
		// the full-flow test (handlers_test.go) goes through a real
		// oauth2.Config.Exchange, which sends "code" instead — the test
		// /authorize handler smuggles the nonce into the code itself.
		nonce := r.Form.Get("nonce_for_test")
		if nonce == "" {
			nonce = r.Form.Get("code")
		}
		sub, email, groups := idp.nextID()
		idToken := idp.mintIDToken(t, "http://"+r.Host, "test-client", nonce, sub, email, groups)
		// golang.org/x/oauth2 form-decodes the body unless the response
		// explicitly says application/json — Go's default content-type
		// sniffing calls a JSON body text/plain, which breaks Exchange.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "unused",
			"token_type":   "Bearer",
			"id_token":     idToken,
		})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func bigEndianBytes(i int) []byte {
	b := []byte{byte(i >> 16), byte(i >> 8), byte(i)}
	for len(b) > 1 && b[0] == 0 {
		b = b[1:]
	}
	return b
}

func (idp *testIdP) mintIDToken(t *testing.T, issuer, clientID, nonce, sub, email string, groups []string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT","kid":"` + idp.kid + `"}`))
	claims := map[string]any{
		"iss":    issuer,
		"sub":    sub,
		"aud":    clientID,
		"exp":    time.Now().Add(time.Hour).Unix(),
		"nonce":  nonce,
		"email":  email,
		"groups": groups,
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := header + "." + payload
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func newTestAuthenticator(t *testing.T, idp *testIdP, operatorGroups, viewerGroups []string) *Authenticator {
	t.Helper()
	a, err := New(context.Background(), Config{
		IssuerURL:      idp.srv.URL,
		ClientID:       "test-client",
		ClientSecret:   "test-secret",
		RedirectURL:    "http://localhost/auth/callback",
		SessionSecret:  []byte("test-session-secret-32-bytes-ok!!"),
		OperatorGroups: operatorGroups,
		ViewerGroups:   viewerGroups,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// exchangeAndVerify drives the same path handleCallback does — mint a
// token via the test IdP's /token, then verify it — without going
// through real HTTP redirects (state/PKCE plumbing is exercised
// end-to-end in handlers_test.go).
func exchangeAndVerify(t *testing.T, a *Authenticator, idp *testIdP, nonce string) (idTokenClaims, map[string]any, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, idp.srv.URL+"/token", strings.NewReader(url.Values{
		"nonce_for_test": {nonce},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return a.verifyIDToken(context.Background(), body.IDToken, nonce)
}

func TestVerifyIDTokenHappyPath(t *testing.T) {
	idp := newTestIdP(t)
	a := newTestAuthenticator(t, idp, []string{"platform-team"}, nil)

	idp.nextID = func() (string, string, []string) { return "alice", "alice@example.com", []string{"platform-team"} }
	claims, raw, err := exchangeAndVerify(t, a, idp, "nonce-123")
	if err != nil {
		t.Fatalf("verifyIDToken: %v", err)
	}
	if claims.Sub != "alice" || claims.Email != "alice@example.com" {
		t.Errorf("claims = %+v", claims)
	}
	groups := groupsFromClaims(raw, "groups")
	if len(groups) != 1 || groups[0] != "platform-team" {
		t.Errorf("groups = %v", groups)
	}
}

func TestVerifyIDTokenNonceMismatch(t *testing.T) {
	idp := newTestIdP(t)
	a := newTestAuthenticator(t, idp, nil, nil)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, idp.srv.URL+"/token", strings.NewReader(url.Values{
		"nonce_for_test": {"actual-nonce"},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.verifyIDToken(context.Background(), body.IDToken, "expected-different-nonce"); err == nil {
		t.Fatal("expected nonce mismatch error")
	}
}

func TestVerifyIDTokenWrongAudience(t *testing.T) {
	idp := newTestIdP(t)
	a := newTestAuthenticator(t, idp, nil, nil)
	a.cfg.ClientID = "some-other-client" // simulate a token minted for a different client

	if _, _, err := exchangeAndVerify(t, a, idp, "n"); err == nil {
		t.Fatal("expected audience mismatch error")
	}
}

func TestViewerGroupsRestrictionThreadsThroughConfig(t *testing.T) {
	idp := newTestIdP(t)
	// End-to-end through New/Config (not just the decideRole unit test):
	// a non-empty ViewerGroups should actually reach the Authenticator.
	a := newTestAuthenticator(t, idp, []string{"platform-team"}, []string{"contractors"})

	idp.nextID = func() (string, string, []string) { return "bob", "bob@example.com", []string{"contractors"} }
	claims, raw, err := exchangeAndVerify(t, a, idp, "nonce-456")
	if err != nil {
		t.Fatalf("verifyIDToken: %v", err)
	}
	groups := groupsFromClaims(raw, a.cfg.GroupsClaim)
	role, ok := decideRole(groups, a.cfg.OperatorGroups, a.cfg.ViewerGroups)
	if !ok || role != "viewer" {
		t.Fatalf("bob (contractors) via configured ViewerGroups = %q, %v; want viewer, true", role, ok)
	}
	if claims.Sub != "bob" {
		t.Errorf("sub = %q", claims.Sub)
	}

	idp.nextID = func() (string, string, []string) { return "eve", "eve@example.com", []string{"outsiders"} }
	_, raw, err = exchangeAndVerify(t, a, idp, "nonce-789")
	if err != nil {
		t.Fatalf("verifyIDToken: %v", err)
	}
	groups = groupsFromClaims(raw, a.cfg.GroupsClaim)
	if _, ok := decideRole(groups, a.cfg.OperatorGroups, a.cfg.ViewerGroups); ok {
		t.Fatal("eve (outsiders) should be rejected by the configured ViewerGroups allowlist")
	}
}

func TestVerifyIDTokenExpired(t *testing.T) {
	idp := newTestIdP(t)
	a, err := New(context.Background(), Config{
		IssuerURL:     idp.srv.URL,
		ClientID:      "test-client",
		ClientSecret:  "s",
		RedirectURL:   "http://localhost/auth/callback",
		SessionSecret: []byte("test-session-secret-32-bytes-ok!!"),
		Now:           func() time.Time { return time.Now().Add(2 * time.Hour) }, // "now" is past the 1h exp minted below
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := exchangeAndVerify(t, a, idp, "n"); err == nil {
		t.Fatal("expected expiry error")
	}
}

func TestDecideRole(t *testing.T) {
	cases := []struct {
		name                     string
		groups, operator, viewer []string
		wantRole                 string
		wantOK                   bool
	}{
		{"operator group wins", []string{"eng", "platform-team"}, []string{"platform-team"}, nil, "operator", true},
		{"no viewer allowlist defaults everyone to viewer", []string{"random"}, []string{"platform-team"}, nil, "viewer", true},
		{"explicit viewer allowlist enforced", []string{"random"}, []string{"platform-team"}, []string{"eng"}, "", false},
		{"explicit viewer allowlist matched", []string{"eng"}, []string{"platform-team"}, []string{"eng"}, "viewer", true},
		{"no operator groups configured, never grants operator", []string{"platform-team"}, nil, nil, "viewer", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			role, ok := decideRole(c.groups, c.operator, c.viewer)
			if role != c.wantRole || ok != c.wantOK {
				t.Errorf("decideRole(%v, %v, %v) = %q, %v; want %q, %v", c.groups, c.operator, c.viewer, role, ok, c.wantRole, c.wantOK)
			}
		})
	}
}

func TestGroupsFromClaims(t *testing.T) {
	if got := groupsFromClaims(map[string]any{"groups": []any{"a", "b"}}, "groups"); fmt.Sprint(got) != "[a b]" {
		t.Errorf("array form: got %v", got)
	}
	if got := groupsFromClaims(map[string]any{"groups": "solo"}, "groups"); fmt.Sprint(got) != "[solo]" {
		t.Errorf("string form: got %v", got)
	}
	if got := groupsFromClaims(map[string]any{}, "groups"); got != nil {
		t.Errorf("missing claim: got %v, want nil", got)
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	idp := newTestIdP(t)
	cases := []struct {
		name string
		cfg  Config
	}{
		{"no issuer", Config{}},
		{"no client id/secret", Config{IssuerURL: idp.srv.URL, RedirectURL: "x", SessionSecret: []byte("test-session-secret-32-bytes-ok!!")}},
		{"no redirect", Config{IssuerURL: idp.srv.URL, ClientID: "c", ClientSecret: "s", SessionSecret: []byte("test-session-secret-32-bytes-ok!!")}},
		{"no session secret", Config{IssuerURL: idp.srv.URL, ClientID: "c", ClientSecret: "s", RedirectURL: "x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(context.Background(), c.cfg); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}

func TestNewFailsOnUnreachableIssuer(t *testing.T) {
	if _, err := New(context.Background(), Config{
		IssuerURL: "http://127.0.0.1:1", ClientID: "c", ClientSecret: "s",
		RedirectURL: "x", SessionSecret: []byte("test-session-secret-32-bytes-ok!!"),
	}); err == nil {
		t.Fatal("expected discovery to fail against an unreachable issuer")
	}
}
