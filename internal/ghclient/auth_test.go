package ghclient

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
)

func TestPreferAppThenPAT_PATFallback(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_FILE", "")

	p, err := PreferAppThenPAT(config.GitHubConfig{})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := p.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ghp_test" {
		t.Errorf("token = %q, want ghp_test", tok)
	}
	if SourceKind(p) != "pat" {
		t.Errorf("SourceKind = %q, want pat", SourceKind(p))
	}
}

func TestPreferAppThenPAT_Empty(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_FILE", "")

	p, err := PreferAppThenPAT(config.GitHubConfig{})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := p.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		t.Errorf("token = %q, want empty", tok)
	}
	if SourceKind(p) != "none" {
		t.Errorf("SourceKind = %q, want none", SourceKind(p))
	}
}

func TestPreferAppThenPAT_AppFromEnv(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_TOKEN", "ghp_should_not_win")
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "67890")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_FILE", path)
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")

	p, err := PreferAppThenPAT(config.GitHubConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*AppToken); !ok {
		t.Fatalf("got %T, want *AppToken", p)
	}
	if SourceKind(p) != "app" {
		t.Errorf("SourceKind = %q, want app", SourceKind(p))
	}
}

func TestScopeRepos(t *testing.T) {
	t.Run("bare names, deduped and sorted", func(t *testing.T) {
		got, err := ScopeRepos([]config.Repo{
			{Name: "svc-b", GitHub: "acme/svc-b"},
			{Name: "svc-a", GitHub: "acme/svc-a"},
			{Name: "svc-a-alias", GitHub: "acme/svc-a"},
			{Name: "local-only"}, // no github: — nothing to scope
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"svc-a", "svc-b"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v (names, not owner/name)", got, want)
		}
	})

	t.Run("rejects two owners", func(t *testing.T) {
		_, err := ScopeRepos([]config.Repo{
			{Name: "a", GitHub: "acme/a"},
			{Name: "b", GitHub: "other-org/b"},
		})
		if err == nil {
			t.Fatal("want error: one installation token cannot span two owners")
		}
		if !strings.Contains(err.Error(), "installation_id") {
			t.Errorf("error should point at the fix, got: %v", err)
		}
	})

	t.Run("rejects malformed github field", func(t *testing.T) {
		for _, bad := range []string{"acme", "/svc", "acme/"} {
			if _, err := ScopeRepos([]config.Repo{{Name: "x", GitHub: bad}}); err == nil {
				t.Errorf("github: %q accepted, want error", bad)
			}
		}
	})

	t.Run("no repos means no narrowing", func(t *testing.T) {
		got, err := ScopeRepos(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want empty", got)
		}
	})
}

func TestPreferAppThenPAT_ScopesAppTokenToRepos(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "67890")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", string(testKeyPEM(t)))
	t.Setenv("GITHUB_APP_PRIVATE_KEY_FILE", "")

	p, err := PreferAppThenPAT(config.GitHubConfig{},
		config.Repo{Name: "svc", GitHub: "acme/example-service"},
		config.Repo{Name: "other", GitHub: "acme/other-service"},
	)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := p.(*AppToken)
	if !ok {
		t.Fatalf("got %T, want *AppToken", p)
	}
	want := []string{"example-service", "other-service"}
	if !reflect.DeepEqual(a.Repos, want) {
		t.Fatalf("Repos = %v, want %v", a.Repos, want)
	}
}

func TestPreferAppThenPAT_RejectsReposAcrossInstallations(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "67890")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", string(testKeyPEM(t)))
	t.Setenv("GITHUB_APP_PRIVATE_KEY_FILE", "")

	// Fail at startup rather than mint a token that cannot reach half
	// the fleet.
	_, err := PreferAppThenPAT(config.GitHubConfig{},
		config.Repo{Name: "a", GitHub: "acme/a"},
		config.Repo{Name: "b", GitHub: "beta-corp/b"},
	)
	if err == nil {
		t.Fatal("want error for repos under two owners")
	}
}

