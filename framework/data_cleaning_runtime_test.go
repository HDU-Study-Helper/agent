package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type cleaningRuntimeData struct{ investigationRuntimeData }

func (*cleaningRuntimeData) Catalog(context.Context, Scope) ([]Dataset, error) {
	return []Dataset{{Name: "academic_items", Fields: []Field{{Name: "id"}, {Name: "parent_id"}, {Name: "native_id"}, {Name: "metadata"}, {Name: "text"}, {Name: "title"}}}}, nil
}
func TestRuntimeDataCleaningPreservesSelfTopicAndFalseBeforeModel(t *testing.T) {
	round := 0
	data := &cleaningRuntimeData{investigationRuntimeData: investigationRuntimeData{query: func(Query) QueryResult {
		return QueryResult{Rows: []map[string]any{{"id": "60619", "parent_id": "topic-24", "native_id": "reply-1", "metadata": `{"is_current_user":true,"count":0,"current_user_state":{},"assessment_time_fields":{"node":{},"test":{}}}`, "text": "", "title": ""}, {"id": "other", "parent_id": "topic-24", "metadata": `{"is_current_user":false,"count":0}`}}, HasMore: false}
	}}}
	model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		round++
		if round == 1 {
			return calls(call("q", "data_query", `{"dataset":"academic_items","fields":["id","parent_id","native_id","metadata","text","title"],"limit":100}`)), nil
		}
		got := investigationToolResult(t, req, "q")
		b, _ := json.Marshal(got.Data)
		if !strings.Contains(string(b), `"is_current_user":true`) || !strings.Contains(string(b), `"is_current_user":false`) || !strings.Contains(string(b), `"count":0`) || !strings.Contains(string(b), "topic-24") || strings.Contains(string(b), "assessment_time_fields") {
			t.Fatalf("evidence or empty shell cleaning incorrect: %s", b)
		}
		return final("self reply topic-24 confirmed"), nil
	})
	runtime, _, _ := newInvestigationTestRuntime(t, data, "learner", true, model)
	got := runInvestigationTest(t, runtime)
	if got.Output != "self reply topic-24 confirmed" {
		t.Fatal(got)
	}
}
