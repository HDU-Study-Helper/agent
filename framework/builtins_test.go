package framework

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type mockData struct {
	calls                int
	overRows, overFields bool
}

func (*mockData) Catalog(context.Context, Scope) ([]Dataset, error) {
	return []Dataset{{Name: "records", Fields: []Field{{Name: "title", Type: "string"}}}}, nil
}
func (d *mockData) Query(ctx context.Context, s Scope, q Query) (QueryResult, error) {
	d.calls++
	if s.OwnerID != "owner" {
		panic("wrong owner")
	}
	if d.overRows {
		return QueryResult{Rows: []map[string]any{{"title": "a"}, {"title": "b"}}}, nil
	}
	if d.overFields {
		return QueryResult{Rows: []map[string]any{{"password": "secret"}}}, nil
	}
	return QueryResult{Rows: []map[string]any{{"title": "owned"}}}, nil
}

type mockNotification struct {
	owner string
	err   error
}

func (n *mockNotification) Send(_ context.Context, s Scope, _ Notification) (NotificationReceipt, error) {
	n.owner = s.OwnerID
	return NotificationReceipt{ID: "n1", Status: "accepted"}, n.err
}
func toolContext(names, permissions []string) ExecutionContext {
	return ExecutionContext{Scope: Scope{OwnerID: "owner", AgentID: "a"}, RunID: "run", EnabledTools: names, Permissions: permissions, Limits: Limits{}.Defaults(), budget: &budget{models: 10, tools: 100}}
}

type summaryWorkflowRepo struct {
	Repository
	workflow Workflow
}

