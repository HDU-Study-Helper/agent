package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestReadResultBudgetAllowsNarrowedRetryWithoutTruncating(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.EnabledTools = []string{"read_rows"}
	a.Limits.ToolResultBytes = 1024
	repo.agents[a.ID] = a
	tools := NewToolRegistry()
	executions := 0
	if err := tools.Register(ToolSpec{Name: "read_rows", Description: "Read fixture rows", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object","required":["limit"],"properties":{"limit":{"type":"integer"}}}`), OutputSchema: json.RawMessage(`{"type":"array"}`)}, func(_ context.Context, _ ExecutionContext, raw json.RawMessage) (any, error) {
		executions++
		var q struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(raw, &q)
		rows := []string{}
		for i := 0; i < q.Limit; i++ {
			rows = append(rows, strings.Repeat("x", 700))
		}
		return rows, nil
	}); err != nil {
		t.Fatal(err)
	}
	round := 0
	provider := modelFunc(func(_ context.Context, r ModelRequest) (ModelResponse, error) {
		round++
		switch round {
		case 1:
			return calls(call("large", "read_rows", `{"limit":2}`)), nil
		case 2:
			last := r.Messages[len(r.Messages)-1]
			var result ToolResult
			if json.Unmarshal([]byte(last.Content), &result) != nil || result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" || result.Data != nil || !strings.Contains(result.Error.Message, "Reduce the query limit") {
				t.Fatal("large read was not recoverable and explicit", last.Content)
			}
			return calls(call("small", "read_rows", `{"limit":1}`)), nil
		case 3:
			var result struct {
				OK   bool     `json:"ok"`
				Data []string `json:"data"`
			}
			if json.Unmarshal([]byte(r.Messages[len(r.Messages)-1].Content), &result) != nil || !result.OK || len(result.Data) != 1 || len(result.Data[0]) != 700 {
				t.Fatal("narrowed read content was truncated")
			}
			return final(`{"ok":true}`), nil
		default:
			t.Fatal("unexpected extra call")
			return ModelResponse{}, nil
		}
	})
	result, err := NewRuntime(repo, tools, map[string]ModelProvider{"mock": provider}).Run(context.Background(), "owner", RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(result, err)
	}
	record := repo.runs[result.RunID]
	if round != 3 || executions != 2 || record.ToolCalls != 2 || record.Status != "succeeded" || record.ToolExecutions[0].Error.Code != "INVALID_ARGUMENT" {
		t.Fatal(record, executions)
	}
}

func TestWriteResultBudgetRemainsTerminalAfterExecution(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.EnabledTools = []string{"write_row"}
	a.Limits.ToolResultBytes = 1024
	repo.agents[a.ID] = a
	tools := NewToolRegistry()
	writes := 0
	if err := tools.Register(ToolSpec{Name: "write_row", Description: "Write fixture", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"string"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
		writes++
		return strings.Repeat("x", 2000), nil
	}); err != nil {
		t.Fatal(err)
	}
	rounds := 0
	result, err := NewRuntime(repo, tools, map[string]ModelProvider{"mock": modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
		rounds++
		return calls(call("write", "write_row", `{}`)), nil
	})}).Run(context.Background(), "owner", RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)})
	requireCode(t, err, "EXECUTION_LIMIT_EXCEEDED")
	if writes != 1 || rounds != 1 || repo.runs[result.RunID].ToolCalls != 1 {
		t.Fatal("write was retried or not audited", writes, rounds, repo.runs[result.RunID])
	}
}

func TestBuiltinReadOnlyMetadataDoesNotMarkWrites(t *testing.T) {
	tools := NewToolRegistry()
	if err := (Builtins{}).Register(tools); err != nil {
		t.Fatal(err)
	}
	reads := map[string]bool{"memory_get": true, "memory_search": true, "memory_list": true, "memory_get_relations": true, "workflow_get": true, "workflow_list": true, "data_query": true, "data_search": true, "data_list_tables": true, "data_describe_table": true, "web_search": true, "web_fetch": true}
	for _, spec := range tools.Catalog() {
		if spec.ReadOnly != reads[spec.Name] {
			t.Fatal("incorrect tool capability metadata", spec.Name, spec.ReadOnly)
		}
	}
}
