package framework

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type cleaningRuntimeData struct{ investigationRuntimeData }

func (*cleaningRuntimeData) Catalog(context.Context, Scope) ([]Dataset, error) {
	return []Dataset{{Name: "records", Fields: []Field{{Name: "id"}, {Name: "parent_id"}, {Name: "native_id"}, {Name: "metadata"}, {Name: "text"}, {Name: "title"}}}, {Name: "academic_items", Fields: []Field{{Name: "id"}, {Name: "parent_id"}, {Name: "native_id"}, {Name: "metadata"}, {Name: "text"}, {Name: "title"}}}}, nil
}

func TestRuntimePreservesToolDataWithoutImplicitBusinessCleaning(t *testing.T) {
	for _, role := range []string{"learner", "filter", "worker", "custom"} {
		t.Run(role, func(t *testing.T) {
			rows := []map[string]any{
				{"id": "60619", "parent_id": "topic-24", "native_id": "reply-1", "metadata": `{"is_current_user":true,"count":0,"unknown":null,"state":{}}`, "text": "", "title": "", "unknown": nil, "empty": map[string]any{}, "items": []any{}},
				{"id": "other", "parent_id": "topic-24", "metadata": `{"is_current_user":false,"count":0}`},
			}
			tools := NewToolRegistry()
			if err := tools.Register(ToolSpec{Name: "data_query", Description: "test data", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
				return map[string]any{"rows": rows, "has_more": false}, nil
			}); err != nil {
				t.Fatal(err)
			}
			base := newFakeRepo()
			a := fixtureAgent("agent")
			a.Type, a.EnabledTools = role, []string{"data_query"}
			base.agents[a.ID] = a
			round := 0
			model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
				round++
				if round == 1 {
					return calls(call("q", "data_query", `{}`)), nil
				}
				got := investigationToolResult(t, req, "q")
				wantRaw, _ := json.Marshal(map[string]any{"rows": rows, "has_more": false})
				var want any
				_ = json.Unmarshal(wantRaw, &want)
				if !got.OK || !reflect.DeepEqual(got.Data, want) {
					t.Fatalf("tool evidence changed: got %#v want %#v", got.Data, want)
				}
				return final("done"), nil
			})
			runtime := NewRuntime(base, tools, map[string]ModelProvider{"mock": model})
			result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: json.RawMessage(`{}`)})
			if err != nil || result.Output != "done" || round != 2 {
				t.Fatal(result, err, round)
			}
		})
	}
}
