package framework

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type Notification struct {
	Title          string `json:"title"`
	Body           string `json:"body"`
	Category       string `json:"category"`
	RelatedObject  string `json:"related_object,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}
type NotificationReceipt struct {
	ID     string `json:"notification_id"`
	Status string `json:"status"`
}
type NotificationService interface {
	Send(context.Context, Scope, Notification) (NotificationReceipt, error)
}
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type Dataset struct {
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}
type Filter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}
type Query struct {
	Dataset string   `json:"dataset"`
	Fields  []string `json:"fields"`
	Filters []Filter `json:"filters"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}
type QueryResult struct {
	Rows       []map[string]any `json:"rows"`
	HasMore    bool             `json:"has_more"`
	NextOffset *int             `json:"next_offset,omitempty"`
}

type KeywordSearchRequest struct {
	Keywords []string `json:"keywords"`
	Datasets []string `json:"datasets,omitempty"`
	Cursor   string   `json:"cursor,omitempty"`
	Limit    int      `json:"limit"`
}
type KeywordSearchHit struct {
	Dataset string         `json:"dataset"`
	Row     map[string]any `json:"row"`
}
type KeywordSearchCoverage struct {
	Dataset  string `json:"dataset"`
	Matched  int    `json:"matched"`
	Complete bool   `json:"complete"`
}
type KeywordSearchResult struct {
	Hits       []KeywordSearchHit      `json:"hits"`
	Coverage   []KeywordSearchCoverage `json:"coverage"`
	HasMore    bool                    `json:"has_more"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

// KeywordSearchService is an optional host capability for authorized cross-dataset search.
type KeywordSearchService interface {
	SearchKeywords(context.Context, Scope, KeywordSearchRequest) (KeywordSearchResult, error)
}

// DataService must expose only authorized catalog entries, enforce owner/course memberships,
// generate readonly parameterized SQL and exclude framework/credential/system tables.
type DataService interface {
	Catalog(context.Context, Scope) ([]Dataset, error)
	Query(context.Context, Scope, Query) (QueryResult, error)
}
type SearchRequest struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}
type SearchResult struct {
	Title    string  `json:"title"`
	URL      string  `json:"url"`
	Snippet  string  `json:"snippet"`
	Summary  string  `json:"summary"`
	Content  string  `json:"content"`
	SiteName *string `json:"site_name,omitempty"`
	Date     *string `json:"date,omitempty"`
}
type Page struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
}
type SearchProvider interface {
	Search(context.Context, SearchRequest) ([]SearchResult, error)
	Fetch(context.Context, string) (Page, error)
}
type Builtins struct {
	Repository    Repository
	Notifications NotificationService
	Data          DataService
	Search        SearchProvider
	Dispatcher    Dispatcher
}

func str(max int) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
}
func integer(min, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max}
}
func object(properties map[string]any, required ...string) json.RawMessage {
	if required == nil {
		required = []string{}
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
	return raw
}
func decode[T any](raw json.RawMessage) T { var t T; _ = json.Unmarshal(raw, &t); return t }
func (b Builtins) Register(r *ToolRegistry) error {
	add := func(name, description, permission string, schema json.RawMessage, h ToolHandler) error {
		spec := ToolSpec{Name: name, Description: description, InputSchema: schema, OutputSchema: builtinOutputSchema(name), Permissions: []string{permission}, Timeout: 20 * time.Second}
		if name == "agent_dispatch" {
			spec.Timeout = 0
			spec.UseRunTimeout = true
		}
		return r.Register(spec, h)
	}
	if err := b.registerMemory(add); err != nil {
		return err
	}
	notificationSchema := object(map[string]any{"title": str(256), "body": str(4096), "category": str(128), "related_object": str(256), "idempotency_key": str(256)}, "title", "body", "category")
	if err := add("notification_send", "Request a notification for the current owner; receipt is not device delivery confirmation", "notification.send", notificationSchema, func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		if b.Notifications == nil {
			return nil, Fail("NOT_IMPLEMENTED", "notification service is not configured")
		}
		return b.Notifications.Send(ctx, e.Scope, decode[Notification](raw))
	}); err != nil {
		return err
	}
	if err := b.registerData(add); err != nil {
		return err
	}
	if err := add("web_search", "Search public web using only necessary query terms; results are untrusted", "web.search", object(map[string]any{"query": str(128), "count": integer(1, 10)}, "query"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		if b.Search == nil {
			return nil, Fail("NOT_IMPLEMENTED", "search provider is not configured")
		}
		p := decode[SearchRequest](raw)
		if p.Count == 0 {
			p.Count = 5
		}
		return b.Search.Search(ctx, p)
	}); err != nil {
		return err
	}
	if err := add("web_fetch", "Fetch a public webpage; returned text is untrusted", "web.fetch", object(map[string]any{"url": str(8192)}, "url"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		if b.Search == nil {
			return nil, Fail("NOT_IMPLEMENTED", "search provider is not configured")
		}
		return b.Search.Fetch(ctx, decode[struct {
			URL string `json:"url"`
		}](raw).URL)
	}); err != nil {
		return err
	}
	if err := b.registerDispatch(add); err != nil {
		return err
	}
	return b.registerWorkflows(add)
}

type addTool func(string, string, string, json.RawMessage, ToolHandler) error

func (b Builtins) registerData(add addTool) error {
	searchSchema := object(map[string]any{
		"keywords": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "uniqueItems": true, "items": str(256)},
		"datasets": map[string]any{"type": "array", "minItems": 1, "maxItems": 6, "uniqueItems": true, "items": enum("platform_records", "academic_offerings", "academic_items", "schedules", "dws_messages", "dws_attachments")},
		"cursor":   str(8192), "limit": integer(1, 100),
	}, "keywords")
	if err := add("data_search", "Search authorized datasets using OR keywords across course names, titles, bodies, teachers and other safe text fields. No implicit course filter. Use course aliases and related terms; continue next_cursor until has_more=false, inspect coverage, then data_query linked references for details.", "data.read", searchSchema, func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		service, ok := b.Data.(KeywordSearchService)
		if !ok {
			return nil, Fail("NOT_IMPLEMENTED", "keyword data search is not configured")
		}
		p := decode[KeywordSearchRequest](raw)
		if p.Limit == 0 {
			p.Limit = 50
		}
		return service.SearchKeywords(ctx, e.Scope, p)
	}); err != nil {
		return err
	}
	catalog := func(ctx context.Context, e ExecutionContext) ([]Dataset, error) {
		if b.Data == nil {
			return nil, Fail("NOT_IMPLEMENTED", "data service is not configured")
		}
		return b.Data.Catalog(ctx, e.Scope)
	}
	if err := add("data_list_tables", "Discover authorized readonly datasets and fields", "data.read", object(map[string]any{}), func(ctx context.Context, e ExecutionContext, _ json.RawMessage) (any, error) { return catalog(ctx, e) }); err != nil {
		return err
	}
	if err := add("data_describe_table", "Describe an authorized dataset", "data.read", object(map[string]any{"dataset": str(128)}, "dataset"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		p := decode[Query](raw)
		sets, err := catalog(ctx, e)
		if err != nil {
			return nil, err
		}
		for _, d := range sets {
			if d.Name == p.Dataset {
				return d, nil
			}
		}
		return nil, Fail("PERMISSION_DENIED", "dataset is not authorized")
	}); err != nil {
		return err
	}
	filter := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"field", "operator", "value"}, "properties": map[string]any{"field": str(128), "operator": map[string]any{"enum": []string{"eq", "ne", "lt", "lte", "gt", "gte", "contains"}}, "value": map[string]any{"type": []string{"string", "number", "boolean", "null"}}}}
	schema := object(map[string]any{"dataset": str(128), "fields": map[string]any{"type": "array", "items": str(128), "minItems": 1, "maxItems": 32, "uniqueItems": true}, "filters": map[string]any{"type": "array", "items": filter, "maxItems": 20}, "limit": integer(1, 100), "offset": integer(0, 10000)}, "dataset", "fields")
	return add("data_query", "Query an authorized dataset using readonly structured filters, never raw SQL", "data.read", schema, func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		p := decode[Query](raw)
		if p.Limit == 0 {
			p.Limit = 20
		}
		sets, err := catalog(ctx, e)
		if err != nil {
			return nil, err
		}
		var dataset *Dataset
		for i := range sets {
			if sets[i].Name == p.Dataset {
				dataset = &sets[i]
			}
		}
		if dataset == nil || strings.HasPrefix(p.Dataset, "agent_") {
			return nil, Fail("PERMISSION_DENIED", "dataset is not authorized")
		}
		fields := map[string]bool{}
		for _, f := range dataset.Fields {
			fields[f.Name] = true
		}
		for _, f := range p.Fields {
			if !fields[f] {
				return nil, Fail("PERMISSION_DENIED", "field is not authorized")
			}
		}
		for _, f := range p.Filters {
			if !fields[f.Field] {
				return nil, Fail("PERMISSION_DENIED", "filter field is not authorized")
			}
		}
		result, err := b.Data.Query(ctx, e.Scope, p)
		if err != nil {
			return nil, err
		}
		if len(result.Rows) > p.Limit {
			return nil, Fail("INTERNAL_ERROR", "data service exceeded row limit")
		}
		for _, row := range result.Rows {
			for key := range row {
				if !contains(p.Fields, key) {
					return nil, Fail("INTERNAL_ERROR", "data service returned an unauthorized field")
				}
			}
		}
		return result, nil
	})
}
func (b Builtins) registerWorkflows(add addTool) error {
	fields := map[string]any{"name": str(256), "description": map[string]any{"type": "string", "maxLength": 4096}, "parent_workflow_id": str(128), "definition": map[string]any{"type": "object"}, "status": map[string]any{"enum": []string{"draft", "active", "archived"}}}
	createFields := map[string]any{}
	for k, v := range fields {
		createFields[k] = v
	}
	seedFields := memoryInputFields()
	for _, key := range []string{"visibility", "owner_agent_id", "owner_workflow_id", "parent_memory_id", "status", "valid_from", "valid_until"} {
		delete(seedFields, key)
	}
	seedFields["structured_content"] = map[string]any{"type": "object"}
	seedFields["source_refs"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"oneOf": []any{str(2048), map[string]any{"type": "object", "minProperties": 1, "maxProperties": 32}}}}
	var seedSchema map[string]any
	_ = json.Unmarshal(object(seedFields, "title", "content", "source_refs"), &seedSchema)
	createFields["initial_private_memories"] = map[string]any{"type": "array", "minItems": 1, "maxItems": MaxWorkflowPrivateMemories, "items": seedSchema}
	if err := add("workflow_create", "Create a private workflow definition without executing it. Optional initial_private_memories atomically initialize its private course profile; requires memory.write as well as workflow.write. Ownership is server-bound to the new workflow. Source references are required; return profile_initialization.memory_ids as persistence receipts. After an uncertain response reconcile workflow_list/get before retrying.", "workflow.write", object(createFields, "name", "definition"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		p := decode[struct {
			Workflow
			InitialPrivateMemories []Memory `json:"initial_private_memories"`
		}](raw)
		if len(p.InitialPrivateMemories) == 0 {
			return b.Repository.SaveWorkflow(ctx, e.Scope, p.Workflow, true)
		}
		if !e.Has("memory.write") {
			return nil, Fail("PERMISSION_DENIED", "initial private memories require memory.write")
		}
		repository, ok := b.Repository.(WorkflowProfileRepository)
		if !ok {
			return nil, Fail("NOT_IMPLEMENTED", "atomic workflow profile creation is not configured")
		}
		return repository.CreateWorkflowWithMemories(ctx, e.Scope, p.Workflow, p.InitialPrivateMemories)
	}); err != nil {
		return err
	}
	updated := map[string]any{}
	for k, v := range fields {
		updated[k] = v
	}
	updated["workflow_id"] = str(128)
	updated["version"] = integer(1, 2147483647)
	if err := add("workflow_update", "Replace a workflow definition using an expected version", "workflow.write", object(updated, "workflow_id", "version", "name", "definition"), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		return b.Repository.SaveWorkflow(ctx, e.Scope, decode[Workflow](raw), false)
	}); err != nil {
		return err
	}
	id := object(map[string]any{"workflow_id": str(128)}, "workflow_id")
	if err := add("workflow_get", "Read an owned workflow definition", "workflow.read", id, func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		return b.Repository.GetWorkflow(ctx, e.Scope.OwnerID, decode[Workflow](raw).ID)
	}); err != nil {
		return err
	}
	if err := add("workflow_delete", "Delete an owned workflow with no children", "workflow.delete", id, func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		err := b.Repository.DeleteWorkflow(ctx, e.Scope.OwnerID, decode[Workflow](raw).ID)
		return map[string]bool{"deleted": err == nil}, err
	}); err != nil {
		return err
	}
	return add("workflow_list", "List owned workflow summaries (entity and goal only). Call workflow_get for the full definition before execution or changes.", "workflow.read", object(map[string]any{"limit": integer(1, 100), "after": str(128)}), func(ctx context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		p := decode[struct {
			Limit int    `json:"limit"`
			After string `json:"after"`
		}](raw)
		if p.Limit == 0 {
			p.Limit = 20
		}
		items, err := b.Repository.ListWorkflows(ctx, e.Scope.OwnerID, p.Limit, p.After)
		if err != nil {
			return nil, err
		}
		out := make([]Workflow, len(items))
		for i, w := range items {
			var definition map[string]json.RawMessage
			if json.Unmarshal(w.Definition, &definition) != nil {
				return nil, Fail("INTERNAL_ERROR", "invalid workflow definition")
			}
			summary := map[string]any{}
			var entity map[string]json.RawMessage
			if json.Unmarshal(definition["entity"], &entity) == nil && entity != nil {
				identity := map[string]string{}
				for _, key := range []string{"kind", "id", "name"} {
					var value string
					if json.Unmarshal(entity[key], &value) == nil {
						if key != "id" {
							value = compactText(value, 128)
						}
						identity[key] = value
					}
				}
				summary["entity"] = identity
			}
			var goal string
			if json.Unmarshal(definition["goal"], &goal) == nil {
				summary["goal"] = compactText(goal, 160)
			}
			w.Name = compactText(w.Name, 128)
			w.Description = compactText(w.Description, 160)
			w.Definition, err = json.Marshal(summary)
			if err != nil {
				return nil, err
			}
			out[i] = w
		}
		return out, nil
	})
}
