package framework

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type workflowDispatchRepo struct {
	*fakeRepo
	w Workflow
}

func (r *workflowDispatchRepo) GetWorkflow(_ context.Context, owner, id string) (Workflow, error) {
	if r.w.OwnerID != owner || r.w.ID != id {
		return Workflow{}, Fail("RESOURCE_NOT_FOUND", "not owned")
	}
	return r.w, nil
}

type dispatchFunc func(context.Context, ExecutionContext, RunRequest) (RunResult, error)

func (fn dispatchFunc) Dispatch(ctx context.Context, e ExecutionContext, req RunRequest) (RunResult, error) {
	return fn(ctx, e, req)
}

const unifiedDispatchArgs = `{"target_agent_id":"child","task":"Handle the event","workflow":{"workflow_id":"wf","version":3},"context":{"event":{"event_id":"evt","content":"original notice","source":{"platform":"manual"},"evidence_refs":[]}}}`

func TestDispatchRejectsSelfAndAncestorsBeforeDispatcher(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		ancestry     []string
	}{
		{"self without ancestry", "a", nil},
		{"self", "a", []string{"parent", "a"}},
		{"ancestor", "parent", []string{"parent", "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := NewToolRegistry()
			called := false
			if err := (Builtins{Dispatcher: dispatchFunc(func(context.Context, ExecutionContext, RunRequest) (RunResult, error) {
				called = true
				return RunResult{}, nil
			})}).Register(tools); err != nil {
				t.Fatal(err)
			}
			e := toolContext([]string{"agent_dispatch"}, []string{"agent.dispatch"})
			e.ancestry = tc.ancestry
			args, _ := json.Marshal(map[string]string{"target_agent_id": tc.target, "task": "test"})
			result := tools.Execute(context.Background(), e, "agent_dispatch", args)
			if result.OK || result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" || called {
				t.Fatal("cyclic target reached dispatcher or was not correctable", result, called)
			}
			if !strings.Contains(result.Error.Message, "target_agent_id") {
				t.Fatal("missing actionable target guidance", result.Error)
			}
		})
	}
}

func TestDispatchSelfTargetCanBeCorrectedAcrossIndependentRootRuns(t *testing.T) {
	repo := newFakeRepo()
	parent, child := fixtureAgent("parent"), fixtureAgent("child")
	parent.Model.Provider, child.Model.Provider = "parent", "child"
	parent.EnabledTools = []string{"agent_dispatch"}
	parent.Permissions = []string{"agent.dispatch"}
	parent.Limits.DispatchDepth = 1
	repo.agents[parent.ID], repo.agents[child.ID] = parent, child
	tools := NewToolRegistry()
	var runtime *Runtime
	dispatched := 0
	if err := (Builtins{Dispatcher: dispatchFunc(func(ctx context.Context, e ExecutionContext, req RunRequest) (RunResult, error) {
		dispatched++
		if len(e.ancestry) != 1 || e.ancestry[0] != "parent" || req.AgentID != "child" {
			t.Fatal("root ancestry leaked or invalid target dispatched", e.ancestry, req.AgentID)
		}
		return runtime.Run(DispatchContext(ctx, e), e.Scope.OwnerID, req)
	})}).Register(tools); err != nil {
		t.Fatal(err)
	}
	runtime = NewRuntime(repo, tools, map[string]ModelProvider{
		"parent": modelFunc(func(_ context.Context, request ModelRequest) (ModelResponse, error) {
			last := request.Messages[len(request.Messages)-1]
			if last.Role != "tool" {
				return calls(call("wrong-target", "agent_dispatch", `{"target_agent_id":"parent","task":"test"}`)), nil
			}
			var result ToolResult
			if err := json.Unmarshal([]byte(last.Content), &result); err != nil {
				t.Fatal(err)
			}
			if !result.OK {
				if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
					t.Fatal("self-target failure aborted instead of returning feedback", result)
				}
				return calls(call("correct-target", "agent_dispatch", `{"target_agent_id":"child","task":"test"}`)), nil
			}
			return final("parent done"), nil
		}),
		"child": modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) { return final("child done"), nil }),
	})
	for i := 0; i < 2; i++ {
		result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: "parent", Trigger: DataChanged, Input: json.RawMessage(`{}`)})
		if err != nil || result.Output != "parent done" {
			t.Fatal("root failed to recover target", i, result, err)
		}
		run := repo.runs[result.RunID]
		if run.ParentRunID != "" || run.Status != "succeeded" || run.ModelCalls != 3 || run.ToolCalls != 2 {
			t.Fatal("incorrect root audit", run)
		}
	}
	if dispatched != 2 || len(repo.runs) != 4 {
		t.Fatal("self-target created a child or root ancestry accumulated", dispatched, len(repo.runs))
	}
}

