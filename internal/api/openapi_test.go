package api

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestOpenAPICoversMount fails when Mount adds a /api path missing from openapi.yaml.
// ponytail: string/path check, not full OpenAPI validate — upgrade if codegen lands.
func TestOpenAPICoversMount(t *testing.T) {
	root := repoRoot(t)
	mountSrc, err := os.ReadFile(filepath.Join(root, "internal", "api", "api.go"))
	if err != nil {
		t.Fatal(err)
	}
	openapi, err := os.ReadFile(filepath.Join(root, "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(openapi)

	// Mount lines: mux.HandleFunc("GET /api/health", …) / mux.Handle("POST /api/…", …)
	re := regexp.MustCompile(`(?:HandleFunc|Handle)\("(GET|POST) (/api/[^"]+)"`)
	matches := re.FindAllStringSubmatch(string(mountSrc), -1)
	if len(matches) == 0 {
		t.Fatal("no Mount routes found in api.go")
	}
	seen := map[string]bool{}
	for _, m := range matches {
		path := m[2]
		// OpenAPI uses {id}; Go ServeMux uses {id} too — same form.
		if !strings.Contains(doc, path) {
			t.Errorf("openapi/openapi.yaml missing Mount path %s", path)
		}
		seen[path] = true
	}
	if len(seen) < 10 {
		t.Fatalf("expected ≥10 Mount routes, got %d", len(seen))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
