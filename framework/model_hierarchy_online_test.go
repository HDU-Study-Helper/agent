package framework

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in only. Uses synthetic rows and an in-memory repository; the API
// key is read from stdin, never included in persisted requests or test output.
func TestOnlineLearnerHierarchyAndFinalFormat(t *testing.T) {
	if os.Getenv("LEARNER_ONLINE_PROBE") != "1" {
		t.Skip("explicit bounded online probe only")
	}
	key, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		t.Fatal("test API key required on stdin")
	}
	key = strings.TrimSpace(key)
	model := os.Getenv("LEARNER_PROBE_MODEL")
	if model == "" {
		model = "deepseek-flash"
	}
	if model != "deepseek-flash" && model != "deepseek-v4-pro" {
		t.Fatal("unsupported probe model")
	}
	provider, err := NewOpenAIProvider(HTTPConfig{BaseURL: "https://api.deepseek.com/v1", APIKey: key, Timeout: 45 * time.Second, Retries: 0, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile("../learner-agent/system-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("../../.build", "learner-runtime-minimal-")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	save := func(name string, v any) {
		t.Helper()
		raw, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	repo := newFakeRepo()
	a := fixtureAgent("learner-probe")
	a.Type = "learner"
	a.SystemPrompt = string(prompt)
	a.Model = ModelConfig{Provider: "probe", Model: model, MaxTokens: 1200}
	a.EnabledTools = []string{"data_query", "data_describe_table", "data_list_tables"}
	a.Permissions = []string{"data.read"}
	a.Limits = Limits{ModelCalls: 6, ToolCalls: 8, RunTimeout: 100 * time.Second, ContextBytes: 128 << 10, ToolResultBytes: 16 << 10}.Defaults()
	repo.agents[a.ID] = a
	probeCase := os.Getenv("LEARNER_PROBE_CASE")
	if probeCase != "" && probeCase != "distractor" {
		t.Fatal("unsupported probe case")
	}
	data := &hierarchyProbeData{distractor: probeCase == "distractor"}
	registry := NewToolRegistry()
	if err = (Builtins{Repository: repo, Data: data}).Register(registry); err != nil {
		t.Fatal(err)
	}
	calls := 0
	corrected := false
	recorded := modelFunc(func(ctx context.Context, q ModelRequest) (ModelResponse, error) {
		calls++
		save(fmt.Sprintf("%02d-request.json", calls), q)
		if len(q.Messages) > 0 && strings.Contains(q.Messages[len(q.Messages)-1].Content, "FORMAT_ONLY") {
			corrected = true
			if len(q.Tools) != 0 {
				t.Fatal("format correction still offers business tools")
			}
		}
		response, e := provider.Complete(ctx, q)
		save(fmt.Sprintf("%02d-response.json", calls), map[string]any{"response": response, "error": SafeError(e)})
		return response, e
	})
	runtime := NewRuntime(repo, registry, map[string]ModelProvider{"probe": recorded})
	catalog, _ := data.Catalog(context.Background(), Scope{OwnerID: "owner"})
	input, _ := json.Marshal(map[string]any{"task": "只读核对 connection_id=conn-fixture、course_ref=ref-fixture 这门课的正式作业状态，不创建Workflow或记忆。查询和是否继续由你决定，给出有据结论及限制。", "catalog": catalog, "context": map[string]any{"entity": map[string]any{"kind": "course", "id": "source=chaoxing|connection=conn-fixture|course_ref=ref-fixture", "name": "测试课程"}}})
	result, runErr := runtime.Run(context.Background(), "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: input})
	record := repo.runs[result.RunID]
	summary := map[string]any{"model": model, "model_requests": calls, "query_count": len(data.queries), "queries": data.queries, "format_correction_used": corrected, "run_error": SafeError(runErr), "business_writes": 0, "usage": record.Usage, "output": result.Output, "probe_case": probeCase}
	save("summary.json", summary)
	t.Log("private evidence:", dir)
	if runErr != nil {
		t.Fatal(runErr)
	}
	var output map[string]json.RawMessage
	if json.Unmarshal([]byte(result.Output), &output) != nil {
		t.Fatal("final response is not plain JSON")
	}
	// Business output requirements belong to this opt-in prompt evaluation,
	// not to Runtime. The framework returns the model's final text unchanged.
	if len(data.queries) < 2 || data.queries[0].Dataset != "academic_offerings" || data.queries[1].Dataset != "academic_items" {
		t.Fatal("two-level query path not observed")
	}
	if !strings.Contains(result.Output, "submitted") && !strings.Contains(result.Output, "已提交") {
		t.Fatal("answer omitted the actual assignment status")
	}
	if data.distractor {
		var entity struct{ Kind, ID string }
		if json.Unmarshal(output["entity"], &entity) != nil || entity.ID != "source=chaoxing|connection=conn-fixture|course_ref=ref-fixture" {
			t.Fatal("answer switched to the unrelated course")
		}
	}
	if len(repo.memories) != 0 {
		t.Fatal("unexpected memory write")
	}
}

type hierarchyProbeData struct {
	queries    []Query
	distractor bool
}

func (*hierarchyProbeData) Catalog(context.Context, Scope) ([]Dataset, error) {
	sets := []Dataset{}
	for _, entry := range []struct {
		name   string
		fields []string
	}{{"academic_offerings", []string{"id", "connection_id", "source", "course_ref", "course_name"}}, {"academic_items", []string{"id", "connection_id", "offering_id", "title", "kind", "status"}}} {
		fs := []Field{}
		for _, name := range entry.fields {
			fs = append(fs, Field{Name: name, Type: "string"})
		}
		sets = append(sets, Dataset{Name: entry.name, Fields: fs})
	}
	return sets, nil
}
func (d *hierarchyProbeData) Query(_ context.Context, scope Scope, q Query) (QueryResult, error) {
	if scope.OwnerID != "owner" {
		return QueryResult{}, Fail("PERMISSION_DENIED", "wrong owner")
	}
	d.queries = append(d.queries, q)
	records := map[string][]map[string]any{
		"academic_offerings": {{"id": "offering-fixture", "connection_id": "conn-fixture", "source": "chaoxing", "course_ref": "ref-fixture", "course_name": "测试课程"}},
		"academic_items":     {{"id": "assignment-fixture", "connection_id": "conn-fixture", "offering_id": "offering-fixture", "title": "练习一", "kind": "assignment", "status": "submitted"}},
	}
	if d.distractor {
		records["academic_offerings"] = append(records["academic_offerings"], map[string]any{"id": "offering-other", "connection_id": "conn-fixture", "source": "chaoxing", "course_ref": "ref-other", "course_name": "无关课程"})
		records["academic_items"] = append(records["academic_items"], map[string]any{"id": "assignment-other", "connection_id": "conn-fixture", "offering_id": "offering-other", "title": "无关课程作业", "kind": "assignment", "status": "open"})
	}
	matched := []map[string]any{}
	for _, row := range records[q.Dataset] {
		match := true
		for _, filter := range q.Filters {
			value, ok := filter.Value.(string)
			if !ok {
				return QueryResult{}, Fail("INVALID_ARGUMENT", "string filter required")
			}
			actual, _ := row[filter.Field].(string)
			switch filter.Operator {
			case "eq":
				match = match && actual == value
			case "ne":
				match = match && actual != value
			case "contains":
				match = match && strings.Contains(actual, value)
			case "lt":
				match = match && actual < value
			case "lte":
				match = match && actual <= value
			case "gt":
				match = match && actual > value
			case "gte":
				match = match && actual >= value
			default:
				return QueryResult{}, Fail("INVALID_ARGUMENT", "unsupported operator")
			}
		}
		if match {
			matched = append(matched, row)
		}
	}
	result := QueryResult{Rows: []map[string]any{}}
	for i := q.Offset; i < len(matched) && i < q.Offset+q.Limit; i++ {
		projected := map[string]any{}
		for _, field := range q.Fields {
			projected[field] = matched[i][field]
		}
		result.Rows = append(result.Rows, projected)
	}
	result.HasMore = q.Offset+len(result.Rows) < len(matched)
	if result.HasMore {
		next := q.Offset + len(result.Rows)
		result.NextOffset = &next
	}
	return result, nil
}
