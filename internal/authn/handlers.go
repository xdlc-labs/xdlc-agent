package authn

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"golang.org/x/oauth2"
)

const (
	// SessionCookie holds the signed post-login session.
	SessionCookie = "xdlc_session"
	// flowCookie holds the signed in-flight-login state (state/nonce/PKCE
	// verifier) between /auth/login and /auth/callback.
	flowCookie = "xdlc_oauth_flow"

	// LoginPath starts the OIDC flow; also reported by GET /auth/config
	// so the console doesn't have to hardcode it.
	LoginPath = "/auth/login"
	// LogoutPath clears the session cookie; also reported by GET /auth/config.
	LogoutPath   = "/auth/logout"
	configPath   = "/auth/config"
	callbackPath = "/auth/callback"
)

// Mount registers the OIDC login/callback/logout/config handlers on mux.
func (a *Authenticator) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+configPath, a.handleConfig)
	mux.HandleFunc("GET "+LoginPath, a.handleLogin)
	mux.HandleFunc("GET "+callbackPath, a.handleCallback)
	mux.HandleFunc("GET "+LogoutPath, a.handleLogout)
}

// VerifySession implements the func signature internal/api.Server.SessionVerifier
// expects — checks the session cookie, if any, for a valid role.
func (a *Authenticator) VerifySession(r *http.Request) (string, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return "", false
	}
	return a.verifySession(c.Value)
}

func (a *Authenticator) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"enabled":   true,
		"loginUrl":  LoginPath,
		"logoutUrl": LogoutPath,
	})
}

func randomURLSafe(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// setCookie centralizes HttpOnly/Secure/SameSite so every cookie this
// package sets or clears gets the same attributes — a mismatch on
// clearing (path/samesite) leaves the old cookie behind instead of
// deleting it.
//
// gosec G124: Secure is operator-configured (oidc.cookie_secure,
// default true — false only for local http:// testing, see
// OIDCConfig's doc comment), not a static literal gosec's checker can
// see; always paired here with HttpOnly+SameSite regardless.
func (a *Authenticator) setCookie(w http.ResponseWriter, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is operator-configured, see doc comment above
		Name: name, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (a *Authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomURLSafe(24)
	if err != nil {
		http.Error(w, "authn: failed to start login", http.StatusInternalServerError)
		return
	}
	nonce, err := randomURLSafe(24)
	if err != nil {
		http.Error(w, "authn: failed to start login", http.StatusInternalServerError)
		return
	}
	verifier := oauth2.GenerateVerifier()

	a.setCookie(w, flowCookie, a.issueFlow(state, nonce, verifier), "/auth", 300)

	url := a.oauth2Cfg.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
	http.Redirect(w, r, url, http.StatusFound)
}

func (a *Authenticator) handleCallback(w http.ResponseWriter, r *http.Request) {
	flowRaw, err := r.Cookie(flowCookie)
	if err != nil {
		http.Error(w, "authn: missing login flow cookie — start over at "+LoginPath, http.StatusBadRequest)
		return
	}
	flow, err := a.verifyFlow(flowRaw.Value, r.URL.Query().Get("state"))
	if err != nil {
		http.Error(w, "authn: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Consume the flow cookie regardless of outcome below — one-shot.
	a.setCookie(w, flowCookie, "", "/auth", -1)

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Error(w, "authn: identity provider returned error: "+errParam, http.StatusUnauthorized)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "authn: missing code", http.StatusBadRequest)
		return
	}

	token, err := a.oauth2Cfg.Exchange(r.Context(), code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		http.Error(w, "authn: token exchange failed: "+err.Error(), http.StatusUnauthorized)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		http.Error(w, "authn: token response missing id_token", http.StatusUnauthorized)
		return
	}

	claims, rawClaims, err := a.verifyIDToken(r.Context(), rawIDToken, flow.Nonce)
	if err != nil {
		http.Error(w, "authn: "+err.Error(), http.StatusUnauthorized)
		return
	}

	groups := groupsFromClaims(rawClaims, a.cfg.GroupsClaim)
	role, ok := decideRole(groups, a.cfg.OperatorGroups, a.cfg.ViewerGroups)
	if !ok {
		http.Error(w, "authn: authenticated, but not a member of any configured operator/viewer group", http.StatusForbidden)
		return
	}

	a.setCookie(w, SessionCookie, a.issueSession(claims.Sub, claims.Email, role), "/", int(a.cfg.SessionTTL.Seconds()))
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *Authenticator) handleLogout(w http.ResponseWriter, r *http.Request) {
	a.setCookie(w, SessionCookie, "", "/", -1)
	http.Redirect(w, r, "/", http.StatusFound)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
