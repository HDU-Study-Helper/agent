package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type profileRepository struct {
	Repository
	ordinary, bootstrap int
	scope               Scope
	seeds               []Memory
}

func (r *profileRepository) SaveWorkflow(_ context.Context, s Scope, w Workflow, create bool) (Workflow, error) {
	r.ordinary++
	w.ID, w.OwnerID, w.Version, w.CreatedBy, w.Status = "created", s.OwnerID, 1, s.AgentID, "draft"
	return w, nil
}
func (r *profileRepository) CreateWorkflowWithMemories(ctx context.Context, s Scope, w Workflow, seeds []Memory) (WorkflowProfileCreation, error) {
	r.bootstrap++
	r.scope, r.seeds = s, seeds
	w, _ = r.SaveWorkflow(ctx, s, w, true)
	return WorkflowProfileCreation{Workflow: w, ProfileInitialization: WorkflowProfileReceipt{MemoryIDs: []string{"stored"}, Count: 1, Visibility: "PRI", OwnerWorkflowID: w.ID, Verified: true, ContentSHA256: strings.Repeat("a", 64)}}, nil
}
func TestWorkflowProfileToolCapabilityPermissionsAndReceipt(t *testing.T) {
	r := &profileRepository{}
	tools := NewToolRegistry()
	if err := (Builtins{Repository: r}).Register(tools); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"workflow_create", "workflow_update"}, []string{"workflow.write"})
	plain := json.RawMessage(`{"name":"course","definition":{}}`)
	if got := tools.Execute(context.Background(), e, "workflow_create", plain); !got.OK || r.ordinary != 1 {
		t.Fatal(got)
	}
	seeded := json.RawMessage(`{"name":"course","definition":{},"initial_private_memories":[{"title":"profile","content":"private knowledge","source_refs":[{"dataset":"academic_offerings","id":"course-1"}]}]}`)
	got := tools.Execute(context.Background(), e, "workflow_create", seeded)
	if got.Error == nil || got.Error.Code != "PERMISSION_DENIED" || r.bootstrap != 0 {
		t.Fatal(got)
	}
	e.Permissions = append(e.Permissions, "memory.write")
	got = tools.Execute(context.Background(), e, "workflow_create", seeded)
	if !got.OK || r.bootstrap != 1 || r.scope.WorkflowID != "" || e.Scope.WorkflowID != "" || r.seeds[0].WorkflowID != "" {
		t.Fatal(got, r)
	}
	data := got.Data.(map[string]any)
	receipt := data["profile_initialization"].(map[string]any)
	if data["workflow_id"] != "created" || receipt["verified"] != true || receipt["owner_workflow_id"] != "created" {
		t.Fatal(data)
	}
	refs := toolResultRefs("workflow_create", got)
	if len(refs) != 2 || refs[0].Kind != "WORKFLOW" || refs[0].ID != "created" || refs[1].Kind != "MEMORY" || refs[1].ID != "stored" {
		t.Fatal(refs)
	}
	receipt["verified"] = false
	if refs = toolResultRefs("workflow_create", got); len(refs) != 1 {
		t.Fatal("unverified receipt became memory references", refs)
	}
	for _, field := range []string{`"owner_user_id":"foreign",`, `"owner_agent_id":"foreign",`, `"owner_workflow_id":"foreign",`, `"visibility":"PUB",`, `"memory_id":"existing",`, `"parent_memory_id":"foreign",`} {
		bad := strings.Replace(string(seeded), `"title":"profile",`, field+`"title":"profile",`, 1)
		got = tools.Execute(context.Background(), e, "workflow_create", json.RawMessage(bad))
		if got.Error == nil || got.Error.Code != "INVALID_ARGUMENT" {
			t.Fatal(field, got)
		}
	}
	got = tools.Execute(context.Background(), e, "workflow_update", json.RawMessage(`{"workflow_id":"created","version":1,"name":"course","definition":{},"initial_private_memories":[]}`))
	if got.Error == nil || got.Error.Code != "INVALID_ARGUMENT" {
		t.Fatal(got)
	}
	legacy := NewToolRegistry()
	if err := (Builtins{Repository: summaryWorkflowRepo{}}).Register(legacy); err != nil {
		t.Fatal(err)
	}
	got = legacy.Execute(context.Background(), e, "workflow_create", seeded)
	if got.Error == nil || got.Error.Code != "NOT_IMPLEMENTED" {
		t.Fatal(got)
	}
}

type keywordData struct {
	mockData
	scope   Scope
	request KeywordSearchRequest
}

func (d *keywordData) SearchKeywords(_ context.Context, s Scope, q KeywordSearchRequest) (KeywordSearchResult, error) {
	d.scope, d.request = s, q
	return KeywordSearchResult{Hits: []KeywordSearchHit{{Dataset: "schedules", Row: map[string]any{"id": "schedule"}}}, Coverage: []KeywordSearchCoverage{{Dataset: "schedules", Matched: 1, Complete: true}}}, nil
}
func TestDataSearchOptionalCapabilityAndScope(t *testing.T) {
	d := &keywordData{}
	tools := NewToolRegistry()
	if err := (Builtins{Data: d}).Register(tools); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"data_search"}, []string{"data.read"})
	got := tools.Execute(context.Background(), e, "data_search", json.RawMessage(`{"keywords":["calculus","teacher"],"datasets":["schedules"]}`))
	if !got.OK || d.scope.OwnerID != e.Scope.OwnerID || d.request.Limit != 50 || len(d.request.Keywords) != 2 {
		t.Fatal(got, d)
	}
	for _, raw := range []string{`{"keywords":[]}`, `{"keywords":["course"],"datasets":["memories"]}`, `{"keywords":["course"],"owner_user_id":"foreign"}`} {
		got = tools.Execute(context.Background(), e, "data_search", json.RawMessage(raw))
		if got.Error == nil || got.Error.Code != "INVALID_ARGUMENT" {
			t.Fatal(got)
		}
	}
}
