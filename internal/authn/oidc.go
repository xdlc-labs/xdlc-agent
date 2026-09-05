// Package authn implements OIDC SSO login for the ops console: the
// authorization-code + PKCE flow (via golang.org/x/oauth2, already a
// dependency), hand-rolled RS256 ID-token verification against the
// issuer's JWKS (stdlib crypto only, no JWT library — same ceiling
// tradeoff as internal/ghclient's App-auth JWT signing), and a signed,
// stateless session cookie. Additive to internal/api's existing bearer
// tokens, not a replacement — see docs/console.md.
package authn

import (
	"context"
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Config is internal/config.OIDCConfig with secrets already resolved
// from env and defaults applied.
type Config struct {
	IssuerURL      string
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	Scopes         []string
	GroupsClaim    string
	OperatorGroups []string
	ViewerGroups   []string
	SessionSecret  []byte
	SessionTTL     time.Duration
	CookieSecure   bool

	// HTTPClient and Now are overridable for tests; nil → http.DefaultClient / time.Now.
	HTTPClient *http.Client
	Now        func() time.Time
}

type discoveryDoc struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDoc struct {
	Keys []jwk `json:"keys"`
}

// Authenticator runs OIDC discovery once at construction (fail closed —
// see Package doc) and serves the login/callback/logout/config handlers.
type Authenticator struct {
	cfg       Config
	http      *http.Client
	now       func() time.Time
	oauth2Cfg oauth2.Config
	disc      discoveryDoc

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	keysFetched time.Time
}

// New runs discovery against cfg.IssuerURL and fetches its JWKS. Returns
// an error on any failure — the caller (cmd/xdlc-agent) treats OIDC
// misconfiguration as fatal at startup, not a silent no-SSO fallback.
func New(ctx context.Context, cfg Config) (*Authenticator, error) {
	if cfg.IssuerURL == "" {
		return nil, fmt.Errorf("authn: IssuerURL required")
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("authn: client_id and client secret required")
	}
	if cfg.RedirectURL == "" {
		return nil, fmt.Errorf("authn: redirect_url required")
	}
	if len(cfg.SessionSecret) == 0 {
		return nil, fmt.Errorf("authn: session secret required (set oidc.session_secret_env)")
	}
	if len(cfg.SessionSecret) < 32 {
		return nil, fmt.Errorf("authn: session secret must be at least 32 bytes (got %d)", len(cfg.SessionSecret))
	}
	if err := requireHTTPSIssuer(cfg.IssuerURL); err != nil {
		return nil, err
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "email", "profile"}
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 8 * time.Hour
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	a := &Authenticator{cfg: cfg, http: httpClient, now: now}

	disc, err := a.fetchDiscovery(ctx)
	if err != nil {
		return nil, fmt.Errorf("authn: discovery: %w", err)
	}
	a.disc = disc
	a.oauth2Cfg = oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       cfg.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  disc.AuthorizationEndpoint,
			TokenURL: disc.TokenEndpoint,
		},
	}

	if err := a.refreshKeys(ctx); err != nil {
		return nil, fmt.Errorf("authn: jwks: %w", err)
	}
	return a, nil
}

func (a *Authenticator) fetchDiscovery(ctx context.Context) (discoveryDoc, error) {
	url := strings.TrimSuffix(a.cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return discoveryDoc{}, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return discoveryDoc{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return discoveryDoc{}, fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	var d discoveryDoc
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return discoveryDoc{}, err
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return discoveryDoc{}, fmt.Errorf("%s: missing required fields", url)
	}
	return d, nil
}

// refreshKeys fetches the issuer's JWKS. Called at construction and
// again (rate-limited to once per minute) on a verification kid-miss —
// covers ordinary key rotation without hammering the issuer on a bad token.
func (a *Authenticator) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.disc.JWKSURI, nil)
	if err != nil {
		return err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%s: status %d: %s", a.disc.JWKSURI, resp.StatusCode, body)
	}
	var doc jwksDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue // ES256/other key types: ceiling here is RS256, the common default
		}
		pub, err := rsaPublicKeyFromJWK(k)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return fmt.Errorf("no usable RSA keys in JWKS")
	}

	a.mu.Lock()
	a.keys = keys
	a.keysFetched = a.now()
	a.mu.Unlock()
	return nil
}

func rsaPublicKeyFromJWK(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func (a *Authenticator) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	key, ok := a.keys[kid]
	stale := a.now().Sub(a.keysFetched) > time.Minute
	a.mu.Unlock()
	if ok {
		return key, nil
	}
	if !stale {
		return nil, fmt.Errorf("authn: unknown kid %q (checked <1m ago)", kid)
	}
	if err := a.refreshKeys(ctx); err != nil {
		return nil, fmt.Errorf("authn: refresh jwks for kid %q: %w", kid, err)
	}
	a.mu.Lock()
	key, ok = a.keys[kid]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("authn: unknown kid %q after refresh", kid)
	}
	return key, nil
}

