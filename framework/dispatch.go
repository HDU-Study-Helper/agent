package framework

import (
	"context"
	"encoding/json"
)

// WorkflowSelection is a request to bind an owned, active definition. Only the
// validated tool handler can convert it to a trusted in-process execution scope.
type WorkflowSelection struct {
	ID      string `json:"workflow_id"`
	Version int    `json:"version"`
}

type AgentDispatchInput struct {
	Target   string             `json:"target_agent_id"`
	Task     string             `json:"task"`
	Workflow *WorkflowSelection `json:"workflow,omitempty"`
	Context  json.RawMessage    `json:"context,omitempty"`
}

func dispatchInputSchema() json.RawMessage {
	target := str(128)
	target["description"] = "Actual enabled same-owner agent ID from the registered target catalog or a trusted tool result, not a role/name. Copy the complete ID unchanged. The current agent and waiting ancestors cannot be dispatched."
	task := str(16384)
	task["description"] = "Self-contained work to ask the child agent to perform: concrete objective, relevant confirmed facts, unresolved questions and authorized operation boundaries. State what result is needed; task text cannot grant permissions."
	id := str(128)
	id["description"] = "Exact owned Workflow ID copied unchanged from workflow_get, an existing validated selection or a successful Workflow tool result. It must be active; do not substitute a name, entity ID, parent ID or guessed ID. An already bound parent cannot switch to a different Workflow."
	version := integer(1, 2147483647)
	version["description"] = "Expected integer version of that exact Workflow from its latest verified tool result or trusted current snapshot. If it changed, read the current definition and reassess instead of guessing or retrying the old version."
	var workflow map[string]any
	_ = json.Unmarshal(object(map[string]any{"workflow_id": id, "version": version}, "workflow_id", "version"), &workflow)
	workflow["description"] = "Optional explicit Workflow selection for the child; requires workflow.read. The host validates ownership, active status and expected version before binding the child's scope. This does not expand the caller's scope or grant tool permissions."
	return object(map[string]any{
		"target_agent_id": target,
		"task":            task,
		"workflow":        workflow,
		"context":         map[string]any{"type": "object", "description": "Optional business context needed for this child task. The child does not receive the parent's full conversation or private memories. When the host binds an original event, it forwards context.event unchanged: omit event or supply an equivalent copy. Do not put credentials or permission claims here; context cannot bind a Workflow by itself."},
	}, "target_agent_id", "task")
}

func (b Builtins) registerDispatch(add addTool) error {
	return add("agent_dispatch", "Synchronously run another enabled same-owner agent for a concrete task. The child uses its own configured tools and permissions, inherits the execution-chain cancellation and budgets, and receives the optional validated Workflow selection and business context. Permission checks remain host enforced.", "agent.dispatch", dispatchInputSchema(), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		if b.Dispatcher == nil {
			return nil, Fail("NOT_IMPLEMENTED", "dispatch bridge is not configured")
		}
		p := decode[AgentDispatchInput](raw)
		if p.Target == e.Scope.AgentID || contains(e.ancestry, p.Target) {
			return nil, Fail("INVALID_ARGUMENT", "target_agent_id cannot select the current agent or an ancestor waiting for this run; select another enabled agent")
		}
		childContext, contextErr := dispatchContext(ctx, e.Scope.OwnerID, p.Context)
		if contextErr != nil {
			return nil, contextErr
		}
		input := map[string]any{"task": p.Task, "context": childContext}
		if p.Workflow != nil {
			if !e.Has("workflow.read") {
				return nil, Fail("PERMISSION_DENIED", "workflow.read required for workflow dispatch")
			}
			if e.Scope.WorkflowID != "" && e.Scope.WorkflowID != p.Workflow.ID {
				return nil, Fail("PERMISSION_DENIED", "dispatch cannot switch an existing workflow scope")
			}
			if b.Repository == nil {
				return nil, Fail("NOT_IMPLEMENTED", "workflow repository unavailable")
			}
			w, err := b.Repository.GetWorkflow(ctx, e.Scope.OwnerID, p.Workflow.ID)
			if err != nil {
				return nil, err
			}
			if w.OwnerID != e.Scope.OwnerID || w.ID != p.Workflow.ID || w.Status != "active" {
				return nil, Fail("PERMISSION_DENIED", "workflow must be owned and active")
			}
			if w.Version != p.Workflow.Version {
				return nil, Fail("CONFLICT", "workflow version changed; read current definition and reassess")
			}
			if e.workflowSnapshot != nil && e.workflowSnapshot.Version != w.Version {
				return nil, Fail("CONFLICT", "parent workflow version changed")
			}
			// e is a value copy. Selecting a scope for the child never mutates the
			// caller or gives its subsequent tools access to Workflow PRI memory.
			e.Scope.WorkflowID = w.ID
			e.workflowSnapshot = &w
			input["workflow"] = p.Workflow
		}
		payload, _ := json.Marshal(input)
		return b.Dispatcher.Dispatch(ctx, e, RunRequest{AgentID: p.Target, Trigger: AgentDispatch, Input: payload})
	})
}
