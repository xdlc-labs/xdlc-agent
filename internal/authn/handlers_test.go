package authn

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// getCtx is client.Get with an explicit background context — noctx wants
// NewRequestWithContext even in tests hitting a local httptest server.
func getCtx(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestFullLoginFlow drives GET /auth/login -> IdP authorize -> GET
// /auth/callback exactly as a browser + real IdP would (state/PKCE/nonce
// included), ending with a session cookie that VerifySession accepts.
// The test IdP's /authorize just echoes back state+redirects with a
// code, standing in for a real consent screen.
func TestFullLoginFlow(t *testing.T) {
	idp := newTestIdPWithAuthorize(t)
	idp.nextID = func() (string, string, []string) { return "alice", "alice@example.com", []string{"platform-team"} }

	a := newTestAuthenticator(t, idp, []string{"platform-team"}, nil)
	daemonMux := http.NewServeMux()
	a.Mount(daemonMux)
	daemon := httptest.NewServer(daemonMux)
	t.Cleanup(daemon.Close)
	a.cfg.RedirectURL = daemon.URL + "/auth/callback"
	a.oauth2Cfg.RedirectURL = a.cfg.RedirectURL

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse // inspect each hop instead of auto-following
		},
	}

	// 1. GET /auth/login on the daemon -> redirect to the IdP's /authorize.
	resp := getCtx(t, client, daemon.URL+LoginPath)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d, want 302", resp.StatusCode)
	}
	authorizeURL := resp.Header.Get("Location")
	if !strings.Contains(authorizeURL, idp.srv.URL) {
		t.Fatalf("expected redirect to IdP, got %q", authorizeURL)
	}

	// 2. Follow to the IdP's /authorize (test stand-in for a consent
	// screen) -> redirect back to the daemon's callback with code+state.
	resp = getCtx(t, client, authorizeURL)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302", resp.StatusCode)
	}
	callbackURL := resp.Header.Get("Location")
	if !strings.Contains(callbackURL, "/auth/callback") {
		t.Fatalf("expected redirect to callback, got %q", callbackURL)
	}

	// 3. Follow to the daemon's callback -> exchanges code, verifies the
	// ID token, sets the session cookie, redirects to "/".
	resp = getCtx(t, client, callbackURL)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Fatalf("callback status = %d, location = %q, body = %s", resp.StatusCode, resp.Header.Get("Location"), body)
	}

	daemonURL, _ := url.Parse(daemon.URL)
	var sessionCookie *http.Cookie
	for _, c := range jar.Cookies(daemonURL) {
		if c.Name == SessionCookie {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie set after callback")
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.AddCookie(sessionCookie)
	role, ok := a.VerifySession(req)
	if !ok || role != "operator" {
		t.Fatalf("VerifySession after login = %q, %v; want operator, true", role, ok)
	}
}

func TestLogoutClearsSession(t *testing.T) {
	idp := newTestIdP(t)
	a := newTestAuthenticator(t, idp, nil, nil)
	mux := http.NewServeMux()
	a.Mount(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, LogoutPath, nil)
	mux.ServeHTTP(rec, req)

	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookie {
			found = true
			if c.MaxAge >= 0 {
				t.Errorf("logout cookie MaxAge = %d, want negative (delete)", c.MaxAge)
			}
		}
	}
	if !found {
		t.Fatal("logout did not set a clearing Set-Cookie for the session")
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	idp := newTestIdP(t)
	a := newTestAuthenticator(t, idp, nil, nil)
	mux := http.NewServeMux()
	a.Mount(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/callback?state=whatever&code=abc", nil)
	// No flow cookie at all — simulates a forged/replayed callback URL.
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing flow cookie)", rec.Code)
	}
}

func TestAuthConfigReportsEnabled(t *testing.T) {
	idp := newTestIdP(t)
	a := newTestAuthenticator(t, idp, nil, nil)
	mux := http.NewServeMux()
	a.Mount(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"enabled":true`, LoginPath, LogoutPath} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q missing %q", body, want)
		}
	}
}

// newTestIdPWithAuthorize extends newTestIdP with a bare-bones
// /authorize that echoes state back with a fixed code, standing in for
// a real login+consent screen.
func newTestIdPWithAuthorize(t *testing.T) *testIdP {
	t.Helper()
	idp := newTestIdP(t)
	mux := idp.srv.Config.Handler.(*http.ServeMux)
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		nonce := q.Get("nonce")
		// Smuggle the real nonce through to the token handler via the
		// authorization code itself (test IdP only — a real one would
		// bind it server-side to the code).
		u, err := url.Parse(redirect)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		v := u.Query()
		v.Set("state", state)
		v.Set("code", nonce) // token endpoint reads this back as nonce_for_test's source
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	return idp
}
