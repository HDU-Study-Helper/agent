package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDataWebNotificationDispatchInputFieldsExplained(t *testing.T) {
	r := NewToolRegistry()
	if err := (Builtins{}).Register(r); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"data_list_tables": true, "data_describe_table": true, "data_query": true, "data_search": true, "web_search": true, "web_fetch": true, "notification_send": true, "agent_dispatch": true}
	var inspect func(string, map[string]any)
	inspect = func(path string, schema map[string]any) {
		if props, ok := schema["properties"].(map[string]any); ok {
			for name, raw := range props {
				child, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("%s.%s is not a schema", path, name)
				}
				if text, _ := child["description"].(string); strings.TrimSpace(text) == "" {
					t.Errorf("%s.%s lacks a field description", path, name)
				}
				inspect(path+"."+name, child)
			}
		}
		if item, ok := schema["items"].(map[string]any); ok {
			if text, _ := item["description"].(string); strings.TrimSpace(text) == "" {
				t.Errorf("%s[] lacks an item description", path)
			}
			inspect(path+"[]", item)
		}
	}
	for _, spec := range r.Catalog() {
		if !wanted[spec.Name] {
			continue
		}
		delete(wanted, spec.Name)
		var schema map[string]any
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		inspect(spec.Name, schema)
	}
	if len(wanted) != 0 {
		t.Fatal("missing tools", wanted)
	}
}

type dataDefaultCapture struct {
	query  Query
	search KeywordSearchRequest
}

func (*dataDefaultCapture) Catalog(context.Context, Scope) ([]Dataset, error) {
	return []Dataset{{Name: "records", Fields: []Field{{Name: "title", Type: "string"}}}}, nil
}
func (d *dataDefaultCapture) Query(_ context.Context, _ Scope, q Query) (QueryResult, error) {
	d.query = q
	return QueryResult{Rows: []map[string]any{}}, nil
}
func (d *dataDefaultCapture) SearchKeywords(_ context.Context, _ Scope, q KeywordSearchRequest) (KeywordSearchResult, error) {
	d.search = q
	return KeywordSearchResult{Hits: []KeywordSearchHit{}, Coverage: []KeywordSearchCoverage{}}, nil
}

type webDefaultCapture struct{ request SearchRequest }

func (s *webDefaultCapture) Search(_ context.Context, q SearchRequest) ([]SearchResult, error) {
	s.request = q
	return []SearchResult{}, nil
}
func (*webDefaultCapture) Fetch(_ context.Context, url string) (Page, error) {
	return Page{URL: url}, nil
}

func TestDataWebSchemaDefaultAnnotationsMatchHandlers(t *testing.T) {
	data, search := &dataDefaultCapture{}, &webDefaultCapture{}
	r := NewToolRegistry()
	if err := (Builtins{Data: data, Search: search}).Register(r); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"data_query":  `{"dataset":"records","fields":["title"]}`,
		"data_search": `{"keywords":["assignment"]}`,
		"web_search":  `{"query":"public topic"}`,
	} {
		e := toolContext([]string{name}, []string{"data.read", "web.search"})
		if result := r.Execute(context.Background(), e, name, json.RawMessage(input)); !result.OK {
			t.Fatal(name, result)
		}
	}
	if data.query.Limit != 20 || data.query.Offset != 0 || data.search.Limit != 50 || search.request.Count != 5 {
		t.Fatalf("handler defaults changed: query=%+v search=%+v web=%+v", data.query, data.search, search.request)
	}
	expected := map[string]map[string]float64{"data_query": {"limit": 20, "offset": 0}, "data_search": {"limit": 50}, "web_search": {"count": 5}}
	for _, spec := range r.Catalog() {
		fields, ok := expected[spec.Name]
		if !ok {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		props := schema["properties"].(map[string]any)
		for field, want := range fields {
			if got := props[field].(map[string]any)["default"]; got != want {
				t.Errorf("%s.%s default=%v, want %v", spec.Name, field, got, want)
			}
		}
	}
}

func TestDispatchTargetProjectionRetainsBaseFieldGuidance(t *testing.T) {
	repo := dispatchTargetsRepo{list: func(context.Context, string, int, string) ([]AgentInstance, error) {
		return []AgentInstance{fixtureAgent("target")}, nil
	}}
	base := ToolSpec{Name: "agent_dispatch", InputSchema: dispatchInputSchema()}
	before := dispatchTargetProperty(t, base)["description"].(string)
	projected := attachDispatchTargets(context.Background(), repo, Scope{OwnerID: "owner", AgentID: "self"}, []ToolSpec{base})
	after := dispatchTargetProperty(t, projected[0])["description"].(string)
	if !strings.HasPrefix(after, before) || !strings.Contains(after, `"id":"target"`) {
		t.Fatal("dynamic target enum dropped base guidance", after)
	}
}
