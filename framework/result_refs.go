package framework

// toolResultRefs is deliberately allowlisted: references come from successful
// registered persistence tools after output-schema validation, never model text.
func toolResultRefs(name string, result ToolResult) []ToolResultRef {
	if !result.OK || result.Error != nil {
		return nil
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		return nil
	}
	switch name {
	case "public_todo_upsert", "public_todo_complete", "notice_create", "planned_job_create", "planned_job_cancel":
		id, _ := data["id"].(string)
		kind, _ := data["kind"].(string)
		if id == "" || len(id) > 160 {
			return nil
		}
		switch {
		case (name == "public_todo_upsert" || name == "public_todo_complete") && kind == "todo":
			return []ToolResultRef{{Kind: "TASK", ID: id}}
		case name == "notice_create" && kind == "notice":
			return []ToolResultRef{{Kind: "NOTICE", ID: id}}
		case (name == "planned_job_create" || name == "planned_job_cancel" || name == "notice_create") && kind == "job":
			return []ToolResultRef{{Kind: "PLANNED_JOB", ID: id}}
		}
		return nil
	case "memory_create", "memory_update", "workflow_create":
	default:
		return nil
	}
	if name == "workflow_create" {
		id, ok := data["workflow_id"].(string)
		if !ok || id == "" || len(id) > 128 {
			return nil
		}
		refs := []ToolResultRef{{Kind: "WORKFLOW", ID: id}}
		receipt, ok := data["profile_initialization"].(map[string]any)
		if !ok || receipt["verified"] != true || receipt["visibility"] != "PRI" || receipt["owner_workflow_id"] != id {
			return refs
		}
		ids, ok := receipt["memory_ids"].([]any)
		if !ok || len(ids) > MaxWorkflowPrivateMemories {
			return refs
		}
		for _, value := range ids {
			memoryID, ok := value.(string)
			if !ok || memoryID == "" || len(memoryID) > 128 {
				return refs[:1]
			}
			refs = append(refs, ToolResultRef{Kind: "MEMORY", ID: memoryID})
		}
		return refs
	}
	id, ok := data["memory_id"].(string)
	if !ok || id == "" || len(id) > 128 {
		return nil
	}
	return []ToolResultRef{{Kind: "MEMORY", ID: id}}
}
