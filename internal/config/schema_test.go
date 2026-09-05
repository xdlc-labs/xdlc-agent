package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSchemaExampleLoads: config.example.yaml parses; top-level keys ⊆ schema properties.
// ponytail: allowlist from schema properties, not full JSON Schema engine.
func TestSchemaExampleLoads(t *testing.T) {
	root := repoRoot(t)
	examplePath := filepath.Join(root, "config.example.yaml")
	if _, err := Load(examplePath); err != nil {
		t.Fatalf("Load config.example.yaml: %v", err)
	}

	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	schemaBytes, err := os.ReadFile(filepath.Join(root, "schema", "config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties) == 0 {
		t.Fatal("schema has no top-level properties")
	}
	for key := range doc {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("config.example.yaml key %q not in schema properties", key)
		}
	}
	for _, want := range []string{"repos", "server", "gates", "agent"} {
		if _, ok := doc[want]; !ok {
			t.Errorf("config.example.yaml missing expected key %q", want)
		}
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
