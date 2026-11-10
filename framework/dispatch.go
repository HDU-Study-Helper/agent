package framework

import (
	"context"
	"encoding/json"
	"strings"
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

func dispatchEventSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"event_id": map[string]any{"type": []string{"string", "null"}, "maxLength": 128},
		"content":  map[string]any{"oneOf": []any{map[string]any{"type": "string", "maxLength": 65536}, map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "object"}}}}, "source": map[string]any{"type": "object"},
		"occurred_at":   map[string]any{"type": []string{"string", "null"}, "format": "date-time"},
		"evidence_refs": map[string]any{"type": "array", "maxItems": 100},
	}, "required": []string{"event_id", "content", "source", "evidence_refs"}}
}

func dispatchInputSchema() json.RawMessage {
	workflow := object(map[string]any{"workflow_id": str(128), "version": integer(1, 2147483647)}, "workflow_id", "version")
	// Host event fallback happens in the handler. Explicit event copies still
	// receive the same full event validation here, before execution.
	return object(map[string]any{"target_agent_id": str(128), "task": str(16384), "workflow": workflow, "context": map[string]any{"type": "object", "properties": map[string]any{"event": dispatchEventSchema()}}}, "target_agent_id", "task")
}

func (b Builtins) registerDispatch(add addTool) error {
	return add("agent_dispatch", "Synchronously run an enabled agent of the same owner. Worker calls require target_agent_id, task, workflow:{workflow_id,version}; context.item_id may identify the subtask. For every target, including Learner calls without workflow, the host automatically forwards original event evidence when bound: omit context.event rather than copying it. Without a host event, provide context.event:{event_id,content,source,evidence_refs,occurred_at?}. Requires workflow.read for Workflow selection; server validates owner/active/version and binds scope. Never put credentials in context.", "agent.dispatch", dispatchInputSchema(), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		if b.Dispatcher == nil {
			return nil, Fail("NOT_IMPLEMENTED", "dispatch bridge is not configured")
		}
		p := decode[AgentDispatchInput](raw)
		if p.Target == e.Scope.AgentID || contains(e.ancestry, p.Target) {
			return nil, Fail("INVALID_ARGUMENT", "target_agent_id cannot select the current agent or an ancestor waiting for this run; use the configured Worker or Learner target instead")
		}
		childContext, contextErr := dispatchContext(ctx, e.Scope.OwnerID, p.Context, p.Workflow != nil)
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
			// Entity identity comes from the owner/version-validated definition,
			// never a lossy model copy. This binds business context, not authority.
			var definition struct {
				Entity json.RawMessage `json:"entity"`
			}
			if json.Unmarshal(w.Definition, &definition) == nil && len(definition.Entity) > 0 && string(definition.Entity) != "null" {
				var entity map[string]any
				if json.Unmarshal(definition.Entity, &entity) == nil && entity != nil {
					if requested, exists := childContext["entity"]; exists {
						var original any
						if json.Unmarshal(requested, &original) == nil {
							canonical, _ := json.Marshal(original)
							actual, _ := json.Marshal(entity)
							if string(canonical) != string(actual) {
								if originalEntity, ok := original.(map[string]any); ok {
									requestedID, _ := originalEntity["id"].(string)
									actualID, _ := entity["id"].(string)
									if requestedID != "" && actualID != "" && !strings.HasPrefix(actualID, requestedID) {
										return nil, Fail("PERMISSION_DENIED", "requested entity conflicts with selected workflow; verify Host evidence and select the matching workflow")
									}
								}
								childContext["requested_entity"] = requested
								childContext["entity_warning"] = json.RawMessage(`"requested_entity differs from the validated Workflow entity; verify task and Host evidence before business writes"`)
							}
						}
					}
					childContext["entity"] = definition.Entity
					input["context"] = childContext
				}
			}
		}
		payload, _ := json.Marshal(input)
		return b.Dispatcher.Dispatch(ctx, e, RunRequest{AgentID: p.Target, Trigger: AgentDispatch, Input: payload})
	})
}