// installationTokenRequest is the POST body of
// /app/installations/{id}/access_tokens, as GitHub sees it.
type installationTokenRequest struct {
	Repositories []string          `json:"repositories"`
	RepositoryID []int64           `json:"repository_ids"`
	Permissions  map[string]string `json:"permissions"`
}

// TestAppTokenRequestIsScoped is the S5 regression test: the mint
// request must carry an explicit repository list and an explicit
// least-privilege permission set, never `nil` options (which yield a
// token for every repo and every permission in the installation).
func TestAppTokenRequestIsScoped(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	var bodies []installationTokenRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/42/access_tokens" {
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var got installationTokenRequest
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("unmarshal %q: %v", raw, err)
		}
		bodies = append(bodies, got)
		_, _ = fmt.Fprint(w, `{"token":"ghs_scoped","expires_at":"2099-01-01T00:00:00Z"}`)
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	a := &AppToken{
		AppID:          7,
		InstallationID: 42,
		PrivateKey:     key,
		Repos:          []string{"example-service", "other-service"},
		HTTP:           &http.Client{Transport: rewriteHost{target: target}},
	}
	tok, err := a.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ghs_scoped" {
		t.Fatalf("token = %q", tok)
	}
	if len(bodies) != 1 {
		t.Fatalf("mint requests = %d, want 1", len(bodies))
	}

	body := bodies[0]
	wantRepos := []string{"example-service", "other-service"}
	if !reflect.DeepEqual(body.Repositories, wantRepos) {
		t.Errorf("repositories = %v, want %v", body.Repositories, wantRepos)
	}
	wantPerms := map[string]string{
		"contents":      "write", // clone/fetch, Fix push, promote ff, revert
		"pull_requests": "write", // read the Fix-PR queue; fix_mode: pr opens the PR
		"actions":       "read",  // workflow runs + failed job logs
		"metadata":      "read",  // mandatory on any installation token
	}
	if !reflect.DeepEqual(body.Permissions, wantPerms) {
		t.Errorf("permissions = %v, want exactly %v", body.Permissions, wantPerms)
	}
	// Escalations the daemon must never ask for.
	for _, forbidden := range []string{"workflows", "administration", "secrets", "members", "packages", "checks", "deployments"} {
		if v, ok := body.Permissions[forbidden]; ok {
			t.Errorf("permissions[%q] = %q, must not be requested", forbidden, v)
		}
	}

	// Caching survives scoping: no second mint until expiry, and the
	// refreshed request carries the same scope.
	if _, err := a.Token(); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Fatalf("cache miss: mint requests = %d, want 1", len(bodies))
	}
	a.mu.Lock()
	a.expiry = time.Now().Add(-time.Hour)
	a.mu.Unlock()
	if _, err := a.Token(); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("mint requests = %d after expiry, want 2", len(bodies))
	}
	if !reflect.DeepEqual(bodies[1], bodies[0]) {
		t.Errorf("refreshed request %+v differs from first %+v", bodies[1], bodies[0])
	}
}

// TestAppTokenUnscopedReposOmitsField documents the fallback: with no
// repo list the request still pins permissions, and "repositories" is
// omitted rather than sent empty (an empty array is not the same thing
// to GitHub).
func TestAppTokenUnscopedReposOmitsField(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = fmt.Fprint(w, `{"token":"ghs_wide","expires_at":"2099-01-01T00:00:00Z"}`)
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	a := &AppToken{AppID: 7, InstallationID: 42, PrivateKey: key,
		HTTP: &http.Client{Transport: rewriteHost{target: target}}}
	if _, err := a.Token(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "repositories") {
		t.Errorf("body should omit repositories when unscoped, got %s", raw)
	}
	if !strings.Contains(string(raw), `"contents":"write"`) {
		t.Errorf("body should still pin permissions, got %s", raw)
	}
}

func testKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func TestAppJWTRoundTripShape(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	jwt, err := appJWT(99, key)
	if err != nil {
		t.Fatal(err)
	}
	parts := 0
	for i := 0; i < len(jwt); i++ {
		if jwt[i] == '.' {
			parts++
		}
	}
	if parts != 2 {
		t.Fatalf("jwt should have 3 segments (2 dots), got %q", jwt)
	}
}
