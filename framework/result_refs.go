package framework

// toolResultRefs is deliberately allowlisted: references come from successful
// registered persistence tools after output-schema validation, never model text.
func toolResultRefs(name string, result ToolResult) []ToolResultRef {
	if !result.OK || result.Error != nil || (name != "memory_create" && name != "memory_update" && name != "workflow_create") {
		return nil
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
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
