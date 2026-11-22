package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type memoryIndexTestRepo struct {
	Repository
	memory Memory
}

func (r memoryIndexTestRepo) SearchMemory(_ context.Context, s Scope, q MemorySearch) ([]Memory, error) {
	if s.OwnerID != "owner" || s.WorkflowID != "workflow" || q.WorkflowID != s.WorkflowID || q.Limit != 50 {
		return nil, Fail("INVALID_ARGUMENT", "lost scope or search defaults")
	}
	return []Memory{r.memory}, nil
}

func (r memoryIndexTestRepo) GetMemory(_ context.Context, _ Scope, _ string) (Memory, error) {
	return r.memory, nil
}

func TestMemoryListReturnsCompactIndexButGetAndSearchKeepDetails(t *testing.T) {
	m := Memory{ID: "memory", OwnerID: "owner", Visibility: "PRI", WorkflowID: "workflow", Title: strings.Repeat("课", 200), Category: strings.Repeat("类", 40), Content: strings.Repeat("full body ", 50), Status: "active", Level: "L1", MemoryType: "BASE", SourceRefs: json.RawMessage(`["source-id"]`), StructuredContent: json.RawMessage(`{"deadline":"tomorrow"}`)}
	r := NewToolRegistry()
	if err := (Builtins{Repository: memoryIndexTestRepo{memory: m}}).Register(r); err != nil {
		t.Fatal(err)
	}
	exec := ExecutionContext{Scope: Scope{OwnerID: "owner", WorkflowID: "workflow"}, EnabledTools: []string{"memory_list", "memory_search", "memory_get"}, Permissions: []string{"memory.read"}, Limits: Limits{}.Defaults(), budget: &budget{tools: 3}}
	args := json.RawMessage(`{"owner_workflow_id":"workflow"}`)
	result := r.Execute(context.Background(), exec, "memory_list", args)
	if !result.OK {
		t.Fatal(result.Error)
	}
	raw, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]string
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 1 {
		t.Fatal(string(raw), err)
	}
	row := list[0]
	if len(row) != 4 || row["memory_id"] != m.ID || row["status"] != m.Status || len([]rune(row["title"])) != 60 || len([]rune(row["category"])) != 24 || len(raw) > 360 {
		t.Fatal("expected compact index only", row, len(raw))
	}
	for name, input := range map[string]json.RawMessage{"memory_get": json.RawMessage(`{"memory_id":"memory"}`), "memory_search": args} {
		result := r.Execute(context.Background(), exec, name, input)
		raw, _ := json.Marshal(result.Data)
		if !result.OK || !strings.Contains(string(raw), m.Content) || !strings.Contains(string(raw), "source-id") || !strings.Contains(string(raw), "deadline") {
			t.Fatal(name, "did not return full details", result.Error)
		}
	}
}
