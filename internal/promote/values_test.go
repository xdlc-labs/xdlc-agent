package promote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeValues lays out gitops/values/{dev,prod} under a fresh temp repo.
// A body of "" means "do not create that file".
func writeValues(t *testing.T, service, devBody, prodBody string) string {
	t.Helper()
	dir := t.TempDir()
	for env, body := range map[string]string{"dev": devBody, "prod": prodBody} {
		sub := filepath.Join(dir, "gitops", "values", env)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(sub, service+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readProdFile(t *testing.T, repoDir, service string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoDir, "gitops", "values", "prod", service+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestCarryProdTagRewritesOnlyImageTag is the production bug: the carry
// used to replace *every* line matching `^\s*tag:`, so a sidecar, an
// init container or a sub-chart image got pointed at this service's
// image SHA — a tag that does not exist in their repository — and landed
// in ImagePullBackOff in prod under an audit row saying ok=true.
func TestCarryProdTagRewritesOnlyImageTag(t *testing.T) {
	const devBody = `image:
  repository: ghcr.io/xdlc-labs/example-service
  tag: sha-dev-9f1c2ab
`
	const prodBody = `image:
  repository: ghcr.io/xdlc-labs/example-service
  tag: sha-prod-1111111
replicaCount: 3
sidecar:
  image:
    repository: ghcr.io/otel/collector
    tag: v0.104.0
initContainers:
  - name: migrate
    image:
      tag: migrate-2.1.0
subchart:
  agent:
    image:
      tag: "agent-7.7.7"
`
	const want = `image:
  repository: ghcr.io/xdlc-labs/example-service
  tag: sha-dev-9f1c2ab
replicaCount: 3
sidecar:
  image:
    repository: ghcr.io/otel/collector
    tag: v0.104.0
initContainers:
  - name: migrate
    image:
      tag: migrate-2.1.0
subchart:
  agent:
    image:
      tag: "agent-7.7.7"
`
	dir := writeValues(t, "example-service", devBody, prodBody)

	c, err := CarryProdTag(dir, "example-service")
	if err != nil {
		t.Fatalf("CarryProdTag: %v", err)
	}
	if c.Status != CarryUpdated || !c.Changed() {
		t.Fatalf("status = %q, want %q", c.Status, CarryUpdated)
	}
	if c.Tag != "sha-dev-9f1c2ab" || c.PrevTag != "sha-prod-1111111" {
		t.Errorf("carry = %+v, want tag sha-dev-9f1c2ab from sha-prod-1111111", c)
	}
	if got := readProdFile(t, dir, "example-service"); got != want {
		t.Errorf("prod values:\n%s\nwant:\n%s", got, want)
	}

	// Idempotent: a second carry has nothing to do.
	c, err = CarryProdTag(dir, "example-service")
	if err != nil {
		t.Fatalf("second CarryProdTag: %v", err)
	}
	if c.Status != CarryCurrent || c.Changed() {
		t.Errorf("second carry status = %q, want %q", c.Status, CarryCurrent)
	}
}

// TestReadTagIgnoresEarlierTagLines: the reads feed the promote_requires
// min_tag pins. Taking the first `tag:` in the file fed a sidecar's
// version to a SemVer comparison against the service's image tag.
func TestReadTagIgnoresEarlierTagLines(t *testing.T) {
	const body = `# a sidecar declared before the service's own image
sidecar:
  image:
    tag: v0.104.0
image:
  repository: ghcr.io/org/svc
  tag: %s
`
	dir := writeValues(t, "svc",
		strings.Replace(body, "%s", "1.4.0", 1),
		strings.Replace(body, "%s", "1.3.0", 1))

	dev, err := ReadDevTag(dir, "svc")
	if err != nil {
		t.Fatalf("ReadDevTag: %v", err)
	}
	if dev != "1.4.0" {
		t.Errorf("ReadDevTag = %q, want 1.4.0 (the image.tag, not the sidecar's)", dev)
	}
	prod, err := ReadProdTag(dir, "svc")
	if err != nil {
		t.Fatalf("ReadProdTag: %v", err)
	}
	if prod != "1.3.0" {
		t.Errorf("ReadProdTag = %q, want 1.3.0 (the image.tag, not the sidecar's)", prod)
	}
}

// TestCarryProdTagPreservesFileBytes is the whitespace bug: the old
// regexp ended in `\s*$` under (?m), so it swallowed the newline after
// the tag line and never re-emitted it. Every promote deleted a blank
// line and stripped the trailing newline, which fights an
// end-of-file-fixer hook or a YAML lint job on the promote commit
// itself. Everything except the tag token must come through byte for
// byte.
func TestCarryProdTagPreservesFileBytes(t *testing.T) {
	// Deliberately awkward: a leading comment, blank lines around the
	// tag, mixed indent widths, a trailing comment on the tag line, and
	// a trailing newline. A yaml.Node marshal round trip normalises most
	// of this; the promote commit must not.
	const prodBody = "# Managed by GitOps. Do not edit by hand.\n" +
		"\n" +
		"image:\n" +
		"    repository: ghcr.io/org/svc\n" +
		"    tag: \"sha-prod-0000000\"  # set by xdlc promote\n" +
		"\n" +
		"replicaCount: 3\n" +
		"\n" +
		"resources:\n" +
		"  limits:\n" +
		"    cpu: 500m\n"
	const want = "# Managed by GitOps. Do not edit by hand.\n" +
		"\n" +
		"image:\n" +
		"    repository: ghcr.io/org/svc\n" +
		"    tag: \"sha-dev-abc1234\"  # set by xdlc promote\n" +
		"\n" +
		"replicaCount: 3\n" +
		"\n" +
		"resources:\n" +
		"  limits:\n" +
		"    cpu: 500m\n"

	dir := writeValues(t, "svc", "image:\n  tag: sha-dev-abc1234\n", prodBody)
	if _, err := CarryProdTag(dir, "svc"); err != nil {
		t.Fatalf("CarryProdTag: %v", err)
	}
	got := readProdFile(t, dir, "svc")
	if got != want {
		t.Errorf("prod values not byte-preserved.\n got %q\nwant %q", got, want)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("trailing newline stripped")
	}
}

// TestCarryProdTagQuoteStyles: whatever quoting the file already uses is
// what it keeps, and a trailing comment survives.
func TestCarryProdTagQuoteStyles(t *testing.T) {
	cases := []struct {
		name, prod, want string
	}{
		{"plain", "image:\n  tag: old-1\n", "image:\n  tag: sha-dev-abc1234\n"},
		{"double", "image:\n  tag: \"old-1\"\n", "image:\n  tag: \"sha-dev-abc1234\"\n"},
		{"single", "image:\n  tag: 'old-1'\n", "image:\n  tag: 'sha-dev-abc1234'\n"},
		{"comment", "image:\n  tag: old-1 # pinned\n", "image:\n  tag: sha-dev-abc1234 # pinned\n"},
		{"flow-parent", "image: {repository: r, tag: old-1}\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeValues(t, "svc", "image:\n  tag: sha-dev-abc1234\n", tc.prod)
			_, err := CarryProdTag(dir, "svc")
			if tc.want == "" {
				// A flow mapping cannot be spliced safely; the carry must
				// refuse rather than write a corrupt values file.
				if err == nil {
					t.Fatalf("expected an error, prod values now:\n%s", readProdFile(t, dir, "svc"))
				}
				if got := readProdFile(t, dir, "svc"); got != tc.prod {
					t.Errorf("prod values modified despite the error: %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CarryProdTag: %v", err)
			}
			if got := readProdFile(t, dir, "svc"); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCarryProdTagNoValuesFile is the silent-no-op bug: dispatch passes
// repos[].name as the service, so a values file named after the service
// instead ("checkout-api.yaml" for repos[].name "checkout") was never
// found, no tag was carried, prod was fast-forwarded anyway and the
// audit row said ok=true with no warning anywhere.
//
// It stays a non-error — a repo that promotes by fast-forward alone is a
// supported topology — but the outcome is now named, and Siblings points
// straight at the misnamed file.
func TestCarryProdTagNoValuesFile(t *testing.T) {
	dir := writeValues(t, "checkout-api",
		"image:\n  tag: sha-dev-9f1c2ab\n",
		"image:\n  tag: sha-prod-old000\n")

	c, err := CarryProdTag(dir, "checkout")
	if err != nil {
		t.Fatalf("CarryProdTag: %v", err)
	}
	if c.Status != CarryNoValues {
		t.Fatalf("status = %q, want %q", c.Status, CarryNoValues)
	}
	if c.Changed() {
		t.Error("Changed() true with no values file")
	}
	if c.Tag != "" {
		t.Errorf("Tag = %q, want empty", c.Tag)
	}
	if c.ProdPath != filepath.Join("gitops", "values", "prod", "checkout.yaml") {
		t.Errorf("ProdPath = %q", c.ProdPath)
	}
	if strings.Join(c.Siblings, ",") != "checkout-api.yaml" {
		t.Errorf("Siblings = %v, want [checkout-api.yaml] so the mismatch is diagnosable", c.Siblings)
	}
	// The misnamed prod file must not have been touched.
	if got := readProdFile(t, dir, "checkout-api"); !strings.Contains(got, "sha-prod-old000") {
		t.Errorf("unrelated values file rewritten: %s", got)
	}
}

// TestCarryProdTagHalfConfigured: one side present is a broken carry
// configuration, not a topology. The repo does pin its image in git but
// the tag has nowhere to go, so fast-forwarding would advertise a
// promote that did not happen — refuse instead.
func TestCarryProdTagHalfConfigured(t *testing.T) {
	for _, tc := range []struct{ name, dev, prod string }{
		{"prod missing", "image:\n  tag: sha-dev-abc1234\n", ""},
		{"dev missing", "", "image:\n  tag: sha-prod-000\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeValues(t, "svc", tc.dev, tc.prod)
			c, err := CarryProdTag(dir, "svc")
			if err == nil {
				t.Fatalf("CarryProdTag succeeded, status %q", c.Status)
			}
			if !strings.Contains(err.Error(), "gitops/values") {
				t.Errorf("error %q does not name the values paths", err)
			}
		})
	}
}

func TestCarryProdTagNoImageTagKey(t *testing.T) {
	dir := writeValues(t, "svc",
		"image:\n  tag: sha-dev-abc1234\n",
		"replicaCount: 3\nsidecar:\n  image:\n    tag: v1\n")
	if _, err := CarryProdTag(dir, "svc"); err == nil {
		t.Fatal("expected an error for a prod values file with no image.tag")
	} else if !strings.Contains(err.Error(), "image.tag") {
		t.Errorf("error %q should name image.tag", err)
	}
	if got := readProdFile(t, dir, "svc"); strings.Contains(got, "sha-dev-abc1234") {
		t.Errorf("sidecar tag clobbered: %s", got)
	}
}

func TestSetImageTagRefusesUnsafeTags(t *testing.T) {
	const body = "image:\n  tag: old\n"
	for _, tag := range []string{"", `a"b`, "a b", "a#b", `a\b`, "-flag", "{a}"} {
		if _, err := setImageTag([]byte(body), tag); err == nil {
			t.Errorf("setImageTag accepted %q", tag)
		}
	}
	if _, err := setImageTag([]byte(body), "sha256:0f1e2d"); err != nil {
		t.Errorf("setImageTag rejected a digest-style tag: %v", err)
	}
}

func TestReadTagMissingFile(t *testing.T) {
	dir := t.TempDir()
	for _, fn := range []func(string, string) (string, error){ReadDevTag, ReadProdTag} {
		got, err := fn(dir, "svc")
		if err != nil || got != "" {
			t.Errorf("missing values file: got %q, %v; want \"\", nil", got, err)
		}
	}
}
