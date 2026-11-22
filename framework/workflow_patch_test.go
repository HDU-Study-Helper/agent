package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type workflowPatchRepo struct {
	Repository
	existing, saved Workflow
	saves           int
	owner           string
	race            bool
}

func (r *workflowPatchRepo) GetWorkflow(_ context.Context, owner, id string) (Workflow, error) {
	r.owner = owner
	if owner != r.existing.OwnerID || id != r.existing.ID {
		return Workflow{}, Fail("PERMISSION_DENIED", "not owned")
	}
	return r.existing, nil
}
func (r *workflowPatchRepo) SaveWorkflow(_ context.Context, _ Scope, w Workflow, create bool) (Workflow, error) {
	r.saves++
	r.saved = w
	if r.race {
		return Workflow{}, Fail("INVALID_ARGUMENT", "workflow version conflict")
	}
	w.Version++
	return w, nil
}
func patchWorkflowFixture() Workflow {
	return Workflow{ID: "wf", OwnerID: "owner", Name: "old", Description: "keep description", Definition: json.RawMessage(`{"old":true,"unrelated":"must not merge"}`), Version: 4, Status: "active", CreatedBy: "agent", CreatedAt: time.Now(), UpdatedAt: time.Now()}
}
func TestWorkflowUpdatePreservesOmittedMetadata(t *testing.T) {
	for _, parent := range []string{"", "parent"} {
		t.Run("parent-"+parent, func(t *testing.T) {
			old := patchWorkflowFixture()
			old.ParentID = parent
			r := &workflowPatchRepo{existing: old}
			tools := NewToolRegistry()
			if e := (Builtins{Repository: r}).Register(tools); e != nil {
				t.Fatal(e)
			}
			e := toolContext([]string{"workflow_update"}, []string{"workflow.write"})
			got := tools.Execute(context.Background(), e, "workflow_update", json.RawMessage(`{"workflow_id":"wf","version":4,"name":"new","definition":{"replacement":true}}`))
			if !got.OK || r.saves != 1 {
				t.Fatal(got, r.saves)
			}
			if r.owner != "owner" || r.saved.Status != "active" || r.saved.Description != old.Description || r.saved.ParentID != parent {
				t.Fatal(r.saved)
			}
			if string(r.saved.Definition) != `{"replacement":true}` {
				t.Fatalf("definition was merged instead of replaced: %s", r.saved.Definition)
			}
		})
	}
}
func TestWorkflowUpdateExplicitClearAndStatusChange(t *testing.T) {
	old := patchWorkflowFixture()
	next, e := PatchWorkflow(old, json.RawMessage(`{"workflow_id":"wf","version":4,"name":"new","definition":{},"description":"","status":"archived"}`))
	if e != nil || next.Description != "" || next.Status != "archived" {
		t.Fatal(next, e)
	}
}
func TestWorkflowUpdateRejectsParentChangeAndStaleBeforeSave(t *testing.T) {
	for _, extra := range []string{`,"parent_workflow_id":"other"`, `,"version":3`} {
		r := &workflowPatchRepo{existing: patchWorkflowFixture()}
		tools := NewToolRegistry()
		if err := (Builtins{Repository: r}).Register(tools); err != nil {
			t.Fatal(err)
		}
		raw := `{"workflow_id":"wf","version":4,"name":"new","definition":{}` + extra + `}`
		// A stale version is represented once, rather than duplicate JSON keys.
		if strings.Contains(extra, "version") {
			raw = `{"workflow_id":"wf","version":3,"name":"new","definition":{}}`
		}
		got := tools.Execute(context.Background(), toolContext([]string{"workflow_update"}, []string{"workflow.write"}), "workflow_update", json.RawMessage(raw))
		if got.Error == nil || got.Error.Code != "INVALID_ARGUMENT" || r.saves != 0 {
			t.Fatal(got, r.saves)
		}
	}
}
func TestWorkflowUpdateRetainsRepositoryCompareAndSet(t *testing.T) {
	r := &workflowPatchRepo{existing: patchWorkflowFixture(), race: true}
	tools := NewToolRegistry()
	if e := (Builtins{Repository: r}).Register(tools); e != nil {
		t.Fatal(e)
	}
	got := tools.Execute(context.Background(), toolContext([]string{"workflow_update"}, []string{"workflow.write"}), "workflow_update", json.RawMessage(`{"workflow_id":"wf","version":4,"name":"new","definition":{}}`))
	if got.Error == nil || r.saves != 1 {
		t.Fatal(got, r.saves)
	}
}

func TestMemoryWorkflowInputFieldsAreExplained(t *testing.T) {
	tools := NewToolRegistry()
	if err := (Builtins{}).Register(tools); err != nil {
		t.Fatal(err)
	}
	var check func(any, string)
	check = func(v any, path string) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if props, ok := m["properties"].(map[string]any); ok {
			for key, child := range props {
				field := child.(map[string]any)
				description, _ := field["description"].(string)
				if strings.TrimSpace(description) == "" {
					t.Errorf("missing description: %s.%s", path, key)
				}
				check(child, path+"."+key)
			}
		}
		if item, ok := m["items"].(map[string]any); ok {
			check(item, path+"[]")
		}
		for _, k := range []string{"oneOf", "anyOf"} {
			if branches, ok := m[k].([]any); ok {
				for _, b := range branches {
					check(b, path+"."+k)
				}
			}
		}
	}
	for _, s := range tools.Catalog() {
		if !strings.HasPrefix(s.Name, "memory_") && !strings.HasPrefix(s.Name, "workflow_") {
			continue
		}
		var raw any
		if json.Unmarshal(s.InputSchema, &raw) != nil {
			t.Fatal(s.Name)
		}
		check(raw, s.Name)
	}
}
func TestMemoryPatchDoesNotApplyCreateDefaults(t *testing.T) {
	old := Memory{ID: "memory", Visibility: "PUB", MemoryType: "TEMP", Level: "L3", Status: "archived", Category: "keep", Priority: 7, IsPersonal: true, Content: "old", StructuredContent: json.RawMessage(`{"old":true}`), SourceRefs: json.RawMessage(`["old"]`)}
	next, e := PatchMemory(old, json.RawMessage(`{"memory_id":"memory","content":"new","structured_content":{"new":true},"source_refs":null}`))
	if e != nil {
		t.Fatal(e)
	}
	if next.Visibility != "PUB" || next.MemoryType != "TEMP" || next.Level != "L3" || next.Status != "archived" || next.Priority != 7 || !next.IsPersonal || next.Category != "keep" {
		t.Fatal(next)
	}
	if string(next.StructuredContent) != `{"new":true}` || next.SourceRefs != nil {
		t.Fatal(next)
	}
}
