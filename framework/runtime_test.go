package framework

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRepo struct {
	Repository
	mu       sync.Mutex
	agents   map[string]AgentInstance
	memories []Memory
	runs     map[string]RunRecord
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{agents: map[string]AgentInstance{}, runs: map[string]RunRecord{}}
}
func (s *fakeRepo) GetAgent(ctx context.Context, scope Scope) (AgentInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[scope.AgentID]
	if !ok || a.OwnerID != scope.OwnerID {
		return a, Fail("AGENT_NOT_FOUND", "not owned")
	}
	return a, ctx.Err()
}
func (s *fakeRepo) ListMemory(ctx context.Context, scope Scope, _ string, _ int) ([]Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Memory{}
	for _, m := range s.memories {
		if m.OwnerID == scope.OwnerID && m.AgentID == scope.AgentID {
			out = append(out, m)
		}
	}
	return out, ctx.Err()
}
func (s *fakeRepo) SaveRun(_ context.Context, r RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.ID] = r
	return nil
}

type modelFunc func(context.Context, ModelRequest) (ModelResponse, error)

func (fn modelFunc) Complete(ctx context.Context, r ModelRequest) (ModelResponse, error) {
	return fn(ctx, r)
}
func fixtureAgent(id string) AgentInstance {
	return AgentInstance{ID: id, OwnerID: "owner", Status: "enabled", AgentDefinition: AgentDefinition{Name: id, SystemPrompt: "test", Model: ModelConfig{Provider: "mock", Model: "model"}, Limits: Limits{}.Defaults()}}
}
func final(text string) ModelResponse {
	return ModelResponse{Message: Message{Role: "assistant", Content: text}, FinishReason: "stop"}
}
func calls(values ...ToolCall) ModelResponse {
	return ModelResponse{Message: Message{Role: "assistant", ToolCalls: values}, FinishReason: "tool_calls"}
}
func call(id, name, args string) ToolCall {
	return ToolCall{ID: id, Type: "function", Function: FunctionCall{Name: name, Arguments: args}}
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || SafeError(err).Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

type configResolver struct {
	model    string
	provider ModelProvider
}

func (r *configResolver) ResolveProvider(string) ModelProvider { return r.provider }
func (r *configResolver) ResolveModel(config ModelConfig) (ModelProvider, ModelConfig, error) {
	if config.Mode == "default" {
		config.Model = r.model
	}
	return r.provider, config, nil
}
func TestRuntimeResolvesDefaultModelForEveryNewRun(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.Model.Mode = "default"
	repo.agents[a.ID] = a
	var seen []string
	resolver := &configResolver{model: "old", provider: modelFunc(func(_ context.Context, r ModelRequest) (ModelResponse, error) {
		seen = append(seen, r.Config.Model)
		return final("ok"), nil
	})}
	runtime := NewRuntime(repo, NewToolRegistry(), nil)
	runtime.Resolver = resolver
	for _, model := range []string{"old", "new"} {
		resolver.model = model
		if _, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(seen, ",") != "old,new" {
		t.Fatal("stale model", seen)
	}
}

func TestRuntimeMultiCallAndAutomaticIsolatedMemory(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.EnabledTools = []string{"echo"}
	a.Permissions = []string{"echo.read"}
	repo.agents[a.ID] = a
	repo.memories = []Memory{{ID: "mine", OwnerID: "owner", AgentID: "a", Content: "PRIVATE_A"}, {ID: "other", OwnerID: "owner", AgentID: "b", Content: "PRIVATE_B"}, {ID: "other-user", OwnerID: "other", AgentID: "a", Content: "OTHER_USER"}}
	tools := NewToolRegistry()
	if err := tools.Register(ToolSpec{Name: "echo", Description: "echo", Permissions: []string{"echo.read"}, InputSchema: object(map[string]any{"value": str(20)}, "value"), OutputSchema: json.RawMessage(`{"type":"string"}`)}, func(_ context.Context, e ExecutionContext, raw json.RawMessage) (any, error) {
		if e.Scope.OwnerID != "owner" || e.Scope.AgentID != "a" || e.RunID == "" {
			t.Fatal("untrusted scope")
		}
		return decode[struct {
			Value string `json:"value"`
		}](raw).Value, nil
	}); err != nil {
		t.Fatal(err)
	}
	model := modelFunc(func(ctx context.Context, r ModelRequest) (ModelResponse, error) {
		encoded, _ := json.Marshal(r.Messages)
		if !strings.Contains(string(encoded), "PRIVATE_A") || strings.Contains(string(encoded), "PRIVATE_B") || strings.Contains(string(encoded), "OTHER_USER") {
			t.Fatal("memory isolation failed")
		}
		if len(r.Tools) != 1 {
			t.Fatal("tool filtering failed")
		}
		switch len(r.Messages) {
		case 3:
			return calls(call("one", "echo", `{"value":"first"}`), call("two", "echo", `{"value":"second"}`)), nil
		case 6:
			if r.Messages[4].ToolCallID != "one" || r.Messages[5].ToolCallID != "two" || !strings.Contains(r.Messages[4].Content, "first") || !strings.Contains(r.Messages[5].Content, "second") {
				t.Fatal("tool_call_id mismatched")
			}
			return calls(call("three", "unknown", `{}`)), nil
		case 8:
			if !strings.Contains(r.Messages[7].Content, "TOOL_NOT_FOUND") {
				t.Fatal("unknown tool missing")
			}
			return final("done"), nil
		}
		t.Fatalf("unexpected history: %d", len(r.Messages))
		return ModelResponse{}, nil
	})
	runtime := NewRuntime(repo, tools, map[string]ModelProvider{"mock": model})
	for range 2 {
		result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: "a", Trigger: DataChanged, Input: json.RawMessage(`{"event":"x"}`)})
		if err != nil || result.Output != "done" {
			t.Fatalf("result %v, %v", result, err)
		}
		record := repo.runs[result.RunID]
		if record.Status != "succeeded" || record.ModelCalls != 3 || record.ToolCalls != 3 || record.FinishedAt == nil {
			t.Fatal("run metrics not saved")
		}
	}
}
func TestRuntimeLimitsTimeoutsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits Limits
		model  modelFunc
		code   string
	}{
		{"provider timeout", Limits{RunTimeout: 10 * time.Millisecond}, func(ctx context.Context, _ ModelRequest) (ModelResponse, error) {
			<-ctx.Done()
			return ModelResponse{}, ctx.Err()
		}, "TIMEOUT"},
		{"model limit", Limits{ModelCalls: 1}, func(context.Context, ModelRequest) (ModelResponse, error) {
			return calls(call("a", "unknown", `{}`)), nil
		}, "EXECUTION_LIMIT_EXCEEDED"},
		{"multiple tool limit", Limits{ToolCalls: 1}, func(context.Context, ModelRequest) (ModelResponse, error) {
			return calls(call("a", "unknown", `{}`), call("b", "unknown", `{}`)), nil
		}, "EXECUTION_LIMIT_EXCEEDED"},
		{"duplicate IDs", Limits{}, func(context.Context, ModelRequest) (ModelResponse, error) {
			return calls(call("a", "unknown", `{}`), call("a", "unknown", `{}`)), nil
		}, "PROVIDER_ERROR"},
		{"truncated completion", Limits{}, func(context.Context, ModelRequest) (ModelResponse, error) {
			response := final("partial")
			response.FinishReason = "length"
			return response, nil
		}, "PROVIDER_ERROR"},
		{"provider panic", Limits{}, func(context.Context, ModelRequest) (ModelResponse, error) { panic("secret") }, "INTERNAL_ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			a := fixtureAgent("a")
			a.Limits = tc.limits.Defaults()
			repo.agents[a.ID] = a
			r := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": tc.model})
			result, err := r.Run(context.Background(), "owner", RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)})
			requireCode(t, err, tc.code)
			record := repo.runs[result.RunID]
			if record.Status != "failed" || record.Error.Code != tc.code {
				t.Fatal("failure record missing")
			}
		})
	}
}
func TestRuntimeContextAndIdentity(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.Limits.ContextBytes = 40
	repo.agents["a"] = a
	r := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
		t.Fatal("oversized context reached provider")
		return final(""), nil
	})})
	req := RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)}
	_, err := r.Run(context.Background(), "owner", req)
	requireCode(t, err, "EXECUTION_LIMIT_EXCEEDED")
	_, err = r.Run(context.Background(), "intruder", req)
	requireCode(t, err, "AGENT_NOT_FOUND")
	req.Trigger = AgentDispatch
	_, err = r.Run(context.Background(), "owner", req)
	requireCode(t, err, "PERMISSION_DENIED")
}
func TestToolSchemaPermissionTimeoutAndPanic(t *testing.T) {
	r := NewToolRegistry()
	invocations := 0
	spec := ToolSpec{Name: "restricted", Description: "test", Permissions: []string{"test"}, InputSchema: object(map[string]any{"value": integer(1, 3)}, "value"), OutputSchema: json.RawMessage(`{"type":"string"}`)}
	if err := r.Register(spec, func(_ context.Context, _ ExecutionContext, _ json.RawMessage) (any, error) {
		invocations++
		return "ok", nil
	}); err != nil {
		t.Fatal(err)
	}
	e := ExecutionContext{Scope: Scope{OwnerID: "owner", AgentID: "a"}, EnabledTools: []string{"restricted"}, Limits: Limits{}.Defaults(), budget: &budget{tools: 10}}
	result := r.Execute(context.Background(), e, "restricted", json.RawMessage(`{"value":1}`))
	if result.Error.Code != "PERMISSION_DENIED" {
		t.Fatal(result)
	}
	e.Permissions = []string{"test"}
	for _, input := range []string{`{"value":0}`, `{"value":1,"owner_user_id":"other"}`, `{"value":"1"}`, `{}`, `null`, `invalid`} {
		result = r.Execute(context.Background(), e, "restricted", json.RawMessage(input))
		if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
			t.Fatal("schema accepted", input, result)
		}
	}
	if invocations != 0 {
		t.Fatal("unauthorized handler executed")
	}
	if result = r.Execute(context.Background(), e, "restricted", json.RawMessage(`{"value":1}`)); !result.OK {
		t.Fatal(result)
	}
	spec.Name = "slow"
	spec.Timeout = time.Millisecond
	if err := r.Register(spec, func(ctx context.Context, _ ExecutionContext, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	e.EnabledTools = append(e.EnabledTools, "slow")
	if result = r.Execute(context.Background(), e, "slow", json.RawMessage(`{"value":1}`)); result.Error.Code != "TIMEOUT" {
		t.Fatal(result)
	}
	spec.Name = "panic"
	if err := r.Register(spec, func(context.Context, ExecutionContext, json.RawMessage) (any, error) { panic("secret") }); err != nil {
		t.Fatal(err)
	}
	e.EnabledTools = append(e.EnabledTools, "panic")
	if result = r.Execute(context.Background(), e, "panic", json.RawMessage(`{"value":1}`)); result.Error.Code != "INTERNAL_ERROR" {
		t.Fatal(result)
	}
}
func TestMemorySelectionPreservesDatabase(t *testing.T) {
	repo := newFakeRepo()
	repo.memories = []Memory{{ID: "large", OwnerID: "owner", AgentID: "a", Content: strings.Repeat("x", 2000)}, {ID: "small", OwnerID: "owner", AgentID: "a", Content: "kept"}}
	text, err := LoadMemories(context.Background(), repo, Scope{OwnerID: "owner", AgentID: "a"}, 500)
	if err != nil || !strings.Contains(text, "kept") || strings.Contains(text, "large") || len(repo.memories) != 2 {
		t.Fatalf("budget selection: %q %v", text, err)
	}
}
func TestDispatchCycleAndDepth(t *testing.T) {
	repo := newFakeRepo()
	repo.agents["a"] = fixtureAgent("a")
	repo.agents["b"] = fixtureAgent("b")
	r := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{})
	parent := ExecutionContext{Scope: Scope{OwnerID: "owner", AgentID: "a"}, RunID: "parent", Permissions: []string{"agent.dispatch"}, Limits: Limits{DispatchDepth: 1}, ancestry: []string{"a"}, budget: &budget{models: 1, tools: 1}}
	_, err := r.Run(DispatchContext(context.Background(), parent), "owner", RunRequest{AgentID: "a", Trigger: AgentDispatch, Input: json.RawMessage(`{}`)})
	requireCode(t, err, "EXECUTION_LIMIT_EXCEEDED")
	parent.ancestry = []string{"x", "a"}
	_, err = r.Run(DispatchContext(context.Background(), parent), "owner", RunRequest{AgentID: "b", Trigger: AgentDispatch, Input: json.RawMessage(`{}`)})
	requireCode(t, err, "EXECUTION_LIMIT_EXCEEDED")
}
func TestWorkflowExecutionUnsupported(t *testing.T) {
	requireCode(t, ExecuteWorkflow(context.Background(), Workflow{}), "NOT_IMPLEMENTED")
	if SafeError(errors.New("secret")).Message == "secret" {
		t.Fatal("internal error leaked")
	}
}

