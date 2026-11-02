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
	memory := record(map[string]any{"memory_id": text, "owner_user_id": text, "agent_id": text, "title": text, "content": text, "category": text, "priority": number, "created_at": timestamp, "updated_at": timestamp}, "memory_id", "owner_user_id", "agent_id", "title", "content", "category", "priority", "created_at", "updated_at")
	workflow := record(map[string]any{"workflow_id": text, "owner_user_id": text, "name": text, "description": text, "parent_workflow_id": text, "definition": map[string]any{"type": "object"}, "version": number, "status": text, "created_by_agent_id": text, "created_at": timestamp, "updated_at": timestamp}, "workflow_id", "owner_user_id", "name", "description", "definition", "version", "status", "created_by_agent_id", "created_at", "updated_at")
	dataset := record(map[string]any{"name": text, "fields": array(record(map[string]any{"name": text, "type": text}, "name", "type"))}, "name", "fields")
	var schema any
	switch name {
	case "memory_create", "memory_get", "memory_update":
		schema = memory
	case "memory_list", "memory_search":
		schema = array(memory)
	case "memory_delete", "workflow_delete":
		schema = record(map[string]any{"deleted": map[string]any{"const": true}}, "deleted")
	case "workflow_create", "workflow_get", "workflow_update":
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
		schema = record(map[string]any{"rows": array(map[string]any{"type": "object"})}, "rows")
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