func (r summaryWorkflowRepo) ListWorkflows(context.Context, string, int, string) ([]Workflow, error) {
	return []Workflow{r.workflow}, nil
}
func (r summaryWorkflowRepo) GetWorkflow(context.Context, string, string) (Workflow, error) {
	return r.workflow, nil
}
func TestWorkflowListSummarizesDefinitionButGetStaysComplete(t *testing.T) {
	repo := summaryWorkflowRepo{workflow: Workflow{ID: "wf", OwnerID: "owner", Name: "specific course", Description: "description", Version: 2, Status: "active", Definition: json.RawMessage(`{"entity":{"kind":"course","id":"course-1"},"goal":"verify records","instructions":"large repeated instructions"}`)}}
	registry := NewToolRegistry()
	if err := (Builtins{Repository: repo}).Register(registry); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"workflow_list", "workflow_get"}, []string{"workflow.read"})
	result := registry.Execute(context.Background(), e, "workflow_list", json.RawMessage(`{}`))
	if !result.OK {
		t.Fatal(result)
	}
	raw, _ := json.Marshal(result.Data)
	var list []Workflow
	if json.Unmarshal(raw, &list) != nil || len(list) != 1 {
		t.Fatal(string(raw))
	}
	var definition map[string]any
	json.Unmarshal(list[0].Definition, &definition)
	if definition["entity"] == nil || definition["goal"] == nil || definition["instructions"] != nil {
		t.Fatal(definition)
	}
	result = registry.Execute(context.Background(), e, "workflow_get", json.RawMessage(`{"workflow_id":"wf"}`))
	if !result.OK {
		t.Fatal(result)
	}
	raw, _ = json.Marshal(result.Data)
	var full Workflow
	json.Unmarshal(raw, &full)
	json.Unmarshal(full.Definition, &definition)
	if definition["instructions"] == nil {
		t.Fatal("full definition lost")
	}
}
func TestBuiltinDataAuthorizationAndOutputChecks(t *testing.T) {
	d := &mockData{}
	registry := NewToolRegistry()
	if err := (Builtins{Data: d}).Register(registry); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"data_query", "data_list_tables", "data_describe_table"}, []string{"data.read"})
	for _, input := range []string{`{"dataset":"memories","fields":["content"]}`, `{"dataset":"memory_relations","fields":["source_memory_id"]}`, `{"dataset":"agent_memories","fields":["title"]}`, `{"dataset":"records","fields":["password"]}`, `{"dataset":"records","fields":["title"],"filters":[{"field":"owner_user_id","operator":"eq","value":"other"}]}`, `{"dataset":"records","fields":["title"],"limit":101}`, `{"dataset":"records","fields":["title"],"sql":"SELECT * FROM users"}`} {
		result := registry.Execute(context.Background(), e, "data_query", json.RawMessage(input))
		if result.OK {
			t.Fatal("unsafe query accepted", input)
		}
	}
	if d.calls != 0 {
		t.Fatal("unsafe request reached service")
	}
	result := registry.Execute(context.Background(), e, "data_query", json.RawMessage(`{"dataset":"records","fields":["title"],"limit":1}`))
	if !result.OK {
		t.Fatal(result)
	}
	d.overRows = true
	result = registry.Execute(context.Background(), e, "data_query", json.RawMessage(`{"dataset":"records","fields":["title"],"limit":1}`))
	if result.Error.Code != "INTERNAL_ERROR" {
		t.Fatal(result)
	}
	d.overRows = false
	d.overFields = true
	result = registry.Execute(context.Background(), e, "data_query", json.RawMessage(`{"dataset":"records","fields":["title"],"limit":1}`))
	if result.Error.Code != "INTERNAL_ERROR" {
		t.Fatal(result)
	}
}
func TestNotificationTrustedOwnerPermissionsAndFailure(t *testing.T) {
	service := &mockNotification{}
	tools := NewToolRegistry()
	if err := (Builtins{Notifications: service}).Register(tools); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"notification_send"}, nil)
	input := json.RawMessage(`{"title":"title","body":"body","category":"test"}`)
	result := tools.Execute(context.Background(), e, "notification_send", input)
	if result.Error.Code != "PERMISSION_DENIED" || service.owner != "" {
		t.Fatal("notification permission bypass")
	}
	e.Permissions = []string{"notification.send"}
	result = tools.Execute(context.Background(), e, "notification_send", json.RawMessage(`{"title":"title","body":"body","category":"test","owner_user_id":"other"}`))
	if result.Error.Code != "INVALID_ARGUMENT" || service.owner != "" {
		t.Fatal("recipient override")
	}
	result = tools.Execute(context.Background(), e, "notification_send", input)
	if !result.OK || service.owner != "owner" {
		t.Fatal("trusted owner missing", result)
	}
	service.err = Fail("PROVIDER_ERROR", "delivery request rejected")
	result = tools.Execute(context.Background(), e, "notification_send", input)
	if result.OK || result.Error.Code != "PROVIDER_ERROR" {
		t.Fatal("notification false success", result)
	}
}
func TestToolOutputContractAndDeadline(t *testing.T) {
	tools := NewToolRegistry()
	spec := ToolSpec{Name: "bad", Description: "test", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"integer"}`)}
	if err := tools.Register(spec, func(context.Context, ExecutionContext, json.RawMessage) (any, error) { return "wrong type", nil }); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"bad"}, nil)
	result := tools.Execute(context.Background(), e, "bad", json.RawMessage(`{}`))
	if result.Error.Code != "INTERNAL_ERROR" {
		t.Fatal("invalid output accepted")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result = tools.Execute(ctx, e, "bad", json.RawMessage(`{}`))
	if result.Error.Code != "TIMEOUT" {
		t.Fatal("cancelled tool executed")
	}
}

func TestSchemaRegistrationNeverFetchesExternalReferences(t *testing.T) {
	r := NewToolRegistry()
	for _, schema := range []string{`{"$ref":"http://127.0.0.1/schema"}`, `{"\u0024ref":"http://127.0.0.1/schema"}`, `{"$schema":"http://127.0.0.1/schema"}`} {
		err := r.Register(ToolSpec{Name: "external", Description: "test", InputSchema: json.RawMessage(schema), OutputSchema: json.RawMessage(`{}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) { return nil, nil })
		requireCode(t, err, "INVALID_ARGUMENT")
	}
}

func TestInvalidToolsConsumeSharedBudget(t *testing.T) {
	r := NewToolRegistry()
	e := toolContext(nil, nil)
	e.budget.tools = 1
	first := r.Execute(context.Background(), e, "unknown", json.RawMessage(`{}`))
	if first.Error.Code != "TOOL_NOT_FOUND" {
		t.Fatal(first)
	}
	second := r.Execute(context.Background(), e, "unknown", json.RawMessage(`{}`))
	if second.Error.Code != "EXECUTION_LIMIT_EXCEEDED" {
		t.Fatal(second)
	}
}
