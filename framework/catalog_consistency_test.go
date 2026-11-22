package framework

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestGeneratedToolCatalogsMatchActualRegistrations(t *testing.T) {
	registry := NewToolRegistry()
	if err := (Builtins{}).Register(registry); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ path, prefix string }{{"tool-catalog.json", ""}, {"memory-tool-schemas.json", "memory_"}} {
		actual := []ToolSpec{}
		for _, s := range registry.Catalog() {
			if strings.HasPrefix(s.Name, fixture.prefix) {
				actual = append(actual, s)
			}
		}
		raw, err := os.ReadFile(fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		var saved any
		if json.Unmarshal(raw, &saved) != nil {
			t.Fatal("invalid snapshot", fixture.path)
		}
		encoded, _ := json.Marshal(actual)
		var normalized any
		_ = json.Unmarshal(encoded, &normalized)
		left, _ := json.Marshal(normalized)
		right, _ := json.Marshal(saved)
		if string(left) != string(right) {
			t.Errorf("%s differs from registration; regenerate with cmd/tool-catalog (prefix %q)", fixture.path, fixture.prefix)
		}
	}
}

func TestAllBuiltinDeclaredInputAndOutputPropertiesAreDescribed(t *testing.T) {
	registry := NewToolRegistry()
	if err := (Builtins{}).Register(registry); err != nil {
		t.Fatal(err)
	}
	for _, spec := range registry.Catalog() {
		for _, entry := range []struct {
			kind string
			raw  json.RawMessage
		}{{"input", spec.InputSchema}, {"output", spec.OutputSchema}} {
			var node map[string]any
			if err := json.Unmarshal(entry.raw, &node); err != nil {
				t.Fatal(err)
			}
			checkDeclaredDescriptions(t, spec.Name+"."+entry.kind, node)
		}
	}
}
func checkDeclaredDescriptions(t *testing.T, path string, node map[string]any) {
	t.Helper()
	if props, ok := node["properties"].(map[string]any); ok {
		for key, value := range props {
			child, ok := value.(map[string]any)
			if !ok {
				t.Fatal(path, key, "not schema")
			}
			if description, ok := child["description"].(string); !ok || strings.TrimSpace(description) == "" {
				t.Errorf("missing description: %s.%s", path, key)
			}
			checkDeclaredDescriptions(t, path+"."+key, child)
		}
	}
	if child, ok := node["items"].(map[string]any); ok {
		checkDeclaredDescriptions(t, path+"[]", child)
	}
	for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
		if branches, ok := node[keyword].([]any); ok {
			for _, value := range branches {
				if child, ok := value.(map[string]any); ok {
					checkDeclaredDescriptions(t, path+"."+keyword, child)
				}
			}
		}
	}
}
