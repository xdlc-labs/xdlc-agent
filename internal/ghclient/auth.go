package ghclient

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v90/github"
	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"golang.org/x/oauth2"
)

// TokenProvider returns a GitHub token for API and git auth.
type TokenProvider interface {
	Token() (string, error)
}

// StaticToken is a PAT (GITHUB_TOKEN) that never expires.
type StaticToken string

// Token implements TokenProvider.
func (s StaticToken) Token() (string, error) { return string(s), nil }

// EmptyToken is unauthenticated (public read only).
type EmptyToken struct{}

// Token implements TokenProvider.
func (EmptyToken) Token() (string, error) { return "", nil }

// AppToken mints short-lived GitHub App installation tokens.
type AppToken struct {
	AppID          int64
	InstallationID int64
	PrivateKey     *rsa.PrivateKey
	HTTP           *http.Client

	// Repos narrows every minted token to these repositories. They are
	// bare repository *names* — the "name" half of "owner/name" —
	// because that is what POST
	// /app/installations/{id}/access_tokens accepts in its
	// "repositories" field; sending "owner/name" is a 422. Build it
	// with ScopeRepos, which also rejects the cases one installation
	// token cannot cover.
	//
	// Empty means installation-wide: access to every repository the App
	// is installed on. That is the blast radius we are minimising here,
	// so leave it empty only when the caller genuinely has no repo list.
	Repos []string

	mu     sync.Mutex
	cached string
	expiry time.Time
}

// ScopeRepos maps configured repos to the bare repository names an
// installation token can be scoped to (AppToken.Repos).
//
// One installation belongs to exactly one GitHub account, so a single
// installation token can never span two owners. config.GitHubConfig
// carries a single installation_id, so repos under a second owner would
// silently mint a token that 422s (or, worse, covers only some of them):
// reject that at startup instead. Repos with no github: field are
// skipped — they contribute no GitHub access requirement.
func ScopeRepos(repos []config.Repo) ([]string, error) {
	var owner string
	seen := make(map[string]struct{}, len(repos))
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		if r.GitHub == "" {
			continue
		}
		o, n, ok := strings.Cut(r.GitHub, "/")
		if !ok || o == "" || n == "" {
			return nil, fmt.Errorf("ghclient: repo %q: github must be \"owner/name\", got %q", r.Name, r.GitHub)
		}
		if owner == "" {
			owner = o
		} else if !strings.EqualFold(o, owner) {
			return nil, fmt.Errorf(
				"ghclient: repos span two GitHub owners (%s and %s); one App installation covers a single account, so one installation token cannot reach both — split them into separate deployments with their own github.installation_id",
				owner, o)
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}
	sort.Strings(names) // deterministic request body
	return names, nil
}

// tokenPermissions is the least-privilege permission set xdlc actually
// uses. An installation token defaults to *every* permission the App
// holds; this pins it to what the code calls, so a prompt-injected
// subagent that gets hold of the token (docs/threat-model.md) cannot
// reach anything else the App was granted.
//
// A permission the App does not hold is simply absent from the minted
// token — GitHub intersects, it does not error — so listing one here is
// a ceiling, never an escalation.
func tokenPermissions() *github.InstallationPermissions {
	read, write := "read", "write"
	return &github.InstallationPermissions{
		// contents:write — clone/fetch/reset (repos.Manager.EnsureCloned,
		// via the http.extraHeader credential in repos.AuthEnv), the
		// subagent's Fix commit+push, promote's ff-only develop→main push
		// and revert's push to the prod branch. The one write the loop
		// cannot do without.
		Contents: &write,
		// pull_requests:write — reading PRs for the Fix-PR work queue
		// (Client.GetPR / FindPRByBranch) needs only read, but
		// fix_mode: pr has the subagent *open* the PR with this same
		// credential, so read alone would break that mode.
		PullRequests: &write,
		// actions:read — the CI gate's latest workflow_run conclusion
		// (Client.GetStatus) plus the failed job list and job-log
		// download that feed the Fix prompt
		// (Client.FetchFailedJobLogs).
		Actions: &read,
		// metadata:read — mandatory on every installation token; listed
		// explicitly so the request is self-documenting.
		Metadata: &read,

		// Deliberately NOT requested:
		//   workflows:write — a Fix that edits .github/workflows/* will
		//     be refused by the push. That is the intent: rewriting CI
		//     is exactly what an injected agent would do to exfiltrate
		//     secrets, and the daemon never needs it.
		//   checks, statuses, deployments, secrets, administration,
		//   members, packages — no code path touches them.
	}
}

