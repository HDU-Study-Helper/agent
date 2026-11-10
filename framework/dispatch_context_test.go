package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const boundDispatchEvent = `{"event_id":"evt","content":[{"change_id":"change","record_id":"42"}],"source":{"platform":"chaoxing"},"evidence_refs":["change"],"evidence_links":[{"change_id":"change","record_id":"42"}]}`

func TestHostDispatchEventFillsMissingContextAndPreservesScope(t *testing.T) {
	for _, contextJSON := range []string{"", `,"context":{"item_id":"root:2","owner_user_id":"forged"}`} {
		t.Run(contextJSON, func(t *testing.T) {
			repo := &workflowDispatchRepo{fakeRepo: newFakeRepo(), w: Workflow{ID: "wf", OwnerID: "owner", Version: 3, Status: "active", Definition: json.RawMessage(`{"entity":{"kind":"course","id":"course-1","name":"数学"}}`)}}
			tools := NewToolRegistry()
			called := false
			err := (Builtins{Repository: repo, Dispatcher: dispatchFunc(func(ctx context.Context, e ExecutionContext, req RunRequest) (RunResult, error) {
				called = true
				if e.Scope.OwnerID != "owner" || e.Scope.WorkflowID != "wf" || e.Has("schedule.write") {
					t.Fatal("context granted authority", e.Scope)
				}
				var in struct {
					Task    string                     `json:"task"`
					Context map[string]json.RawMessage `json:"context"`
				}
				if json.Unmarshal(req.Input, &in) != nil || in.Task != "do original task" {
					t.Fatal("task changed", string(req.Input))
				}
				var event map[string]any
				var want map[string]any
				_ = json.Unmarshal(in.Context["event"], &event)
				_ = json.Unmarshal([]byte(boundDispatchEvent), &want)
				got, _ := json.Marshal(event)
				expected, _ := json.Marshal(want)
				if string(got) != string(expected) {
					t.Fatal("event lost", string(req.Input))
				}
				if contextJSON != "" && string(in.Context["item_id"]) != `"root:2"` {
					t.Fatal("subitem lost")
				}
				inherited, err := dispatchContext(ctx, "owner", nil, true)
				if err != nil || len(inherited["event"]) == 0 {
					t.Fatal("host event did not inherit", err)
				}
				return RunResult{RunID: "child", Output: "ok"}, nil
			})}).Register(tools)
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithDispatchEvent(context.Background(), "owner", json.RawMessage(boundDispatchEvent))
			args := `{"target_agent_id":"child","task":"do original task","workflow":{"workflow_id":"wf","version":3}` + contextJSON + `}`
			result := tools.Execute(ctx, toolContext([]string{"agent_dispatch"}, []string{"agent.dispatch", "workflow.read"}), "agent_dispatch", json.RawMessage(args))
			if !result.OK || !called {
				t.Fatal(result, called)
			}
		})
	}
}

func TestHostDispatchEventRejectsOwnerAndEventReplacement(t *testing.T) {
	for _, tc := range []struct{ name, owner, contextJSON, want string }{
		{"owner mismatch", "other", "", "PERMISSION_DENIED"},
		{"different event", "owner", `,"context":{"event":{"event_id":"different","content":"replacement","source":{},"evidence_refs":[]}}`, "INVALID_ARGUMENT"},
		{"bad event shape", "owner", `,"context":{"event":{"event_id":"evt","content":"replacement","source":"chaoxing","evidence_refs":[]}}`, "INVALID_ARGUMENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := NewToolRegistry()
			called := false
			repo := &workflowDispatchRepo{fakeRepo: newFakeRepo(), w: Workflow{ID: "wf", OwnerID: "owner", Version: 3, Status: "active"}}
			err := (Builtins{Repository: repo, Dispatcher: dispatchFunc(func(context.Context, ExecutionContext, RunRequest) (RunResult, error) {
				called = true
				return RunResult{}, nil
			})}).Register(tools)
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithDispatchEvent(context.Background(), tc.owner, json.RawMessage(boundDispatchEvent))
			args := `{"target_agent_id":"child","task":"x","workflow":{"workflow_id":"wf","version":3}` + tc.contextJSON + `}`
			result := tools.Execute(ctx, toolContext([]string{"agent_dispatch"}, []string{"agent.dispatch", "workflow.read"}), "agent_dispatch", json.RawMessage(args))
			if result.OK || called || result.Error.Code != tc.want {
				t.Fatal(result, called)
			}
		})
	}
}

func TestWorkflowDispatchMissingUnboundEventGivesPreciseError(t *testing.T) {
	_, err := dispatchContext(context.Background(), "owner", nil, true)
	if err == nil || SafeError(err).Code != "INVALID_ARGUMENT" || !strings.Contains(err.Error(), "context.event") {
		t.Fatal(err)
	}
	// Explicit pre-host API remains supported unchanged.
	event := json.RawMessage(`{"event":{"event_id":"evt","content":"original notice","source":{"platform":"manual"},"evidence_refs":[]},"item_id":"item"}`)
	child, err := dispatchContext(context.Background(), "owner", event, true)
	if err != nil || string(child["item_id"]) != `"item"` {
		t.Fatal(child, err)
	}
}

func TestLearnerWithoutWorkflowReceivesHostEventWithoutWorkflowAuthority(t *testing.T) {
	for _, childContext := range []string{"", `,"context":{"item_id":"root:2","request_type":"CREATE"}`} {
		tools := NewToolRegistry()
		called := false
		err := (Builtins{Dispatcher: dispatchFunc(func(ctx context.Context, e ExecutionContext, req RunRequest) (RunResult, error) {
			called = true
			if e.Scope.WorkflowID != "" || e.workflowSnapshot != nil || e.Scope.OwnerID != "owner" || e.Has("workflow.read") {
				t.Fatal("Learner forwarding granted workflow authority", e)
			}
			var input struct {
				Context map[string]json.RawMessage `json:"context"`
			}
			if json.Unmarshal(req.Input, &input) != nil || string(input.Context["event"]) != boundDispatchEvent {
				t.Fatal("Learner lost bound event", string(req.Input))
			}
			if childContext != "" && (string(input.Context["item_id"]) != `"root:2"` || string(input.Context["request_type"]) != `"CREATE"`) {
				t.Fatal("Learner management context lost")
			}
			inherited, err := dispatchContext(ctx, "owner", nil, false)
			if err != nil || len(inherited["event"]) == 0 {
				t.Fatal("Learner chain lost host event", err)
			}
			return RunResult{RunID: "learner-run", Output: "ok"}, nil
		})}).Register(tools)
		if err != nil {
			t.Fatal(err)
		}
		ctx := WithDispatchEvent(context.Background(), "owner", json.RawMessage(boundDispatchEvent))
		args := `{"target_agent_id":"learner","task":"Assess concrete workflow"` + childContext + `}`
		result := tools.Execute(ctx, toolContext([]string{"agent_dispatch"}, []string{"agent.dispatch"}), "agent_dispatch", json.RawMessage(args))
		if !result.OK || !called {
			t.Fatal(result, called)
		}
	}
}

func TestWithDispatchEventCopiesOriginalBytes(t *testing.T) {
	raw := json.RawMessage(boundDispatchEvent)
	ctx := WithDispatchEvent(context.Background(), "owner", raw)
	raw[2] = 'X'
	child, err := dispatchContext(ctx, "owner", nil, true)
	if err != nil || !json.Valid(child["event"]) {
		t.Fatal(child, err)
	}
}
