package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryToolReferenceComesFromValidatedResultWithoutContent(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("writer")
	a.EnabledTools = []string{"memory_create"}
	a.Permissions = []string{"memory.write"}
	repo.agents[a.ID] = a
	tools := NewToolRegistry()
	err := tools.Register(ToolSpec{Name: "memory_create", Description: "Save a test memory", Permissions: []string{"memory.write"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object","required":["memory_id","content"],"properties":{"memory_id":{"type":"string"},"content":{"type":"string"}}}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
		return map[string]string{"memory_id": "actual-memory", "content": "private memory content"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	round := 0
	runtime := NewRuntime(repo, tools, map[string]ModelProvider{"mock": modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
		round++
		if round == 1 {
			return calls(call("write", "memory_create", `{}`)), nil
		}
		return final("done"), nil
	})})
	result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	record := repo.runs[result.RunID]
	if len(record.ToolExecutions) != 1 || len(record.ToolExecutions[0].ResultRefs) != 1 || record.ToolExecutions[0].ResultRefs[0].ID != "actual-memory" {
		t.Fatal(record.ToolExecutions)
	}
	raw, _ := json.Marshal(record.ToolExecutions)
	if strings.Contains(string(raw), "private memory content") {
		t.Fatal("private content leaked into tool audit")
	}
}
func TestMemoryToolReferenceIgnoresFailureAndUnrelatedTool(t *testing.T) {
	result := ToolResult{OK: true, Data: map[string]any{"memory_id": "id"}}
	if len(toolResultRefs("data_query", result)) != 0 {
		t.Fatal("unrelated query recorded as write")
	}
	result.Error = Fail("INTERNAL_ERROR", "failed")
	if len(toolResultRefs("memory_create", result)) != 0 {
		t.Fatal("failed result recorded")
	}
	result.Error = nil
	result.OK = false
	if len(toolResultRefs("memory_update", result)) != 0 {
		t.Fatal("unsuccessful result recorded")
	}
}
