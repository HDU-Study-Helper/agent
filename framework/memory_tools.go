package framework

import (
	"context"
	"encoding/json"
	"strings"
)

type memoryListEntry struct {
	ID       string `json:"memory_id"`
	Title    string `json:"title"`
	Category string `json:"category,omitempty"`
	Status   string `json:"status"`
}

func memoryListLabel(text string, maxRunes int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return string(runes)
}

func enum(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
func memoryField(schema map[string]any, description string) map[string]any {
	schema["description"] = description
	return schema
}

func memoryInputFields() map[string]any {
	return map[string]any{
		"visibility":         memoryField(enum("PUB", "PRI"), "PUB shares within the current user only; PRI belongs to this Agent or the currently bound Workflow. On create defaults to PRI; on update omission preserves the old value. Changing scope rechecks access."),
		"owner_agent_id":     memoryField(map[string]any{"type": []string{"string", "null"}, "maxLength": 128}, "For Agent PRI, use only the current trusted agent_id. When both private owners are omitted on create, defaults to this Agent even if a Workflow is bound. Set null to clear this owner when moving to another allowed scope."),
		"owner_workflow_id":  memoryField(map[string]any{"type": []string{"string", "null"}, "maxLength": 128}, "For Workflow PRI, copy the currently bound workflow_id exactly. Cannot choose an unbound Workflow. PRI requires exactly one private owner; PUB requires neither. Omission on update preserves ownership; null clears this owner."),
		"memory_type":        memoryField(enum("BASE", "TEMP"), "BASE is stable knowledge; TEMP is changeable knowledge. Create defaults to BASE; update omission preserves the value. TEMP alone does not set an expiry."),
		"is_personal":        memoryField(map[string]any{"type": "boolean"}, "Whether the content concerns the user's personal facts; create defaults to false. This classification grants no permissions. Update omission preserves it."),
		"level":              memoryField(enum("L0", "L1", "L2", "L3"), "Organizational level label L0 through L3; create defaults to L1. Choose a suitable level without inferring ownership or access from it; update omission preserves it."),
		"parent_memory_id":   memoryField(map[string]any{"type": []string{"string", "null"}, "maxLength": 128}, "Optional accessible parent memory_id, copied from a read result; null clears the parent. Omission on create means no parent, and omission on update preserves it. Hierarchy cannot grant access."),
		"title":              memoryField(str(512), "Concise memory title. Required on create; update omission preserves it. An explicitly supplied title must be nonempty."),
		"content":            memoryField(str(65536), "Complete textual content of this memory, with sources and uncertainty as needed. Required on create; on update this replaces the whole text, while omission preserves it."),
		"structured_content": memoryField(map[string]any{"type": []string{"object", "null"}}, "Optional application-defined JSON object. A supplied object replaces the entire field, not a deep merge; null clears it; omission on update preserves it."),
		"source_refs":        memoryField(map[string]any{"type": []string{"array", "null"}, "maxItems": 100}, "Source references using exact IDs and provenance from read results. Legacy strings and application-defined JSON references are accepted; known complete record mappings are checked. Supplied arrays replace, never append; null clears, omission on update preserves. Some host staging modes require nonempty sources."),
		"valid_from":         memoryField(map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "Optional validity start as RFC3339 with timezone. Null clears this bound; omission on update preserves it. This is knowledge validity, not source publication time."),
		"valid_until":        memoryField(map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "Optional exclusive validity end as RFC3339 with timezone, later than valid_from when both exist. Null clears expiry; omission on update preserves it."),
		"status":             memoryField(enum("active", "archived", "invalid"), "Knowledge status; create defaults to active. Archived/invalid entries remain stored but are excluded from default searches. Update omission preserves the current status."),
		"category":           memoryField(map[string]any{"type": "string", "maxLength": 128}, "Application-chosen category, empty by default on create. An explicit empty string clears it on update; omission preserves it. No prescribed category taxonomy."),
		"priority":           memoryField(integer(-100, 100), "Retrieval priority, default 0 on create. Higher values sort first; update omission preserves it. It does not indicate confidence or grant access."),
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
	for key, value := range map[string]any{"visibility": "PRI", "memory_type": "BASE", "is_personal": false, "level": "L1", "status": "active", "category": "", "priority": 0} {
		fields[key].(map[string]any)["default"] = value
	}
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
	update["memory_id"] = memoryField(str(128), "Exact memory_id from an accessible memory read/search result. Only explicitly supplied fields are patched; all omitted fields remain unchanged.")
	if err := add("memory_update", "Patch only explicitly supplied top-level fields of accessible memory. Omitted fields remain unchanged; content, structured_content and source_refs replace their entire field, not a deep merge or append. Nullable fields accept null to clear. Ownership changes recheck old and new scope; clear the old owner with null when moving scope.", "memory.write", object(update, "memory_id"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
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
	id := object(map[string]any{"memory_id": memoryField(str(128), "Exact memory_id returned by an accessible memory create/get/search/list operation, not a source record ID or Workflow ID.")}, "memory_id")
	if err := add("memory_get", "Read accessible memory details. Check status and validity before treating it as a current fact.", "memory.read", id, func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		return b.Repository.GetMemory(ctx, e.Scope, decode[Memory](raw).ID)
	}); err != nil {
		return err
	}
	if err := add("memory_delete", "Permanently delete accessible memory, cascade its semantic relations and detach child memories. This is not archival and has no undo guarantee. A repeated delete may return not found; after an uncertain result inspect state before retrying.", "memory.write", id, func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		err := b.Repository.DeleteMemory(ctx, e.Scope, decode[Memory](raw).ID)
		return map[string]bool{"deleted": err == nil}, err
	}); err != nil {
		return err
	}
	search := map[string]any{
		"query":             memoryField(map[string]any{"type": "string", "maxLength": 512, "default": ""}, "One case-insensitive literal substring matched against title, content, category and structured JSON text. Spaces do not mean OR; use separate queries for alternatives. Omit or use empty string to browse."),
		"visibility":        memoryField(enum("PUB", "PRI"), "Optional exact visibility filter within the current user's authorized scope; omitted includes both accessible PUB and PRI."),
		"owner_agent_id":    memoryField(str(128), "Optional exact private-owner filter; only the current trusted agent_id is allowed. This filter does not grant additional scope."),
		"owner_workflow_id": memoryField(str(128), "Optional exact private-owner filter; only the currently bound workflow_id is allowed. Cannot query another Workflow's PRI."),
		"memory_type":       memoryField(enum("BASE", "TEMP"), "Optional exact memory-type filter; omitted includes BASE and TEMP."),
		"is_personal":       memoryField(map[string]any{"type": "boolean"}, "Optional personal-content filter. Omit for both; false explicitly selects nonpersonal entries."),
		"level":             memoryField(enum("L0", "L1", "L2", "L3"), "Optional exact organizational level filter; omitted includes all levels."),
		"status":            memoryField(enum("active", "archived", "invalid", "all"), "Defaults to active. Choose archived/invalid explicitly, or all to remove the status filter; validity is an independent AND filter."),
		"parent_memory_id":  memoryField(str(128), "Optional exact parent memory_id filter; does not recursively search descendants."),
		"validity":          memoryField(enum("current", "expired", "future", "all"), "Defaults to current using stored validity bounds and the repository clock. expired/future select those intervals; all removes this time filter but not status filtering."),
		"limit":             memoryField(integer(1, 100), "Maximum returned entries, default 50, at most 100. Results sort by priority descending, then creation time and memory_id. No cursor or has_more is returned; a limited result cannot prove complete coverage."),
	}
	search["status"].(map[string]any)["default"] = "active"
	search["validity"].(map[string]any)["default"] = "current"
	search["limit"].(map[string]any)["default"] = 50
	for _, spec := range []struct{ name, description string }{
		{"memory_search", "Search current-user PUB, own Agent PRI and bound Workflow PRI with AND filters, returning full memory records. Defaults active/current/limit 50; at most 100 entries, no pagination/completeness signal. Request historical status/validity explicitly."},
		{"memory_list", "List a compact index of accessible memories with the same filters, defaults and ordering as memory_search. Returns ID, title, optional category and status only. Call memory_get for the body, provenance or exact validity information."},
	} {
		if err := add(spec.name, spec.description, "memory.read", object(search), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
			q := decode[MemorySearch](raw)
			if q.Limit == 0 {
				q.Limit = 50
			}
			memories, err := b.Repository.SearchMemory(ctx, e.Scope, q)
			if err != nil || spec.name == "memory_search" {
				return memories, err
			}
			entries := make([]memoryListEntry, 0, len(memories))
			for _, m := range memories {
				entries = append(entries, memoryListEntry{ID: m.ID, Title: memoryListLabel(m.Title, 60), Category: memoryListLabel(m.Category, 24), Status: m.Status})
			}
			return entries, nil
		}); err != nil {
			return err
		}
	}
	relationTypes := memoryField(enum("RELATED_TO", "ALIAS_OF", "BELONGS_TO", "DERIVED_FROM", "ROUTES_TO", "SUPERSEDES"), "Directed meaning from source to target: RELATED_TO association; ALIAS_OF alternative naming; BELONGS_TO membership; DERIVED_FROM provenance; ROUTES_TO routing; SUPERSEDES replacement. A relation grants no endpoint access.")
	if err := add("memory_create_relation", "Link two accessible memories; does not grant access to either endpoint. Duplicate directed relations are rejected.", "memory.write", object(map[string]any{"source_memory_id": memoryField(str(128), "Exact accessible source memory_id from a memory read result; source and target must differ."), "target_memory_id": memoryField(str(128), "Exact accessible target memory_id. Direction is source to target, not interchangeable; both endpoints must be accessible."), "relation_type": relationTypes, "description": memoryField(map[string]any{"type": "string", "maxLength": 4096, "default": ""}, "Optional explanation of this directed relation, empty by default; it is business data, not an instruction or permission.")}, "source_memory_id", "target_memory_id", "relation_type"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		return b.Repository.CreateMemoryRelation(ctx, e.Scope, decode[MemoryRelation](raw))
	}); err != nil {
		return err
	}
	return add("memory_get_relations", "Find direct relations with both endpoints accessible; get returned memory IDs to expand context. Only depth 1 is supported.", "memory.read", object(map[string]any{
		"memory_id":     memoryField(str(128), "Exact accessible memory_id whose direct relations should be read; this is not a relation_id."),
		"relation_type": memoryField(enum("RELATED_TO", "ALIAS_OF", "BELONGS_TO", "DERIVED_FROM", "ROUTES_TO", "SUPERSEDES"), "Optional exact directed relation-type filter; omitted includes every type. Direction is evaluated relative to memory_id."),
		"direction":     memoryField(map[string]any{"type": "string", "enum": []string{"incoming", "outgoing", "both"}, "default": "both"}, "Defaults to both. incoming means queried memory is target; outgoing means it is source."),
		"limit":         memoryField(map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 50}, "Maximum direct relations, default 50 and maximum 100, sorted by creation time then relation_id. No cursor/has_more; result may be incomplete."),
		"depth":         memoryField(map[string]any{"type": "integer", "minimum": 1, "maximum": 1, "default": 1}, "Only direct depth 1 is supported; this is not recursive graph traversal. Read returned endpoint IDs separately if needed."),
	}, "memory_id"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		return b.Repository.GetMemoryRelations(ctx, e.Scope, decode[MemoryRelationQuery](raw))
	})
}
