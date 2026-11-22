package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Exercise the real builtin schemas without database or model requests.
type investigationRuntimeData struct {
	mockData
	search func(KeywordSearchRequest) KeywordSearchResult
	query  func(Query) QueryResult
}

func (d *investigationRuntimeData) SearchKeywords(_ context.Context, _ Scope, q KeywordSearchRequest) (KeywordSearchResult, error) {
	return d.search(q), nil
}

func (d *investigationRuntimeData) Query(_ context.Context, _ Scope, q Query) (QueryResult, error) {
	return d.query(q), nil
}

func newInvestigationTestRuntime(t *testing.T, data DataService, agentType string, searchEnabled bool, model modelFunc) (*Runtime, *profileRepository, *fakeRepo) {
	t.Helper()
	base := newFakeRepo()
	a := fixtureAgent("learner")
	a.Type = agentType
	a.EnabledTools = []string{"workflow_create", "data_query"}
	if searchEnabled {
		a.EnabledTools = append(a.EnabledTools, "data_search")
	}
	a.Permissions = []string{"data.read", "workflow.write", "memory.write"}
	base.agents[a.ID] = a
	repo := &profileRepository{Repository: base}
	tools := NewToolRegistry()
	if err := (Builtins{Repository: repo, Data: data}).Register(tools); err != nil {
		t.Fatal(err)
	}
	return NewRuntime(repo, tools, map[string]ModelProvider{"mock": model}), repo, base
}

func investigationToolResult(t *testing.T, req ModelRequest, id string) ToolResult {
	t.Helper()
	for _, message := range req.Messages {
		if message.Role == "tool" && message.ToolCallID == id {
			var result ToolResult
			if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
				t.Fatal(err)
			}
			return result
		}
	}
	t.Fatalf("missing tool result %q", id)
	return ToolResult{}
}

