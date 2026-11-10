package framework

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRuntimeRetainsCommittedMemoryRefWhenLaterToolFails(t *testing.T) {
	for _, code := range []string{"TIMEOUT", "EXECUTION_LIMIT_EXCEEDED", "CANCELLED", ""} {
		t.Run(code, func(t *testing.T) {
			repo := newFakeRepo()
			a := fixtureAgent("worker")
			a.EnabledTools = []string{"memory_create", "next", "never"}
			repo.agents[a.ID] = a
			tools := NewToolRegistry()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writes, nextCalls, neverCalls := 0, 0, 0
			register := func(name string, output json.RawMessage, handler ToolHandler) {
				t.Helper()
				if err := tools.Register(ToolSpec{Name: name, Description: "audit regression", InputSchema: object(map[string]any{}), OutputSchema: output}, handler); err != nil {
					t.Fatal(err)
				}
			}
			register("memory_create", json.RawMessage(`{"type":"object","properties":{"memory_id":{"type":"string"}},"required":["memory_id"]}`), func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
				writes++
				return map[string]string{"memory_id": "committed-memory"}, nil
			})
			register("next", json.RawMessage(`{"type":"string"}`), func(callCtx context.Context, _ ExecutionContext, _ json.RawMessage) (any, error) {
				nextCalls++
				switch code {
				case "TIMEOUT":
					deadlineCtx, deadlineCancel := context.WithTimeout(callCtx, time.Nanosecond)
					defer deadlineCancel()
					<-deadlineCtx.Done()
					return nil, deadlineCtx.Err()
				case "EXECUTION_LIMIT_EXCEEDED":
					return nil, Fail(code, "result budget exceeded")
				case "CANCELLED":
					cancel()
					return nil, context.Canceled
				default:
					return "ok", nil
				}
			})
			register("never", json.RawMessage(`{"type":"string"}`), func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
				neverCalls++
				return "ok", nil
			})
			modelCalls := 0
			runtime := NewRuntime(repo, tools, map[string]ModelProvider{"mock": modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
				modelCalls++
				if modelCalls == 1 {
					return calls(call("write", "memory_create", `{}`), call("next", "next", `{}`), call("last", "never", `{}`)), nil
				}
				return final("done"), nil
			})})
			result, err := runtime.Run(ctx, "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: json.RawMessage(`{}`)})
			if code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireCode(t, err, code)
			}
			record := repo.runs[result.RunID]
			wantCount := 2
			if code == "" {
				wantCount = 3
			}
			if writes != 1 || nextCalls != 1 || len(record.ToolExecutions) != wantCount || record.ToolCalls != wantCount {
				t.Fatalf("missing or duplicate execution audit: writes=%d next=%d record=%+v", writes, nextCalls, record)
			}
			first := record.ToolExecutions[0]
			if first.Name != "memory_create" || first.Error != nil || len(first.ResultRefs) != 1 || first.ResultRefs[0] != (ToolResultRef{Kind: "MEMORY", ID: "committed-memory"}) {
				t.Fatalf("committed memory receipt lost: %+v", first)
			}
			if code != "" && (neverCalls != 0 || record.Status != "failed" || record.ToolExecutions[1].Error == nil || record.ToolExecutions[1].Error.Code != code) {
				t.Fatalf("failed batch continued or lost failure audit: %+v", record)
			}
		})
	}
}