func TestUnifiedDispatchScopeAndSnapshot(t *testing.T) {
	repo := &workflowDispatchRepo{fakeRepo: newFakeRepo(), w: Workflow{ID: "wf", OwnerID: "owner", Version: 3, Status: "active", Name: "course", Definition: json.RawMessage(`{"goal":"test goal"}`)}}
	parent, child := fixtureAgent("parent"), fixtureAgent("child")
	parent.EnabledTools = []string{"agent_dispatch"}
	parent.Permissions = []string{"agent.dispatch", "workflow.read"}
	parent.Model.Provider = "parent"
	child.Model.Provider = "child"
	repo.agents[parent.ID] = parent
	repo.agents[child.ID] = child
	tools := NewToolRegistry()
	var runtime *Runtime
	dispatch := dispatchFunc(func(ctx context.Context, e ExecutionContext, req RunRequest) (RunResult, error) {
		return runtime.Run(DispatchContext(ctx, e), e.Scope.OwnerID, req)
	})
	if err := (Builtins{Repository: repo, Dispatcher: dispatch}).Register(tools); err != nil {
		t.Fatal(err)
	}
	childCalled := false
	runtime = NewRuntime(repo, tools, map[string]ModelProvider{
		"parent": modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
			last := req.Messages[len(req.Messages)-1]
			if last.Role == "tool" {
				var result ToolResult
				if json.Unmarshal([]byte(last.Content), &result) != nil || !result.OK {
					t.Fatal(last.Content)
				}
				return final("parent done"), nil
			}
			return ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "dispatch", Type: "function", Function: FunctionCall{Name: "agent_dispatch", Arguments: unifiedDispatchArgs}}}}}, nil
		}),
		"child": modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
			childCalled = true
			if !strings.Contains(req.Messages[0].Content, "workflow_id=wf") || !strings.Contains(req.Messages[0].Content, "Trusted run_id=") || !strings.Contains(req.Messages[0].Content, `"version":3`) || !strings.Contains(req.Messages[0].Content, "test goal") {
				t.Fatal("missing trusted context", req.Messages[0].Content)
			}
			var envelope struct {
				Input struct {
					Workflow WorkflowSelection `json:"workflow"`
					Context  struct {
						Event struct {
							Content string `json:"content"`
						} `json:"event"`
					} `json:"context"`
				} `json:"input"`
			}
			if json.Unmarshal([]byte(req.Messages[len(req.Messages)-1].Content), &envelope) != nil || envelope.Input.Workflow.Version != 3 || envelope.Input.Context.Event.Content != "original notice" {
				t.Fatal("lost unified input")
			}
			return final("child done"), nil
		}),
	})
	result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: "parent", Trigger: UserMessage, Input: json.RawMessage(`{}`)})
	if err != nil || !childCalled {
		t.Fatal(result, err)
	}
	for _, run := range repo.runs {
		if run.AgentID == "parent" && (run.WorkflowID != "" || run.WorkflowSnapshot != nil) {
			t.Fatal("child scope leaked into parent")
		}
		if run.AgentID == "child" && (run.WorkflowID != "wf" || run.WorkflowVersion != 3 || run.WorkflowSnapshot == nil || run.ParentRunID != result.RunID) {
			t.Fatal("missing pinned child version", run)
		}
	}
}

func TestUnifiedDispatchPreservesStructuredChanges(t *testing.T) {
	repo := &workflowDispatchRepo{fakeRepo: newFakeRepo(), w: Workflow{ID: "wf", OwnerID: "owner", Version: 3, Status: "active"}}
	tools := NewToolRegistry()
	changes := `[{"change_id":"change","record_id":"48413","external_id":"1308560821","connection_id":"connection"}]`
	called := false
	err := (Builtins{Repository: repo, Dispatcher: dispatchFunc(func(_ context.Context, _ ExecutionContext, req RunRequest) (RunResult, error) {
		var input struct {
			Context struct {
				Event struct {
					Content json.RawMessage `json:"content"`
				} `json:"event"`
			} `json:"context"`
		}
		if json.Unmarshal(req.Input, &input) != nil || string(input.Context.Event.Content) != changes {
			t.Fatal("structured evidence lost", string(req.Input))
		}
		called = true
		return RunResult{RunID: "child-run", Output: "ok"}, nil
	})}).Register(tools)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Replace(unifiedDispatchArgs, `"original notice"`, changes, 1)
	result := tools.Execute(context.Background(), toolContext([]string{"agent_dispatch"}, []string{"agent.dispatch", "workflow.read"}), "agent_dispatch", json.RawMessage(args))
	if !result.OK || !called {
		t.Fatal(result, called)
	}
}

