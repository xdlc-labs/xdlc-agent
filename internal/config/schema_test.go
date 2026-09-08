package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestSchemaCoversEveryConfigField walks the Go config structs and requires
// every yaml key they accept to appear somewhere in the published schema.
//
// Why this is separate from TestSchemaExampleLoads: that test only compares
// config.example.yaml's TOP-LEVEL keys against the schema's top-level
// properties, so it cannot see a nested key at all. Every object in
// schema/config.schema.json sets "additionalProperties": false, which means a
// key the daemon accepts but the schema omits makes an editor reject a config
// the daemon is perfectly happy to load. That is exactly how agent.fix_budget
// -- documented in config.example.yaml, read by AgentConfig -- ended up
// rejected by the schema.
//
// The check is deliberately structure-insensitive: it asserts the key name
// exists in the union of every object's properties rather than under one
// specific object. That still catches an omitted key, which is the failure
// that reaches users, without hard-coding a Go-struct-to-$defs mapping that
// would need editing on every refactor.
func TestSchemaCoversEveryConfigField(t *testing.T) {
	schemaBytes, err := os.ReadFile(filepath.Join(repoRoot(t), "schema", "config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatal(err)
	}

	known := map[string]bool{}
	collectSchemaPropertyNames(schema, known)
	if len(known) == 0 {
		t.Fatal("collected no property names from the schema; the walker has rotted")
	}

	for _, key := range yamlKeysOf(reflect.TypeFor[Config](), map[reflect.Type]bool{}) {
		if !known[key] {
			t.Errorf("config field %q is accepted by the daemon but missing from schema/config.schema.json; "+
				"every schema object sets additionalProperties:false, so a config using it is flagged as invalid", key)
		}
	}
}

// collectSchemaPropertyNames gathers the keys of every "properties" object
// anywhere in the schema.
func collectSchemaPropertyNames(node any, out map[string]bool) {
	switch n := node.(type) {
	case map[string]any:
		if props, ok := n["properties"].(map[string]any); ok {
			for name := range props {
				out[name] = true
			}
		}
		for _, v := range n {
			collectSchemaPropertyNames(v, out)
		}
	case []any:
		for _, v := range n {
			collectSchemaPropertyNames(v, out)
		}
	}
}

// yamlKeysOf returns every yaml key name reachable from t, descending into
// nested structs, slices and maps. seen guards against recursive types.
func yamlKeysOf(t reflect.Type, seen map[reflect.Type]bool) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() == reflect.Map {
		return yamlKeysOf(t.Elem(), seen)
	}
	if t.Kind() != reflect.Struct || seen[t] || t == reflect.TypeFor[time.Time]() {
		return nil
	}
	seen[t] = true

	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		tag := f.Tag.Get("yaml")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if name == "" {
			// yaml.v3 lowercases the field name when there is no tag.
			name = strings.ToLower(f.Name)
		}
		keys = append(keys, name)
		keys = append(keys, yamlKeysOf(f.Type, seen)...)
	}
	return keys
}

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
