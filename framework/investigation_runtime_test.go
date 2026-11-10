package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Exercise the real builtin schemas and optional host capabilities without any
// database or model requests. Profile writes are counted by profileRepository.
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

func investigationCoverage() []KeywordSearchCoverage {
	return []KeywordSearchCoverage{
		{Dataset: "platform_records", Complete: true},
		{Dataset: "academic_offerings", Complete: true},
		{Dataset: "academic_items", Complete: true},
		{Dataset: "schedules", Complete: true},
		{Dataset: "dws_messages", Complete: true},
		{Dataset: "dws_attachments", Complete: true},
	}
}

func investigationCourseArgs(t *testing.T, manifest, seeds bool) string {
	t.Helper()
	definition := map[string]any{"entity": map[string]any{"kind": "course", "id": "course-1"}}
	if manifest {
		definition["profile_manifest"] = map[string]any{
			"datasets": []string{"platform_records", "academic_offerings", "academic_items", "schedules", "dws_messages", "dws_attachments"},
			"topics":   []string{"profile_index", "course_overview"},
		}
	}
	args := map[string]any{"name": "Calculus", "definition": definition}
	if seeds {
		args["initial_private_memories"] = []any{
			map[string]any{"title": "profile_index", "content": "Course profile topics: course_overview", "source_refs": []any{map[string]any{"dataset": "academic_offerings", "id": "course-1"}}},
			map[string]any{"title": "course_overview", "content": "Calculus course evidence", "source_refs": []any{map[string]any{"dataset": "academic_offerings", "id": "course-1"}}},
		}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
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

func TestInvestigationRuntimePendingBatchRejectsWriteAndFinalPageAllowsCreate(t *testing.T) {
	for _, kind := range []string{"keyword cursor", "query offset"} {
		t.Run(kind, func(t *testing.T) {
			args := investigationCourseArgs(t, true, true)
			data := &investigationRuntimeData{}
			searchCalls, queryCalls := 0, 0
			data.search = func(q KeywordSearchRequest) KeywordSearchResult {
				searchCalls++
				page := KeywordSearchResult{Hits: []KeywordSearchHit{}, Coverage: investigationCoverage()}
				if kind == "keyword cursor" && searchCalls == 1 {
					page.HasMore, page.NextCursor = true, "remaining-evidence"
				} else if kind == "keyword cursor" && q.Cursor != "remaining-evidence" {
					t.Fatalf("continuation lost cursor: %+v", q)
				}
				return page
			}
			data.query = func(q Query) QueryResult {
				queryCalls++
				if queryCalls == 1 {
					if q.Offset != 0 {
						t.Fatalf("unexpected initial offset: %+v", q)
					}
					next := 7
					return QueryResult{Rows: []map[string]any{{"title": "first evidence"}}, HasMore: true, NextOffset: &next}
				}
				if q.Offset != 7 {
					t.Fatalf("continuation lost next_offset: %+v", q)
				}
				return QueryResult{Rows: []map[string]any{{"title": "last evidence"}}}
			}
			var repo *profileRepository
			round := 0
			model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
				round++
				switch round {
				case 1:
					batch := []ToolCall{call("search", "data_search", `{"keywords":["Calculus"]}`)}
					if kind == "query offset" {
						batch = append(batch, call("query", "data_query", `{"dataset":"records","fields":["title"],"limit":7}`))
					}
					return calls(append(batch, call("too-early", "workflow_create", args))...), nil
				case 2:
					rejected := investigationToolResult(t, req, "too-early")
					if rejected.OK || rejected.Error == nil || rejected.Error.Code != "INVALID_ARGUMENT" || repo.ordinary != 0 || repo.bootstrap != 0 {
						t.Fatalf("pending investigation reached storage: result=%+v ordinary=%d bootstrap=%d", rejected, repo.ordinary, repo.bootstrap)
					}
					last := call("last-page", "data_search", `{"keywords":["Calculus"],"cursor":"remaining-evidence"}`)
					if kind == "query offset" {
						first := investigationToolResult(t, req, "query")
						var page QueryResult
						raw, _ := json.Marshal(first.Data)
						if json.Unmarshal(raw, &page) != nil || !page.HasMore || page.NextOffset == nil || *page.NextOffset != 7 {
							t.Fatalf("model did not receive query continuation: %+v", first)
						}
						last = call("last-page", "data_query", `{"dataset":"records","fields":["title"],"limit":7,"offset":7}`)
					}
					return calls(last, call("created", "workflow_create", args)), nil
				case 3:
					created := investigationToolResult(t, req, "created")
					if !created.OK || created.Error != nil || repo.bootstrap != 1 || repo.ordinary != 1 || len(repo.seeds) != 2 {
						t.Fatalf("last page in same response did not unblock atomic creation: %+v repo=%+v", created, repo)
					}
					if repo.scope.OwnerID != "owner" || repo.scope.AgentID != "learner" || repo.scope.WorkflowID != "" {
						t.Fatalf("unexpected bootstrap scope: %+v", repo.scope)
					}
					return final("profile created"), nil
				default:
					t.Fatalf("unexpected model round %d", round)
					return ModelResponse{}, nil
				}
			})
			runtime, profile, base := newInvestigationTestRuntime(t, data, "learner", true, model)
			repo = profile
			result := runInvestigationTest(t, runtime)
			if result.Output != "profile created" || round != 3 {
				t.Fatalf("creation repair did not clear final-answer guard: %+v rounds=%d", result, round)
			}
			record := base.runs[result.RunID]
			if record.Status != "succeeded" || len(record.ToolExecutions) < 4 || record.ToolExecutions[len(record.ToolExecutions)-1].Error != nil {
				t.Fatalf("unexpected persisted run: %+v", record)
			}
		})
	}
}

