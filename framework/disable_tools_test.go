package framework

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRunRequestDisableToolsIsHostOnly(t *testing.T) {
	var req RunRequest
	if err := json.Unmarshal([]byte(`{"agent_id":"a","trigger":"SYSTEM_EVENT","input":{"disable_tools":true},"DisableTools":true,"disable_tools":true}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.DisableTools {
		t.Fatal("untrusted JSON enabled host-only restriction")
	}
	req.DisableTools = true
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 3 {
		t.Fatalf("host restriction leaked to JSON: %s", raw)
	}
}

func TestRuntimeDisableToolsAndNormalRunCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name              string
		disabled, attempt bool
	}{
		{"correction returns final", true, false},
		{"correction rejects tool", true, true},
		{"ordinary tool run", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			a := fixtureAgent("a")
			a.EnabledTools = []string{"echo"}
			a.Permissions = []string{"echo.read"}
			repo.agents[a.ID] = a
			tools := NewToolRegistry()
			executed := 0
			if err := tools.Register(ToolSpec{Name: "echo", Description: "echo", Permissions: []string{"echo.read"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"string"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
				executed++
				return "echoed", nil
			}); err != nil {
				t.Fatal(err)
			}
			callsCount := 0
			model := modelFunc(func(_ context.Context, r ModelRequest) (ModelResponse, error) {
				callsCount++
				if tc.disabled && len(r.Tools) != 0 {
					t.Fatal("disabled tools exposed to model")
				}
				if !tc.disabled && len(r.Tools) != 1 {
					t.Fatal("normal tool catalog changed")
				}
				if tc.attempt && callsCount == 1 {
					return calls(call("echo-1", "echo", `{}`)), nil
				}
				return final(`{"status":"ROUTED"}`), nil
			})
			runtime := NewRuntime(repo, tools, map[string]ModelProvider{"mock": model})
			result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: a.ID, Trigger: SystemEvent, Input: json.RawMessage(`{}`), DisableTools: tc.disabled})
			record := repo.runs[result.RunID]
			if tc.disabled && tc.attempt {
				requireCode(t, err, "PERMISSION_DENIED")
				if executed != 0 || record.Status != "failed" || record.ToolCalls != 0 {
					t.Fatal("disabled tool executed or failed audit missing", executed, record)
				}
			} else {
				if err != nil || result.Output != `{"status":"ROUTED"}` || record.Status != "succeeded" {
					t.Fatal(result, err, record)
				}
				want := 0
				if tc.attempt {
					want = 1
				}
				if executed != want {
					t.Fatal("unexpected tool executions", executed, want)
				}
			}
		})
	}
}

func TestRuntimeDisableToolsInheritsCancellation(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	repo.agents[a.ID] = a
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := modelFunc(func(ctx context.Context, r ModelRequest) (ModelResponse, error) {
		if len(r.Tools) != 0 {
			t.Fatal("tools exposed")
		}
		cancel()
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	})
	runtime := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": model})
	result, err := runtime.Run(ctx, "owner", RunRequest{AgentID: a.ID, Trigger: SystemEvent, Input: json.RawMessage(`{}`), DisableTools: true})
	requireCode(t, err, "CANCELLED")
	if repo.runs[result.RunID].Status != "failed" {
		t.Fatal("cancelled run missing")
	}
}