func runInvestigationTest(t *testing.T, runtime *Runtime) RunResult {
	t.Helper()
	result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: "learner", Trigger: UserMessage, Input: json.RawMessage(`{"task":"investigate and initialize Calculus"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRuntimeLeavesInvestigationAndWorkflowCreationToAgent(t *testing.T) {
	for _, role := range []string{"learner", "filter", "worker", "custom"} {
		for _, discovery := range []string{"none", "keyword page", "query page"} {
			t.Run(role+"/"+discovery, func(t *testing.T) {
				next := 7
				data := &investigationRuntimeData{
					search: func(KeywordSearchRequest) KeywordSearchResult {
						return KeywordSearchResult{Hits: []KeywordSearchHit{}, Coverage: []KeywordSearchCoverage{{Dataset: "platform_records", Complete: false}}, HasMore: true, NextCursor: "optional-next-page"}
					},
					query: func(Query) QueryResult {
						return QueryResult{Rows: []map[string]any{{"title": "sufficient evidence"}}, HasMore: true, NextOffset: &next}
					},
				}
				round := 0
				model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
					round++
					if round == 1 {
						batch := []ToolCall{}
						if discovery == "keyword page" {
							batch = append(batch, call("page", "data_search", `{"keywords":["Calculus"]}`))
						} else if discovery == "query page" {
							batch = append(batch, call("page", "data_query", `{"dataset":"records","fields":["title"]}`))
						}
						// No profile manifest, initial memories or six-dataset scan.
						return calls(append(batch, call("create", "workflow_create", `{"name":"Calculus","definition":{"entity":{"kind":"course","id":"course-1"}}}`))...), nil
					}
					if round != 2 {
						t.Fatal("runtime added a business correction round", round)
					}
					if result := investigationToolResult(t, req, "create"); !result.OK {
						t.Fatal("authorized creation was blocked by investigation policy", result)
					}
					if discovery != "none" {
						page := investigationToolResult(t, req, "page")
						if !page.OK || page.Data.(map[string]any)["has_more"] != true {
							t.Fatal("pagination information was lost", page)
						}
					}
					return final("Workflow saved; further retrieval is unnecessary for this task."), nil
				})
				runtime, repo, base := newInvestigationTestRuntime(t, data, role, true, model)
				result := runInvestigationTest(t, runtime)
				if repo.ordinary != 1 || repo.bootstrap != 0 || round != 2 || result.Output == "" || base.runs[result.RunID].Status != "succeeded" {
					t.Fatal("creation or final response changed", repo, round, result)
				}
			})
		}
	}
}

func TestRuntimeDoesNotForcePagingOrToolErrorRepair(t *testing.T) {
	for _, kind := range []string{"keyword page", "query page", "invalid creation"} {
		t.Run(kind, func(t *testing.T) {
			next := 19
			data := &investigationRuntimeData{
				search: func(KeywordSearchRequest) KeywordSearchResult {
					return KeywordSearchResult{Hits: []KeywordSearchHit{}, Coverage: []KeywordSearchCoverage{}, HasMore: true, NextCursor: "optional-page"}
				},
				query: func(Query) QueryResult {
					return QueryResult{Rows: []map[string]any{}, HasMore: true, NextOffset: &next}
				},
			}
			round := 0
			model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
				round++
				if round == 1 {
					switch kind {
					case "keyword page":
						return calls(call("result", "data_search", `{"keywords":["Calculus"]}`)), nil
					case "query page":
						return calls(call("result", "data_query", `{"dataset":"records","fields":["title"]}`)), nil
					default:
						return calls(call("result", "workflow_create", `{"name":"missing definition"}`)), nil
					}
				}
				if round != 2 {
					t.Fatal("unexpected forced continuation", round)
				}
				result := investigationToolResult(t, req, "result")
				if kind == "invalid creation" {
					if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
						t.Fatal("tool schema validation was weakened", result)
					}
				} else if !result.OK || result.Data.(map[string]any)["has_more"] != true {
					t.Fatal("missing continuation metadata", result)
				}
				return final("Partial result; stopping here by task policy."), nil
			})
			runtime, repo, base := newInvestigationTestRuntime(t, data, "learner", true, model)
			result := runInvestigationTest(t, runtime)
			if round != 2 || repo.ordinary != 0 || repo.bootstrap != 0 || base.runs[result.RunID].ModelCalls != 2 {
				t.Fatal(result, round, repo)
			}
		})
	}
}

func TestRuntimeKeepsDeliveredToolHistoryUnchanged(t *testing.T) {
	round := 0
	data := &investigationRuntimeData{query: func(q Query) QueryResult {
		row := map[string]any{"title": ""}
		if contains(q.Fields, "metadata") {
			row["metadata"] = `{"unknown":null,"state":{}}`
		}
		return QueryResult{Rows: []map[string]any{row}, HasMore: false}
	}}
	var delivered string
	model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		round++
		if round == 1 {
			return calls(call("first", "data_query", `{"dataset":"records","fields":["title","metadata"]}`)), nil
		}
		for _, message := range req.Messages {
			if message.ToolCallID == "first" {
				if delivered == "" {
					delivered = message.Content
				} else if message.Content != delivered {
					t.Fatal("previously delivered evidence was rewritten")
				}
			}
		}
		if round == 2 {
			return calls(call("second", "data_query", `{"dataset":"records","fields":["title"]}`)), nil
		}
		return final("done"), nil
	})
	// mockData's catalog contains only title, so use a dedicated catalog for metadata.
	wrapped := &cleaningRuntimeData{investigationRuntimeData: *data}
	runtime, _, _ := newInvestigationTestRuntime(t, wrapped, "learner", true, model)
	result := runInvestigationTest(t, runtime)
	if result.Output != "done" || delivered == "" || !strings.Contains(delivered, `\"unknown\":null`) {
		t.Fatal("original evidence missing", result, delivered)
	}
}
