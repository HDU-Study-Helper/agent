package framework

import (
	"context"
	"encoding/json"
)

func enum(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
func memoryInputFields() map[string]any {
	return map[string]any{
		"visibility": enum("PUB", "PRI"), "owner_agent_id": map[string]any{"type": []string{"string", "null"}, "maxLength": 128}, "owner_workflow_id": map[string]any{"type": []string{"string", "null"}, "maxLength": 128},
		"memory_type": enum("BASE", "TEMP"), "is_personal": map[string]any{"type": "boolean"}, "level": enum("L0", "L1", "L2", "L3"), "parent_memory_id": map[string]any{"type": []string{"string", "null"}, "maxLength": 128},
		"title": str(512), "content": str(65536), "structured_content": map[string]any{"type": []string{"object", "null"}}, "source_refs": map[string]any{"type": []string{"array", "null"}, "maxItems": 100},
		"valid_from": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "valid_until": map[string]any{"type": []string{"string", "null"}, "format": "date-time"},
		"status": enum("active", "archived", "invalid"), "category": map[string]any{"type": "string", "maxLength": 128}, "priority": integer(-100, 100),
	}
}

// PatchMemory applies only explicitly supplied fields, preserving ownership and
// validity for legacy editors. Ownership changes must explicitly clear the old
// private owner; all access checks run again in the Repository after this merge.
func PatchMemory(existing Memory, raw json.RawMessage) (Memory, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return Memory{}, Fail("INVALID_ARGUMENT", "invalid memory JSON")
	}
	allowed := memoryInputFields()
	allowed["memory_id"] = str(128)
	mergedRaw, _ := json.Marshal(existing)
	var merged map[string]json.RawMessage
	_ = json.Unmarshal(mergedRaw, &merged)
	for key, value := range fields {
		if _, ok := allowed[key]; !ok {
			return Memory{}, Fail("INVALID_ARGUMENT", "unknown or immutable memory field")
		}
		merged[key] = value
	}
	data, _ := json.Marshal(merged)
	var m Memory
	if json.Unmarshal(data, &m) != nil {
		return Memory{}, Fail("INVALID_ARGUMENT", "invalid memory values or timestamps")
	}
	// json.Unmarshal preserves explicit JSON null in RawMessage; absence is the
	// canonical SQL NULL representation for optional JSON values.
	if string(m.StructuredContent) == "null" {
		m.StructuredContent = nil
	}
	if string(m.SourceRefs) == "null" {
		m.SourceRefs = nil
	}
	return m, nil
}
func (b Builtins) registerMemory(add addTool) error {
	fields := memoryInputFields()
	if err := add("memory_create", "Create PUB, own Agent PRI or current Workflow PRI memory. Prefer PRI for unverified inferences; never choose another user. Omitted classification defaults to own PRI, BASE, L1, active.", "memory.write", object(fields, "title", "content"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		m, err := PatchMemory(Memory{}, raw)
		if err != nil {
			return nil, err
		}
		return b.Repository.SaveMemory(ctx, e.Scope, m, true)
	}); err != nil {
		return err
	}
	update := memoryInputFields()
	update["memory_id"] = str(128)
	if err := add("memory_update", "Patch accessible memory; ownership changes recheck old and new scope. Clear old owner with null when moving to PUB or another allowed scope.", "memory.write", object(update, "memory_id"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		id := decode[Memory](raw).ID
		m, err := b.Repository.GetMemory(ctx, e.Scope, id)
		if err != nil {
			return nil, err
		}
		m, err = PatchMemory(m, raw)
		if err != nil {
			return nil, err
		}
		return b.Repository.SaveMemory(ctx, e.Scope, m, false)
	}); err != nil {
		return err
	}
	id := object(map[string]any{"memory_id": str(128)}, "memory_id")
	if err := add("memory_get", "Read accessible memory details. Check status and validity before treating it as a current fact.", "memory.read", id, func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		return b.Repository.GetMemory(ctx, e.Scope, decode[Memory](raw).ID)
	}); err != nil {
		return err
	}
	if err := add("memory_delete", "Delete accessible memory, cascade semantic relations and detach children.", "memory.write", id, func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		err := b.Repository.DeleteMemory(ctx, e.Scope, decode[Memory](raw).ID)
		return map[string]bool{"deleted": err == nil}, err
	}); err != nil {
		return err
	}
	search := map[string]any{"query": map[string]any{"type": "string", "maxLength": 512}, "visibility": enum("PUB", "PRI"), "owner_agent_id": str(128), "owner_workflow_id": str(128), "memory_type": enum("BASE", "TEMP"), "is_personal": map[string]any{"type": "boolean"}, "level": enum("L0", "L1", "L2", "L3"), "status": enum("active", "archived", "invalid", "all"), "parent_memory_id": str(128), "validity": enum("current", "expired", "future", "all"), "limit": integer(1, 100)}
	for _, name := range []string{"memory_search", "memory_list"} {
		if err := add(name, "Search authorized PUB, own Agent PRI and current Workflow PRI. Defaults: active and currently valid, limit 50. Retrieve historical records explicitly with validity/status. Search when evidence is insufficient rather than guessing.", "memory.read", object(search), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
			q := decode[MemorySearch](raw)
			if q.Limit == 0 {
				q.Limit = 50
			}
			return b.Repository.SearchMemory(ctx, e.Scope, q)
		}); err != nil {
			return err
		}
	}
	relationTypes := enum("RELATED_TO", "ALIAS_OF", "BELONGS_TO", "DERIVED_FROM", "ROUTES_TO", "SUPERSEDES")
	if err := add("memory_create_relation", "Link two accessible memories; does not grant access to either endpoint. Duplicate directed relations are rejected.", "memory.write", object(map[string]any{"source_memory_id": str(128), "target_memory_id": str(128), "relation_type": relationTypes, "description": map[string]any{"type": "string", "maxLength": 4096}}, "source_memory_id", "target_memory_id", "relation_type"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		return b.Repository.CreateMemoryRelation(ctx, e.Scope, decode[MemoryRelation](raw))
	}); err != nil {
		return err
	}
	return add("memory_get_relations", "Find direct relations with both endpoints accessible; get returned memory IDs to expand context. Only depth 1 is supported.", "memory.read", object(map[string]any{"memory_id": str(128), "relation_type": relationTypes, "direction": enum("incoming", "outgoing", "both"), "limit": integer(1, 100), "depth": integer(1, 1)}, "memory_id"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		return b.Repository.GetMemoryRelations(ctx, e.Scope, decode[MemoryRelationQuery](raw))
	})
}