func TestInvestigationRuntimeCoursePrerequisitesUseActualCoverage(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		agentType               string
		search, manifest, seeds bool
		coverage                []KeywordSearchCoverage
		reject                  bool
	}{
		{"five datasets despite six claimed in manifest", "learner", true, true, true, investigationCoverage()[:5], true},
		{"sixth dataset not complete", "learner", true, true, true, append(investigationCoverage()[:5], KeywordSearchCoverage{Dataset: "dws_attachments", Complete: false}), true},
		{"no discovery despite manifest", "learner", true, true, true, nil, true},
		{"missing manifest", "learner", true, false, true, investigationCoverage(), true},
		{"missing private seeds", "learner", true, true, false, investigationCoverage(), true},
		{"complete course bootstrap", "learner", true, true, true, investigationCoverage(), false},
		{"worker unaffected", "worker", true, false, false, nil, false},
		{"learner without data_search unaffected", "learner", false, false, false, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := &investigationRuntimeData{search: func(q KeywordSearchRequest) KeywordSearchResult {
				coverage := tc.coverage
				if q.Keywords[0] == "repair" {
					coverage = investigationCoverage()
				}
				return KeywordSearchResult{Hits: []KeywordSearchHit{}, Coverage: coverage}
			}}
			var repo *profileRepository
			round := 0
			model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
				round++
				if round == 1 {
					batch := []ToolCall{}
					if tc.search && tc.coverage != nil {
						batch = append(batch, call("discovery", "data_search", `{"keywords":["Calculus"]}`))
					}
					return calls(append(batch, call("initial-create", "workflow_create", investigationCourseArgs(t, tc.manifest, tc.seeds)))...), nil
				}
				if round == 2 {
					got := investigationToolResult(t, req, "initial-create")
					if tc.reject {
						if got.OK || got.Error == nil || got.Error.Code != "INVALID_ARGUMENT" || repo.ordinary != 0 || repo.bootstrap != 0 {
							t.Fatalf("incomplete course wrote before repair: %+v repo=%+v", got, repo)
						}
						return calls(call("repair-discovery", "data_search", `{"keywords":["repair"]}`), call("repaired-create", "workflow_create", investigationCourseArgs(t, true, true))), nil
					}
					if !got.OK || repo.ordinary != 1 {
						t.Fatalf("valid or unrelated creation was blocked: %+v repo=%+v", got, repo)
					}
					return final("done"), nil
				}
				if round == 3 && tc.reject {
					if got := investigationToolResult(t, req, "repaired-create"); !got.OK || repo.bootstrap != 1 {
						t.Fatalf("valid repair did not write: %+v repo=%+v", got, repo)
					}
					return final("done"), nil
				}
				t.Fatalf("unexpected model round %d", round)
				return ModelResponse{}, nil
			})
			runtime, profile, _ := newInvestigationTestRuntime(t, data, tc.agentType, tc.search, model)
			repo = profile
			if result := runInvestigationTest(t, runtime); result.Output != "done" {
				t.Fatal(result)
			}
		})
	}
}

func TestInvestigationRuntimeRejectsRepeatedPrematureFinal(t *testing.T) {
	for _, kind := range []string{"search", "query"} {
		t.Run(kind, func(t *testing.T) {
			next := 19
			data := &investigationRuntimeData{
				search: func(KeywordSearchRequest) KeywordSearchResult {
					return KeywordSearchResult{Hits: []KeywordSearchHit{}, Coverage: investigationCoverage(), HasMore: true, NextCursor: "must-read"}
				},
				query: func(Query) QueryResult {
					return QueryResult{Rows: []map[string]any{}, HasMore: true, NextOffset: &next}
				},
			}
			round := 0
			model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
				round++
				if round == 1 {
					if kind == "search" {
						return calls(call("page", "data_search", `{"keywords":["Calculus"]}`)), nil
					}
					return calls(call("page", "data_query", `{"dataset":"records","fields":["title"]}`)), nil
				}
				if round > 2 {
					message := req.Messages[len(req.Messages)-1]
					continuation := "must-read"
					if kind == "query" {
						continuation = `"offset":19`
					}
					if message.Role != "user" || !strings.Contains(message.Content, "Host execution validation:") || !strings.Contains(message.Content, continuation) {
						t.Fatalf("host did not return actionable continuation: %+v", message)
					}
				}
				return final("Please ask me to continue reading later"), nil
			})
			runtime, repo, base := newInvestigationTestRuntime(t, data, "learner", true, model)
			result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: "learner", Trigger: UserMessage, Input: json.RawMessage(`{}`)})
			requireCode(t, err, "INCOMPLETE_INVESTIGATION")
			if result.Output != "" || round != 5 || repo.ordinary != 0 || repo.bootstrap != 0 {
				t.Fatalf("premature answer escaped host guard: %+v rounds=%d repo=%+v", result, round, repo)
			}
			if record := base.runs[result.RunID]; record.Status != "failed" || record.Error == nil || record.Error.Code != "INCOMPLETE_INVESTIGATION" {
				t.Fatalf("incomplete investigation was not recorded as failure: %+v", record)
			}
		})
	}
}