// Token implements TokenProvider and oauth2.TokenSource.
func (a *AppToken) Token() (string, error) {
	tok, err := a.token()
	if err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

// oauth2.TokenSource
func (a *AppToken) token() (*oauth2.Token, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cached != "" && time.Now().Before(a.expiry.Add(-2*time.Minute)) {
		return &oauth2.Token{AccessToken: a.cached, Expiry: a.expiry}, nil
	}

	jwt, err := appJWT(a.AppID, a.PrivateKey)
	if err != nil {
		return nil, err
	}
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	httpClient := oauth2.NewClient(context.Background(), src)
	if a.HTTP != nil {
		base := a.HTTP.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		httpClient = &http.Client{Transport: &oauth2.Transport{Source: src, Base: base}}
	}
	client := mustClient(github.WithHTTPClient(httpClient))
	// Narrow the token before it exists: repositories from config.Repos
	// (nil → installation-wide, see AppToken.Repos) and only the
	// permissions the loop uses. The scope is fixed for the life of an
	// AppToken, so the cache above stays valid across refreshes.
	it, _, err := client.Apps.CreateInstallationToken(context.Background(), a.InstallationID,
		&github.InstallationTokenOptions{
			Repositories: a.Repos,
			Permissions:  tokenPermissions(),
		})
	if err != nil {
		return nil, fmt.Errorf("ghclient: create installation token: %w", err)
	}
	a.cached = it.GetToken()
	a.expiry = it.GetExpiresAt().Time
	if a.expiry.IsZero() {
		a.expiry = time.Now().Add(50 * time.Minute)
	}
	return &oauth2.Token{AccessToken: a.cached, Expiry: a.expiry}, nil
}

// PreferAppThenPAT builds a TokenProvider: GitHub App if configured,
// else GITHUB_TOKEN PAT, else empty.
//
// repos are the configured repos (pass cfg.Repos...) and scope the
// minted App installation token to just those repositories; see
// ScopeRepos and AppToken.Repos. Passing none still scopes the token's
// *permissions* (tokenPermissions) but leaves it installation-wide,
// which is strictly worse — always pass the repo list when you have it.
// It is variadic only so existing call sites keep compiling.
func PreferAppThenPAT(cfg config.GitHubConfig, repos ...config.Repo) (TokenProvider, error) {
	appID := cfg.AppID
	if appID == 0 {
		if v := os.Getenv("GITHUB_APP_ID"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("ghclient: GITHUB_APP_ID: %w", err)
			}
			appID = n
		}
	}
	instID := cfg.InstallationID
	if instID == 0 {
		if v := os.Getenv("GITHUB_APP_INSTALLATION_ID"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("ghclient: GITHUB_APP_INSTALLATION_ID: %w", err)
			}
			instID = n
		}
	}

	keyPEM, err := loadAppPrivateKey(cfg)
	if err != nil {
		return nil, err
	}

	if appID != 0 && instID != 0 && keyPEM != nil {
		scoped, err := ScopeRepos(repos)
		if err != nil {
			return nil, err
		}
		return &AppToken{AppID: appID, InstallationID: instID, PrivateKey: keyPEM, Repos: scoped}, nil
	}

	if pat := os.Getenv("GITHUB_TOKEN"); pat != "" {
		return StaticToken(pat), nil
	}
	return EmptyToken{}, nil
}

func loadAppPrivateKey(cfg config.GitHubConfig) (*rsa.PrivateKey, error) {
	keyEnv := cfg.PrivateKeyEnv
	if keyEnv == "" {
		keyEnv = "GITHUB_APP_PRIVATE_KEY"
	}
	fileEnv := cfg.PrivateKeyFileEnv
	if fileEnv == "" {
		fileEnv = "GITHUB_APP_PRIVATE_KEY_FILE"
	}

	var pemBytes []byte
	if p := os.Getenv(fileEnv); p != "" {
		b, err := os.ReadFile(p) //nolint:gosec // path from operator-controlled env
		if err != nil {
			return nil, fmt.Errorf("ghclient: read %s: %w", fileEnv, err)
		}
		pemBytes = b
	} else if v := os.Getenv(keyEnv); v != "" {
		pemBytes = []byte(v)
	} else {
		return nil, nil
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("ghclient: no PEM block in App private key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsed, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("ghclient: parse App private key: %w", err)
		}
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("ghclient: App private key is not RSA")
		}
	}
	return key, nil
}

// appJWT builds a short-lived RS256 JWT for GitHub App authentication.
// ponytail: stdlib JWT only — ceiling is App auth; upgrade to golang-jwt if claims grow.
func appJWT(appID int64, key *rsa.PrivateKey) (string, error) {
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(appID, 10),
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := header + "." + payload
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("ghclient: sign JWT: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// NewFromProvider builds a Client from TokenProvider (App or PAT).
func NewFromProvider(p TokenProvider) *Client {
	if p == nil {
		return New("")
	}
	switch t := p.(type) {
	case EmptyToken:
		return New("")
	case StaticToken:
		return New(string(t))
	case *AppToken:
		ts := oauth2.ReuseTokenSource(nil, oauth2TokenSource{a: t})
		return &Client{gh: mustClient(github.WithHTTPClient(oauth2.NewClient(context.Background(), ts))), Branch: "develop"}
	default:
		tok, err := p.Token()
		if err != nil || tok == "" {
			return New("")
		}
		return New(tok)
	}
}

type oauth2TokenSource struct{ a *AppToken }

func (o oauth2TokenSource) Token() (*oauth2.Token, error) { return o.a.token() }

// SourceKind returns a short label for logging (app|pat|none).
func SourceKind(p TokenProvider) string {
	switch p.(type) {
	case *AppToken:
		return "app"
	case StaticToken:
		return "pat"
	case EmptyToken:
		return "none"
	default:
		tok, _ := p.Token()
		if tok == "" {
			return "none"
		}
		return "pat"
	}
}