func TestRuntimeConcurrencyDoesNotDeadlock(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.Limits.MaxConcurrent = 1
	repo.agents[a.ID] = a
	entered := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	r := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
		close(entered)
		<-release
		return final("ok"), nil
	})})
	req := RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)}
	go func() { _, err := r.Run(context.Background(), "owner", req); first <- err }()
	<-entered
	_, err := r.Run(context.Background(), "owner", req)
	requireCode(t, err, "RATE_LIMITED")
	close(release)
	if err = <-first; err != nil {
		t.Fatal(err)
	}
}

func TestRunDeadlineDiagnosticsAndCancellation(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.Limits.RunTimeout = 10 * time.Millisecond
	repo.agents[a.ID] = a
	runtime := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": modelFunc(func(ctx context.Context, _ ModelRequest) (ModelResponse, error) {
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	})})
	result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)})
	requireCode(t, err, "TIMEOUT")
	if result.RunID == "" || !strings.Contains(SafeError(err).Message, "model call 1") || repo.runs[result.RunID].Status != "failed" {
		t.Fatalf("missing diagnostic: %+v %v", result, err)
	}
	requireCode(t, context.Canceled, "CANCELLED")
}

func TestNestedToolUsesRunBudget(t *testing.T) {
	registry := NewToolRegistry()
	handler := func(ctx context.Context, _ ExecutionContext, _ json.RawMessage) (any, error) {
		select {
		case <-time.After(40 * time.Millisecond):
			return "done", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	for _, runBudget := range []bool{false, true} {
		name := "ordinary"
		if runBudget {
			name = "nested"
		}
		if err := registry.Register(ToolSpec{Name: name, Description: "test deadline policy", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"string"}`), UseRunTimeout: runBudget}, handler); err != nil {
			t.Fatal(err)
		}
	}
	e := toolContext([]string{"ordinary", "nested"}, nil)
	e.Limits.ToolTimeout = 5 * time.Millisecond
	e.Limits.RunTimeout = time.Second
	first := registry.Execute(context.Background(), e, "ordinary", json.RawMessage(`{}`))
	if first.Error == nil || first.Error.Code != "TIMEOUT" || !strings.Contains(first.Error.Message, "ordinary") {
		t.Fatalf("ordinary: %+v", first)
	}
	second := registry.Execute(context.Background(), e, "nested", json.RawMessage(`{}`))
	if !second.OK {
		t.Fatalf("nested: %+v", second)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	third := registry.Execute(ctx, e, "nested", json.RawMessage(`{}`))
	if third.Error == nil || third.Error.Code != "TIMEOUT" {
		t.Fatalf("parent deadline not honored: %+v", third)
	}
	builtins := NewToolRegistry()
	if err := (Builtins{}).Register(builtins); err != nil {
		t.Fatal(err)
	}
	for _, spec := range builtins.Catalog() {
		if spec.Name == "agent_dispatch" && (!spec.UseRunTimeout || spec.Timeout != 0) {
			t.Fatal("dispatch must use remaining run budget")
		}
	}
}