func TestInvestigationRuntimeCanonicalKeywordRetryClearsPending(t *testing.T) {
	searchCalls, round := 0, 0
	data := &investigationRuntimeData{search: func(q KeywordSearchRequest) KeywordSearchResult {
		searchCalls++
		return KeywordSearchResult{Hits: []KeywordSearchHit{}, Coverage: []KeywordSearchCoverage{}, HasMore: searchCalls == 1, NextCursor: "first-cursor"}
	}}
	model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		round++
		switch round {
		case 1:
			return calls(call("aliases", "data_search", `{"keywords":[" Calculus ","calculus","Analysis"],"limit":1}`)), nil
		case 2:
			if got := investigationToolResult(t, req, "aliases"); !got.OK {
				t.Fatalf("initial alias discovery failed: %+v", got)
			}
			return final("premature"), nil
		case 3:
			if last := req.Messages[len(req.Messages)-1]; !strings.Contains(last.Content, "first-cursor") {
				t.Fatalf("pending canonical request was not guarded: %+v", last)
			}
			return calls(call("normalized-retry", "data_search", `{"keywords":["analysis","calculus"],"datasets":["schedules","dws_messages","academic_items","platform_records","dws_attachments","academic_offerings"],"limit":100}`)), nil
		case 4:
			if got := investigationToolResult(t, req, "normalized-retry"); !got.OK {
				t.Fatal(got)
			}
			return final("all available evidence read"), nil
		default:
			t.Fatalf("equivalent keywords left a stale pagination obligation: round %d", round)
			return ModelResponse{}, nil
		}
	})
	runtime, _, _ := newInvestigationTestRuntime(t, data, "learner", true, model)
	if got := runInvestigationTest(t, runtime); got.Output != "all available evidence read" || searchCalls != 2 {
		t.Fatalf("canonical retry did not complete investigation: %+v searches=%d", got, searchCalls)
	}
}

func TestInvestigationRuntimeDeliversBatchDetailsBeforeHistoryCompaction(t *testing.T) {
	searchCalls, round := 0, 0
	data := &investigationRuntimeData{search: func(KeywordSearchRequest) KeywordSearchResult {
		searchCalls++
		return KeywordSearchResult{Hits: []KeywordSearchHit{{Dataset: "academic_items", Row: map[string]any{"id": fmt.Sprintf("item-%d", searchCalls), "title": "Calculus", "text": fmt.Sprintf("unique-body-%d", searchCalls)}}}, Coverage: investigationCoverage()}
	}}
	model := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		round++
		switch round {
		case 1:
			return calls(call("one", "data_search", `{"keywords":["course"]}`), call("two", "data_search", `{"keywords":["teacher"]}`), call("three", "data_search", `{"keywords":["assignment"]}`)), nil
		case 2:
			for i, id := range []string{"one", "two", "three"} {
				got := investigationToolResult(t, req, id)
				raw, _ := json.Marshal(got.Data)
				if !strings.Contains(string(raw), fmt.Sprintf("unique-body-%d", i+1)) || strings.Contains(string(raw), "discovery_details_compacted") {
					t.Fatalf("batch result compacted before first model delivery: %s=%s", id, raw)
				}
			}
			return calls(call("four", "data_search", `{"keywords":["exam"]}`)), nil
		case 3:
			old := investigationToolResult(t, req, "one")
			raw, _ := json.Marshal(old.Data)
			if !strings.Contains(string(raw), "item-1") || !strings.Contains(string(raw), "unique-body-1") || !strings.Contains(string(raw), `"complete":true`) {
				t.Fatalf("previously delivered evidence lost references or was not compacted: %s", raw)
			}
			current := investigationToolResult(t, req, "four")
			raw, _ = json.Marshal(current.Data)
			if !strings.Contains(string(raw), "unique-body-4") || strings.Contains(string(raw), "discovery_details_compacted") {
				t.Fatalf("current result was compacted before delivery: %s", raw)
			}
			return final("evidence read"), nil
		default:
			t.Fatalf("unexpected model round %d", round)
			return ModelResponse{}, nil
		}
	})
	runtime, _, _ := newInvestigationTestRuntime(t, data, "learner", true, model)
	if got := runInvestigationTest(t, runtime); got.Output != "evidence read" || searchCalls != 4 {
		t.Fatal(got, searchCalls)
	}
}
