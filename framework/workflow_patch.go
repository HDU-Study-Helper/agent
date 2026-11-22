package framework

import "encoding/json"

// PatchWorkflow preserves omitted metadata while replacing the explicitly
// required name and definition. Definition is an entire replacement, not a deep
// merge. The repository must still enforce its version compare-and-set because
// the Workflow may change after the read used to construct this update.
func PatchWorkflow(existing Workflow, raw json.RawMessage) (Workflow, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return Workflow{}, Fail("INVALID_ARGUMENT", "invalid workflow JSON")
	}
	allowed := map[string]bool{"workflow_id": true, "version": true, "name": true, "definition": true, "description": true, "status": true, "parent_workflow_id": true}
	for key := range fields {
		if !allowed[key] {
			return Workflow{}, Fail("INVALID_ARGUMENT", "unknown or immutable workflow field")
		}
	}
	for _, key := range []string{"workflow_id", "version", "name", "definition"} {
		if _, ok := fields[key]; !ok {
			return Workflow{}, Fail("INVALID_ARGUMENT", "workflow update requires workflow_id, version, name and definition")
		}
	}
	var next Workflow
	if err := json.Unmarshal(raw, &next); err != nil {
		return Workflow{}, Fail("INVALID_ARGUMENT", "invalid workflow update values")
	}
	if next.ID != existing.ID {
		return Workflow{}, Fail("INVALID_ARGUMENT", "workflow ID differs from loaded workflow")
	}
	if next.Version != existing.Version {
		return Workflow{}, Fail("INVALID_ARGUMENT", "workflow version conflict; read the current workflow before updating")
	}
	if _, provided := fields["parent_workflow_id"]; provided && next.ParentID != existing.ParentID {
		return Workflow{}, Fail("INVALID_ARGUMENT", "workflow parent is immutable")
	}
	if _, provided := fields["description"]; !provided {
		next.Description = existing.Description
	}
	if _, provided := fields["status"]; !provided {
		next.Status = existing.Status
	}
	next.ParentID = existing.ParentID
	return next, nil
}
