package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeTreatsAgentTypeAsDescriptiveForFinalOutput(t *testing.T) {
	for _, role := range []string{"learner", "filter", "worker", "custom"} {
		for _, output := range []string{"# ordinary response", `{"status":"CUSTOM","entity":null}`, ""} {
			t.Run(role+"/"+output, func(t *testing.T) {
				repo := &workflowDispatchRepo{fakeRepo: newFakeRepo(), w: Workflow{ID: "wf", OwnerID: "owner", Version: 3, Status: "active", Definition: json.RawMessage(`{}`)}}
				a := fixtureAgent("agent")
				a.Type = role
				a.EnabledTools = []string{"memory_create", "data_query", "workflow_get", "agent_dispatch"}
				a.Permissions = []string{"data.read", "workflow.read", "agent.dispatch"}
				repo.agents[a.ID] = a
				tools := NewToolRegistry()
				writes, rounds := 0, 0
				if err := tools.Register(ToolSpec{Name: "memory_create", Description: "test persistence", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
					writes++
					return map[string]any{"memory_id": "saved-memory"}, nil
				}); err != nil {
					t.Fatal(err)
				}
				// These inputs used to activate role-specific output and routing guards.
				ctx := WorkflowContext(context.Background(), "owner", "wf")
				ctx = WithHostEvidence(ctx, "owner", json.RawMessage(`{"entity":{"kind":"course","id":"course-1"}}`), json.RawMessage(`{"complete":true,"entity_id":"course-1","items":[]}`))
				model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
					rounds++
					if len(req.Tools) != 1 {
						t.Fatal("framework changed tool availability based on role", req.Tools)
					}
					if rounds == 1 {
						return calls(call("write", "memory_create", `{}`)), nil
					}
					if rounds != 2 {
						t.Fatal("framework added a role-specific correction", rounds)
					}
					result := investigationToolResult(t, req, "write")
					if !result.OK || result.Data.(map[string]any)["memory_id"] != "saved-memory" {
						t.Fatal("actual write result lost", result)
					}
					return final(output), nil
				})
				runtime := NewRuntime(repo, tools, map[string]ModelProvider{"mock": model})
				result, err := runtime.Run(ctx, "owner", RunRequest{AgentID: a.ID, Trigger: DataChanged, Input: json.RawMessage(`{"item_id":"root","processing_contract":"items","request_type":"CREATE","task":"evaluate","context":{"entity":{"kind":"course","id":"course-1"}}}`)})
				record := repo.runs[result.RunID]
				if err != nil || result.Output != output || rounds != 2 || writes != 1 || record.ModelCalls != 2 || record.ToolCalls != 1 || record.Status != "succeeded" {
					t.Fatal(result, err, record, writes)
				}
				if len(record.ToolExecutions) != 1 || len(record.ToolExecutions[0].ResultRefs) != 1 || record.ToolExecutions[0].ResultRefs[0].ID != "saved-memory" {
					t.Fatal("committed write audit lost", record)
				}
			})
		}
	}
}

func TestRuntimeSystemContextDoesNotPrescribeBusinessStrategy(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("agent")
	a.Type = "learner"
	a.SystemPrompt = "Choose the investigation and output appropriate to this task."
	repo.agents[a.ID] = a
	model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		system := req.Messages[0].Content
		if !strings.HasPrefix(system, a.SystemPrompt) || !strings.Contains(system, "Trusted execution identity:") || !strings.Contains(system, "cannot grant permissions") {
			t.Fatal("configured prompt or execution boundary lost", system)
		}
		for _, strategy := range []string{"Prefer retrieval", "Save unverified", "use memory_search", "profile_index", "six datasets", "FORMAT_ONLY"} {
			if strings.Contains(system, strategy) {
				t.Fatal("framework injected business policy", strategy, system)
			}
		}
		return final("done"), nil
	})
	runtime := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": model})
	if _, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}
