package framework

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInvestigationRequiresPagingAndSchemaRepair(t *testing.T) {
	p := investigationProgress{}
	args, _ := json.Marshal(KeywordSearchRequest{Keywords: []string{"数分"}, Limit: 100})
	c := ToolCall{Function: FunctionCall{Name: "data_search", Arguments: string(args)}}
	p.observe(c, ToolResult{OK: true, Data: KeywordSearchResult{HasMore: true, NextCursor: "cursor"}})
	if !strings.Contains(p.continuation(), "cursor") {
		t.Fatal("missing continuation")
	}
	c.Function.Arguments = `{"keywords":["数分"],"limit":50,"cursor":"cursor"}`
	p.observe(c, ToolResult{OK: true, Data: KeywordSearchResult{HasMore: false}})
	if p.continuation() != "" {
		t.Fatal(p.continuation())
	}
	c.Function.Name = "workflow_create"
	p.observe(c, ToolResult{Error: Fail("INVALID_ARGUMENT", "missing source_refs")})
	if !strings.Contains(p.continuation(), "source_refs") {
		t.Fatal("missing repair")
	}
	p.observe(c, ToolResult{OK: true, Data: map[string]any{"workflow_id": "w"}})
	if p.continuation() != "" {
		t.Fatal("repair not cleared")
	}
}
