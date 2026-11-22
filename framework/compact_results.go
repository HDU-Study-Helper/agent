package framework

import (
	"encoding/json"
	"unicode/utf8"
)

func compactText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}

// Dispatch responses can be much larger than the caller's tool budget. Preserve
// the complete child output in the host observer/run store, but send the parent
// a clearly marked summary with identities rather than fail after child writes.
func compactDispatchData(data any, limit int) (any, bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}
	var result RunResult
	if json.Unmarshal(raw, &result) != nil || result.RunID == "" || result.Error != nil {
		return nil, false
	}
	var original map[string]json.RawMessage
	if json.Unmarshal([]byte(result.Output), &original) != nil {
		return nil, false
	}
	summary := map[string]any{"truncated": true, "full_output_run_id": result.RunID, "note": "Tool response compacted; full child output is stored under this run_id. Large fields are omitted; do not infer omitted evidence or operations."}
	for _, key := range []string{"status", "operation", "verified", "workflow_id", "workflow_version", "run_id", "request_id"} {
		if value, ok := original[key]; ok && len(value) <= 2048 {
			summary[key] = value
		}
	}
	for _, key := range []string{"summary", "reason"} {
		var text string
		if json.Unmarshal(original[key], &text) == nil {
			summary[key] = compactText(text, 512)
		}
	}
	// These are handoff data, not optional audit details. Preserve an actionable
	// question even when large evidence/artifacts force the compact path.
	if value := original["interaction"]; len(value) > 0 && string(value) != "null" {
		summary["interaction"] = value
	}
	var childText string
	if json.Unmarshal(original["text"], &childText) == nil && childText != "" {
		summary["text"] = compactText(childText, 2048)
	}
	refs := []any{}
	var artifacts []map[string]json.RawMessage
	json.Unmarshal(original["artifacts"], &artifacts)
	for _, a := range artifacts {
		ref := map[string]json.RawMessage{}
		for _, key := range []string{"type", "entity_ref", "title"} {
			if value, ok := a[key]; ok && len(value) <= 1024 {
				ref[key] = value
			}
		}
		refs = append(refs, ref)
		if len(refs) >= 12 {
			break
		}
	}
	if len(refs) > 0 {
		summary["artifact_refs"] = refs
	}
	summary["artifacts_omitted"] = len(artifacts) > len(refs)
	for _, key := range []string{"evidence_refs", "questions"} {
		if value, ok := original[key]; ok && len(value) <= 4096 {
			summary[key] = value
		} else if ok {
			summary[key+"_omitted"] = true
		}
	}
	for pass := 0; pass < 3; pass++ {
		out, err := json.Marshal(summary)
		if err != nil {
			return nil, false
		}
		result.Output = string(out)
		encoded, err := json.Marshal(result)
		if err == nil && len(encoded) <= limit {
			return result, true
		}
		if pass == 0 {
			delete(summary, "artifact_refs")
			delete(summary, "evidence_refs")
			delete(summary, "questions")
			summary["details_omitted"] = true
		} else {
			delete(summary, "summary")
			delete(summary, "reason")
			delete(summary, "note")
			if childText != "" {
				summary["text"] = compactText(childText, 256)
			}
			// An exceptionally large CHOICE list can still exceed the caller's
			// budget. Never discard the server-issued identity needed to answer.
			if v, ok := summary["interaction"].(json.RawMessage); ok {
				var q struct {
					ID   string `json:"question_id"`
					Text string `json:"question"`
					Type string `json:"type"`
				}
				if json.Unmarshal(v, &q) == nil && q.ID != "" {
					summary["interaction"] = map[string]any{"question_id": q.ID, "type": q.Type, "question": compactText(q.Text, 192), "details_omitted": true}
				}
			}
		}
	}
	return nil, false
}