// idTokenClaims is the subset of ID token claims this package checks or
// uses directly. GroupsClaim's value is read separately from the raw
// claim map (groupsFromClaims) since its field name is operator-configured.
type idTokenClaims struct {
	Iss   string `json:"iss"`
	Sub   string `json:"sub"`
	Aud   any    `json:"aud"` // string or []string per spec
	Exp   int64  `json:"exp"`
	Nonce string `json:"nonce"`
	Email string `json:"email"`
}

// verifyIDToken checks signature, iss, aud, exp, and nonce, returning the
// decoded claims (as a raw map, so GroupsClaim's field name is dynamic)
// plus the typed subset above.
func (a *Authenticator) verifyIDToken(ctx context.Context, idToken, wantNonce string) (idTokenClaims, map[string]any, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return idTokenClaims{}, nil, fmt.Errorf("authn: malformed id_token")
	}
	headerB, payloadB, sigB := parts[0], parts[1], parts[2]

	headerJSON, err := base64.RawURLEncoding.DecodeString(headerB)
	if err != nil {
		return idTokenClaims{}, nil, fmt.Errorf("authn: decode header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return idTokenClaims{}, nil, fmt.Errorf("authn: parse header: %w", err)
	}
	if header.Alg != "RS256" {
		return idTokenClaims{}, nil, fmt.Errorf("authn: unsupported alg %q (RS256 only)", header.Alg)
	}

	key, err := a.keyFor(ctx, header.Kid)
	if err != nil {
		return idTokenClaims{}, nil, err
	}

	sig, err := base64.RawURLEncoding.DecodeString(sigB)
	if err != nil {
		return idTokenClaims{}, nil, fmt.Errorf("authn: decode signature: %w", err)
	}
	if err := verifyRS256(key, headerB+"."+payloadB, sig); err != nil {
		return idTokenClaims{}, nil, fmt.Errorf("authn: signature: %w", err)
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadB)
	if err != nil {
		return idTokenClaims{}, nil, fmt.Errorf("authn: decode payload: %w", err)
	}
	var claims idTokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return idTokenClaims{}, nil, fmt.Errorf("authn: parse claims: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payloadJSON, &raw); err != nil {
		return idTokenClaims{}, nil, fmt.Errorf("authn: parse claims: %w", err)
	}

	if claims.Iss != a.disc.Issuer && claims.Iss != a.cfg.IssuerURL {
		return idTokenClaims{}, nil, fmt.Errorf("authn: iss mismatch: %q", claims.Iss)
	}
	if !audienceContains(claims.Aud, a.cfg.ClientID) {
		return idTokenClaims{}, nil, fmt.Errorf("authn: aud does not contain client_id")
	}
	if claims.Exp == 0 || a.now().After(time.Unix(claims.Exp, 0)) {
		return idTokenClaims{}, nil, fmt.Errorf("authn: id_token expired")
	}
	if claims.Nonce == "" || claims.Nonce != wantNonce {
		return idTokenClaims{}, nil, fmt.Errorf("authn: nonce mismatch")
	}

	return claims, raw, nil
}

func verifyRS256(key *rsa.PublicKey, signingInput string, sig []byte) error {
	h := crypto.SHA256.New()
	h.Write([]byte(signingInput))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, h.Sum(nil), sig)
}

func audienceContains(aud any, clientID string) bool {
	switch v := aud.(type) {
	case string:
		return v == clientID
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && s == clientID {
				return true
			}
		}
	}
	return false
}

// groupsFromClaims extracts a []string from raw[groupsClaim], accepting
// either a JSON array of strings or a single string.
func groupsFromClaims(raw map[string]any, groupsClaim string) []string {
	v, ok := raw[groupsClaim]
	if !ok {
		return nil
	}
	switch g := v.(type) {
	case []any:
		out := make([]string, 0, len(g))
		for _, e := range g {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{g}
	}
	return nil
}

// decideRole maps a user's groups to a console role. See OIDCConfig's
// OperatorGroups/ViewerGroups doc comments for the fail-safe defaults.
func decideRole(groups, operatorGroups, viewerGroups []string) (role string, ok bool) {
	if intersects(groups, operatorGroups) {
		return "operator", true
	}
	if len(viewerGroups) > 0 {
		if intersects(groups, viewerGroups) {
			return "viewer", true
		}
		return "", false
	}
	return "viewer", true
}

func intersects(a, b []string) bool {
	if len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(b))
	for _, s := range b {
		set[s] = struct{}{}
	}
	for _, s := range a {
		if _, ok := set[s]; ok {
			return true
		}
	}
	return false
}

// requireHTTPSIssuer rejects non-HTTPS issuers except loopback http
// (local IdP / tests). Empty URL is rejected by New earlier.
func requireHTTPSIssuer(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("authn: issuer_url %q is not a valid URL", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("authn: issuer_url must use https:// (got %q); http is only allowed for loopback", raw)
	default:
		return fmt.Errorf("authn: issuer_url must use https:// (got scheme %q)", u.Scheme)
	}
}
