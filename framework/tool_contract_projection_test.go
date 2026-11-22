package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestModelFacingToolsDescribesOutputWithoutChangingContracts(t *testing.T) {
	r := NewToolRegistry()
	if e := (Builtins{}).Register(r); e != nil {
		t.Fatal(e)
	}
	before := r.Catalog()
	projected := ModelFacingTools(before)
	if len(projected) != 21 {
		t.Fatal(len(projected))
	}
	for i, s := range projected {
		if !strings.Contains(s.Description, toolReturnContractMarker) || !strings.Contains(s.Description, "{ok,data,error}") {
			t.Fatal(s.Name, "missing return contract")
		}
		if string(s.InputSchema) != string(before[i].InputSchema) || string(s.OutputSchema) != string(before[i].OutputSchema) {
			t.Fatal(s.Name, "changed validation shape")
		}
		if strings.Count(ModelFacingTools([]ToolSpec{s})[0].Description, toolReturnContractMarker) != 1 {
			t.Fatal("duplicated contract")
		}
		if strings.Contains(before[i].Description, toolReturnContractMarker) {
			t.Fatal("mutated registration")
		}
	}
	// No ungranted contracts are introduced by projection.
	e := toolContext([]string{"memory_get"}, []string{"memory.read"})
	visible := ModelFacingTools(r.Authorized(e))
	if len(visible) != 1 || visible[0].Name != "memory_get" {
		t.Fatal(visible)
	}
}

func TestBuiltinOutputFieldsHaveIndependentDescriptions(t *testing.T) {
	var s map[string]any
	_ = json.Unmarshal(builtinOutputSchema("memory_get"), &s)
	p := s["properties"].(map[string]any)
	a := p["memory_id"].(map[string]any)["description"]
	b := p["owner_user_id"].(map[string]any)["description"]
	if a == b || a == nil || b == nil {
		t.Fatal(a, b)
	}
}

func TestBuiltinTransformCompilesAndExecutesSpecializedSchema(t *testing.T) {
	r := NewToolRegistry()
	b := Builtins{TransformSpec: func(s ToolSpec) ToolSpec {
		if s.Name == "workflow_create" {
			var in map[string]any
			_ = json.Unmarshal(s.InputSchema, &in)
			delete(in["properties"].(map[string]any), "initial_private_memories")
			s.InputSchema, _ = json.Marshal(in)
		}
		return s
	}}
	if e := b.Register(r); e != nil {
		t.Fatal(e)
	}
	e := toolContext([]string{"workflow_create"}, []string{"workflow.write"})
	result := r.Execute(context.Background(), e, "workflow_create", json.RawMessage(`{"name":"x","definition":{},"initial_private_memories":[{"title":"x","content":"y","source_refs":["source"]}]}`))
	if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("hidden argument reached handler: %+v", result)
	}
	for _, s := range r.Authorized(e) {
		if strings.Contains(string(s.InputSchema), "initial_private_memories") {
			t.Fatal("argument still exposed")
		}
	}
}