func TestDispatchPreservesArbitraryContextWithWorkflow(t *testing.T) {
	for _, contextJSON := range []string{"", `{}`, `{"entity":{"kind":"custom","id":"different-entity"},"nested":{"values":[null,false,9007199254740993]}}`, `{"event":"custom event","source":null,"entity_warning":"caller data"}`} {
		t.Run(contextJSON, func(t *testing.T) {
			repo := &workflowDispatchRepo{fakeRepo: newFakeRepo(), w: Workflow{ID: "wf", OwnerID: "owner", Version: 3, Status: "active", Definition: json.RawMessage(`{"entity":{"kind":"course","id":"workflow-entity","name":"Workflow entity"}}`)}}
			want := map[string]json.RawMessage{}
			if contextJSON != "" {
				_ = json.Unmarshal([]byte(contextJSON), &want)
			}
			tools := NewToolRegistry()
			called := false
			err := (Builtins{Repository: repo, Dispatcher: dispatchFunc(func(_ context.Context, e ExecutionContext, req RunRequest) (RunResult, error) {
				called = true
				if e.Scope.OwnerID != "owner" || e.Scope.WorkflowID != "wf" || e.Has("schedule.write") || e.workflowSnapshot == nil || e.workflowSnapshot.Version != 3 || string(e.workflowSnapshot.Definition) != string(repo.w.Definition) {
					t.Fatal("context changed authority or lost trusted snapshot", e.Scope)
				}
				var input struct {
					Task    string                     `json:"task"`
					Context map[string]json.RawMessage `json:"context"`
				}
				if json.Unmarshal(req.Input, &input) != nil || input.Task != "Handle the event" || !reflect.DeepEqual(input.Context, want) {
					t.Fatal("business context was rewritten", string(req.Input))
				}
				return RunResult{RunID: "child", Output: "ok"}, nil
			})}).Register(tools)
			if err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"target_agent_id": "child", "task": "Handle the event", "workflow": WorkflowSelection{ID: "wf", Version: 3}}
			if contextJSON != "" {
				payload["context"] = json.RawMessage(contextJSON)
			}
			raw, _ := json.Marshal(payload)
			e := toolContext([]string{"agent_dispatch"}, []string{"agent.dispatch", "workflow.read"})
			result := tools.Execute(context.Background(), e, "agent_dispatch", raw)
			if !result.OK || !called || e.Scope.WorkflowID != "" || e.workflowSnapshot != nil {
				t.Fatal("dispatch failed or child scope leaked to caller", result, called, e.Scope)
			}
		})
	}
}

func TestUnifiedDispatchRejectsInvalidSelection(t *testing.T) {
	for _, tc := range []struct {
		name, args, permission, status, owner, scope, want string
		version                                            int
	}{
		{"stale", unifiedDispatchArgs, "workflow.read", "active", "owner", "", "CONFLICT", 4},
		{"draft", unifiedDispatchArgs, "workflow.read", "draft", "owner", "", "PERMISSION_DENIED", 3},
		{"archived", unifiedDispatchArgs, "workflow.read", "archived", "owner", "", "PERMISSION_DENIED", 3},
		{"foreign", unifiedDispatchArgs, "workflow.read", "active", "other", "", "RESOURCE_NOT_FOUND", 3},
		{"no read", unifiedDispatchArgs, "", "active", "owner", "", "PERMISSION_DENIED", 3},
		{"switch", unifiedDispatchArgs, "workflow.read", "active", "owner", "other-wf", "PERMISSION_DENIED", 3},
		{"parent snapshot changed", unifiedDispatchArgs, "workflow.read", "active", "owner", "wf", "CONFLICT", 3},
		{"non-object context", `{"target_agent_id":"child","task":"x","workflow":{"workflow_id":"wf","version":3},"context":[]}`, "workflow.read", "active", "owner", "", "INVALID_ARGUMENT", 3},
		{"string version", strings.Replace(unifiedDispatchArgs, `"version":3`, `"version":"v3"`, 1), "workflow.read", "active", "owner", "", "INVALID_ARGUMENT", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &workflowDispatchRepo{fakeRepo: newFakeRepo(), w: Workflow{ID: "wf", OwnerID: tc.owner, Version: tc.version, Status: tc.status}}
			tools := NewToolRegistry()
			called := false
			if err := (Builtins{Repository: repo, Dispatcher: dispatchFunc(func(context.Context, ExecutionContext, RunRequest) (RunResult, error) {
				called = true
				return RunResult{}, nil
			})}).Register(tools); err != nil {
				t.Fatal(err)
			}
			e := toolContext([]string{"agent_dispatch"}, []string{"agent.dispatch", tc.permission})
			e.Scope.WorkflowID = tc.scope
			if tc.name == "parent snapshot changed" {
				e.workflowSnapshot = &Workflow{ID: "wf", Version: 2}
			}
			result := tools.Execute(context.Background(), e, "agent_dispatch", json.RawMessage(tc.args))
			if result.OK || result.Error.Code != tc.want || called {
				t.Fatal(result, called)
			}
		})
	}
}

func TestLegacyDispatchDoesNotTrustContextWorkflow(t *testing.T) {
	tools := NewToolRegistry()
	if err := (Builtins{Dispatcher: dispatchFunc(func(_ context.Context, e ExecutionContext, req RunRequest) (RunResult, error) {
		if e.Scope.WorkflowID != "" || e.workflowSnapshot != nil {
			t.Fatal("context JSON granted workflow access")
		}
		return RunResult{RunID: "legacy", Output: "ok"}, nil
	})}).Register(tools); err != nil {
		t.Fatal(err)
	}
	result := tools.Execute(context.Background(), toolContext([]string{"agent_dispatch"}, []string{"agent.dispatch"}), "agent_dispatch", json.RawMessage(`{"target_agent_id":"child","task":"x","context":{"workflow_id":"wf","workflow":{"workflow_id":"wf","version":3}}}`))
	if !result.OK {
		t.Fatal(result)
	}
}
