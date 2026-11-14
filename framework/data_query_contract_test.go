package framework

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDataQueryStringFilterContractRejectsBeforeExecution(t *testing.T) {
	data := &mockData{}
	tools := NewToolRegistry()
	if err := (Builtins{Repository: newFakeRepo(), Data: data}).Register(tools); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"data_query"}, []string{"data.read"})
	for _, value := range []string{`42`, `true`, `null`} {
		result := tools.Execute(context.Background(), e, "data_query", json.RawMessage(`{"dataset":"records","fields":["title"],"filters":[{"field":"title","operator":"eq","value":`+value+`}]}`))
		if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("unsupported filter value accepted: %s %+v", value, result)
		}
	}
	if data.calls != 0 {
		t.Fatal("invalid filter reached backend")
	}
	result := tools.Execute(context.Background(), e, "data_query", json.RawMessage(`{"dataset":"records","fields":["title"],"filters":[{"field":"title","operator":"eq","value":"42"}]}`))
	if !result.OK || data.calls != 1 {
		t.Fatalf("string filter rejected: %+v", result)
	}
}

func TestQueryPageLimitReasonSurvivesModelCleaning(t *testing.T) {
	next := 3
	page := QueryResult{Rows: []map[string]any{{"id": "one"}}, HasMore: true, NextOffset: &next, PageLimitReason: "byte_budget"}
	cleaned := CleanQueryData(page).(map[string]any)
	if cleaned["page_limit_reason"] != "byte_budget" || cleaned["next_offset"] != 3 || cleaned["has_more"] != true {
		t.Fatal(cleaned)
	}
	schema, err := compileSchema(builtinOutputSchema("data_query"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	var value any
	_ = json.Unmarshal(raw, &value)
	if err := schema.Validate(value); err != nil {
		t.Fatal(err)
	}
	missing := map[string]any{"rows": []any{}}
	if schema.Validate(missing) == nil {
		t.Fatal("ambiguous missing has_more accepted")
	}
	boundary := QueryResult{Rows: []map[string]any{}, HasMore: true, PageLimitReason: "offset_limit"}
	raw, _ = json.Marshal(boundary)
	_ = json.Unmarshal(raw, &value)
	if err := schema.Validate(value); err != nil {
		t.Fatal(err)
	}
}
