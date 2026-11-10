package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type manyWorkflowsRepo struct {
	Repository
	items []Workflow
}

func (r manyWorkflowsRepo) ListWorkflows(context.Context, string, int, string) ([]Workflow, error) {
	return r.items, nil
}
func TestWorkflowSummaryDropsLargeEntityEvidence(t *testing.T) {
	entity := map[string]any{"kind": "course", "id": "stable-course-id", "name": "具体课程", "source_evidence": strings.Repeat("private evidence ", 10000)}
	def, _ := json.Marshal(map[string]any{"entity": entity, "goal": strings.Repeat("goal", 1000)})
	items := []Workflow{}
	for i := 0; i < 100; i++ {
		items = append(items, Workflow{ID: NewID(), OwnerID: "owner", Name: strings.Repeat("name", 100), Description: strings.Repeat("description", 100), Definition: def, Status: "active", Version: 1})
	}
	tools := NewToolRegistry()
	if err := (Builtins{Repository: manyWorkflowsRepo{items: items}}).Register(tools); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"workflow_list"}, []string{"workflow.read"})
	e.Limits.ToolResultBytes = 128 << 10
	result := tools.Execute(context.Background(), e, "workflow_list", json.RawMessage(`{"limit":100}`))
	if !result.OK {
		t.Fatal(result)
	}
	raw, _ := json.Marshal(result.Data)
	if len(raw) > 128<<10 || strings.Contains(string(raw), "private evidence") {
		t.Fatal("unbounded entity snapshot leaked into list", len(raw))
	}
	var rows []Workflow
	json.Unmarshal(raw, &rows)
	if len(rows) != 100 {
		t.Fatal("pagination silently reduced")
	}
}
func TestOversizedDispatchReturnsMarkedSummaryWithoutReexecuting(t *testing.T) {
	executions := 0
	full, _ := json.Marshal(map[string]any{"status": "COMPLETED", "workflow_id": "wf", "workflow_version": 3, "summary": "verified result", "artifacts": []any{map[string]any{"type": "SUMMARY", "entity_ref": "record-1", "title": "title", "data": strings.Repeat("private-detail", 10000)}}, "evidence_refs": []string{"e1"}})
	tools := NewToolRegistry()
	if err := (Builtins{Dispatcher: dispatchFunc(func(context.Context, ExecutionContext, RunRequest) (RunResult, error) {
		executions++
		return RunResult{RunID: "child-run", Output: string(full)}, nil
	})}).Register(tools); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"agent_dispatch"}, []string{"agent.dispatch"})
	e.Limits.ToolResultBytes = 1024
	result := tools.Execute(context.Background(), e, "agent_dispatch", json.RawMessage(`{"target_agent_id":"child","task":"check"}`))
	if !result.OK || executions != 1 {
		t.Fatal(result, executions)
	}
	raw, _ := json.Marshal(result.Data)
	var returned RunResult
	json.Unmarshal(raw, &returned)
	var compact map[string]any
	json.Unmarshal([]byte(returned.Output), &compact)
	if compact["status"] != "COMPLETED" || compact["truncated"] != true || compact["full_output_run_id"] != "child-run" || strings.Contains(string(raw), "private-detail") {
		t.Fatal(string(raw))
	}
	if len(full) < 100000 {
		t.Fatal("test fixture not oversized")
	}
}
