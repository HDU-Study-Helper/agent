package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaDiagnosticsIdentifyPathsWithoutEchoingInput(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"context":{"type":"object","required":["event"],"properties":{"event":{"type":"object","required":["source","content"],"properties":{"source":{"type":"object"},"content":{"type":"string"},"state":{"enum":["active","draft"]}}}}}},"required":["context"]}`)
	registry := NewToolRegistry()
	err := registry.Register(ToolSpec{Name: "check", Description: "validation regression", InputSchema: schema, OutputSchema: json.RawMessage(`{"type":"string"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
		t.Fatal("invalid input executed")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ args, want1, want2 string }{
		{`{"context":{"event":{"content":"private-token-value"}}}`, "/context/event", "missing required field source"},
		{`{"context":{"event":{"source":"private-token-value","content":"body"}}}`, "/context/event/source", "expected object"},
		{`{"context":{"event":{"source":{},"content":"body","state":"private-token-value"}}}`, "/context/event/state", "schema enum"},
		{`{"context":{"event":{"source":{},"content":"body"}},"private-token-value":"sensitive"}`, "additionalProperties", "unexpected field"},
	} {
		result := registry.Execute(context.Background(), toolContext([]string{"check"}, nil), "check", json.RawMessage(tc.args))
		if result.OK || result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
			t.Fatal(result)
		}
		message := result.Error.Message
		if !strings.Contains(message, tc.want1) || !strings.Contains(message, tc.want2) || strings.Contains(message, "private-token-value") || strings.Contains(message, "sensitive") || len(message) > 1024 {
			t.Fatal(message)
		}
	}
}
func TestDispatchSchemaDiagnosticsSourceObject(t *testing.T) {
	compiled, err := compileSchema(dispatchInputSchema())
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"target_agent_id": "worker", "task": "check", "workflow": map[string]any{"workflow_id": "wf", "version": 1}, "context": map[string]any{"event": map[string]any{"event_id": "e", "content": []any{}, "source": "private-token-value", "evidence_refs": []any{}}}}
	message := inputSchemaError(compiled.Validate(input), dispatchInputSchema())
	if !strings.Contains(message, "/context/event/source") || !strings.Contains(message, "expected object") || strings.Contains(message, "private-token-value") {
		t.Fatal(message)
	}
}
