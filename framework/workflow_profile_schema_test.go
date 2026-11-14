package framework

import (
	"encoding/json"
	"testing"
)

func TestWorkflowCreateOptionalProfileMetadata(t *testing.T) {
	var schema json.RawMessage
	if err := (Builtins{}).registerWorkflows(func(name, _, _ string, s json.RawMessage, _ ToolHandler) error {
		if name == "workflow_create" {
			schema = s
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	compiled, err := compileSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, metadata := range []any{nil, map[string]any{}, map[string]any{"search_coverage": []any{map[string]any{"dataset": "schedules", "complete": false}}, "dimensions": map[string]any{"identity": map[string]any{"status": "partial"}}}, map[string]any{"version": 2, "query_coverage": "legacy notes"}} {
		raw, _ := json.Marshal(map[string]any{"name": "course", "definition": map[string]any{"profile_manifest": metadata}})
		var input any
		_ = json.Unmarshal(raw, &input)
		if err := compiled.Validate(input); err != nil {
			t.Fatal(err)
		}
	}
	raw := `{"name":"course","definition":{"profile_manifest":"invalid shape"}}`
	var input any
	_ = json.Unmarshal([]byte(raw), &input)
	if compiled.Validate(input) == nil {
		t.Fatal("non-object metadata accepted")
	}
}
