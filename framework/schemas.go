package framework

import "encoding/json"

// Output schemas describe the data field of the shared {ok,data,error} envelope.
func builtinOutputSchema(name string) json.RawMessage {
	text := map[string]any{"type": "string"}
	number := map[string]any{"type": "integer"}
	timestamp := map[string]any{"type": "string", "format": "date-time"}
	record := func(props map[string]any, required ...string) map[string]any {
		raw := object(props, required...)
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		return value
	}
	array := func(item any) map[string]any { return map[string]any{"type": "array", "items": item} }
	memory := record(map[string]any{
		"memory_id": text, "owner_user_id": text, "visibility": enum("PUB", "PRI"), "owner_agent_id": text, "owner_workflow_id": text,
		"memory_type": enum("BASE", "TEMP"), "is_personal": map[string]any{"type": "boolean"}, "level": enum("L0", "L1", "L2", "L3"), "parent_memory_id": text,
		"title": text, "content": text, "structured_content": map[string]any{"type": "object"}, "source_refs": array(map[string]any{}),
		"valid_from": timestamp, "valid_until": timestamp, "status": enum("active", "archived", "invalid"), "category": text, "priority": number, "created_at": timestamp, "updated_at": timestamp,
	}, "memory_id", "owner_user_id", "visibility", "memory_type", "is_personal", "level", "title", "content", "status", "category", "priority", "created_at", "updated_at")
	relation := record(map[string]any{"relation_id": text, "owner_user_id": text, "source_memory_id": text, "target_memory_id": text, "relation_type": enum("RELATED_TO", "ALIAS_OF", "BELONGS_TO", "DERIVED_FROM", "ROUTES_TO", "SUPERSEDES"), "description": text, "created_by_agent_id": text, "created_at": timestamp}, "relation_id", "owner_user_id", "source_memory_id", "target_memory_id", "relation_type", "description", "created_by_agent_id", "created_at")
	workflow := record(map[string]any{"workflow_id": text, "owner_user_id": text, "name": text, "description": text, "parent_workflow_id": text, "definition": map[string]any{"type": "object"}, "version": number, "status": text, "created_by_agent_id": text, "created_at": timestamp, "updated_at": timestamp}, "workflow_id", "owner_user_id", "name", "description", "definition", "version", "status", "created_by_agent_id", "created_at", "updated_at")
	dataset := record(map[string]any{"name": text, "fields": array(record(map[string]any{"name": text, "type": text}, "name", "type"))}, "name", "fields")
	var schema any
	switch name {
	case "memory_create", "memory_get", "memory_update":
		schema = memory
	case "memory_create_relation":
		schema = relation
	case "memory_get_relations":
		schema = array(relation)
	case "memory_list", "memory_search":
		schema = array(memory)
	case "memory_delete", "workflow_delete":
		schema = record(map[string]any{"deleted": map[string]any{"const": true}}, "deleted")
	case "workflow_create":
		workflow["properties"].(map[string]any)["profile_initialization"] = record(map[string]any{
			"memory_ids": array(str(128)), "count": integer(1, MaxWorkflowPrivateMemories), "visibility": enum("PRI"), "owner_workflow_id": str(128), "verified": map[string]any{"const": true}, "content_sha256": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
		}, "memory_ids", "count", "visibility", "owner_workflow_id", "verified", "content_sha256")
		schema = workflow
	case "workflow_get", "workflow_update":
		schema = workflow
	case "workflow_list":
		schema = array(workflow)
	case "notification_send":
		schema = record(map[string]any{"notification_id": str(256), "status": str(128)}, "notification_id", "status")
	case "data_list_tables":
		schema = array(dataset)
	case "data_describe_table":
		schema = dataset
	case "data_query":
		schema = record(map[string]any{"rows": array(map[string]any{"type": "object"}), "has_more": map[string]any{"type": "boolean"}, "next_offset": integer(0, 10000)}, "rows")
	case "data_search":
		schema = record(map[string]any{
			"hits":     array(record(map[string]any{"dataset": text, "row": map[string]any{"type": "object"}}, "dataset", "row")),
			"coverage": array(record(map[string]any{"dataset": text, "matched": integer(0, 2147483647), "complete": map[string]any{"type": "boolean"}}, "dataset", "matched", "complete")),
			"has_more": map[string]any{"type": "boolean"}, "next_cursor": str(8192),
		}, "hits", "coverage", "has_more")
	case "web_search":
		schema = array(record(map[string]any{"title": text, "url": text, "snippet": text, "summary": text, "content": text, "site_name": map[string]any{"type": []string{"string", "null"}}, "date": map[string]any{"type": []string{"string", "null"}}}, "title", "url", "snippet", "summary", "content"))
	case "web_fetch":
		schema = record(map[string]any{"url": text, "title": text, "content": text}, "url", "title", "content")
	case "agent_dispatch":
		schema = record(map[string]any{"run_id": str(128), "output": text, "error": map[string]any{"type": "null"}}, "run_id", "output", "error")
	default:
		schema = map[string]any{}
	}
	raw, _ := json.Marshal(schema)
	return raw
}
