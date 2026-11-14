package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type dispatchTargetsRepo struct {
	*fakeRepo
	list func(context.Context, string, int, string) ([]AgentInstance, error)
}

func (r dispatchTargetsRepo) ListAgents(ctx context.Context, owner string, limit int, after string) ([]AgentInstance, error) {
	return r.list(ctx, owner, limit, after)
}

func mustCompileTargetSchema(t *testing.T, raw json.RawMessage) interface{ Validate(any) error } {
	t.Helper()
	schema, err := compileSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func dispatchTargetProperty(t *testing.T, spec ToolSpec) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	return schema["properties"].(map[string]any)["target_agent_id"].(map[string]any)
}

func TestAttachDispatchTargetsOwnedEnabledGenericAndImmutable(t *testing.T) {
	agents := []AgentInstance{fixtureAgent("self"), fixtureAgent("worker-id"), fixtureAgent("learner-id"), fixtureAgent("custom-id"), fixtureAgent("foreign"), fixtureAgent("disabled")}
	agents[1].Type, agents[2].Type, agents[3].Type = "worker", "learner", "custom-role"
	agents[1].Name = "Worker \"name\"\nwith text"
	agents[1].SystemPrompt = "SECRET_PROMPT"
	agents[1].Model.Parameters = map[string]json.RawMessage{"secret": json.RawMessage(`"SECRET_KEY"`)}
	agents[4].OwnerID = "other-owner"
	agents[5].Status = "disabled"
	repo := dispatchTargetsRepo{list: func(_ context.Context, owner string, limit int, after string) ([]AgentInstance, error) {
		if owner != "owner" || limit != 100 || after != "" {
			t.Fatal("incorrect owner or pagination", owner, limit, after)
		}
		return agents, nil
	}}
	specs := []ToolSpec{{Name: "agent_dispatch", InputSchema: dispatchInputSchema()}, {Name: "other", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	original := append([]byte(nil), specs[0].InputSchema...)
	out := attachDispatchTargets(context.Background(), repo, Scope{OwnerID: "owner", AgentID: "self", WorkflowID: "wf"}, specs)
	property := dispatchTargetProperty(t, out[0])
	if !reflect.DeepEqual(property["enum"], []any{"custom-id", "learner-id", "worker-id"}) {
		t.Fatal("incorrect targets", property)
	}
	description := property["description"].(string)
	for _, want := range []string{`"role":"worker"`, `"role":"learner"`, `"role":"custom-role"`, `Worker \"name\"\nwith text`} {
		if !strings.Contains(description, want) {
			t.Fatal("missing escaped target metadata", want, description)
		}
	}
	for _, forbidden := range []string{"SECRET_PROMPT", "SECRET_KEY", "foreign", "disabled"} {
		if strings.Contains(description, forbidden) {
			t.Fatal("unexpected metadata exposed", forbidden)
		}
	}
	if !bytes.Equal(specs[0].InputSchema, original) || !bytes.Equal(out[1].InputSchema, specs[1].InputSchema) {
		t.Fatal("input specs mutated")
	}
	if err := mustCompileTargetSchema(t, out[0].InputSchema).Validate(map[string]any{"target_agent_id": "invented-worker", "task": "test"}); err == nil {
		t.Fatal("unknown target accepted by provider schema")
	}
	// Registered validation remains unrestricted: hints do not create authority.
	if err := mustCompileTargetSchema(t, original).Validate(map[string]any{"target_agent_id": "newly-added-id", "task": "test"}); err != nil {
		t.Fatal("original schema restricted", err)
	}
}

func TestAttachDispatchTargetsPaginationAndSoftFailures(t *testing.T) {
	for _, mode := range []string{"pages", "error", "later-error", "cancelled", "truncated", "stalled", "empty", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			calls := 0
			repo := dispatchTargetsRepo{list: func(_ context.Context, owner string, limit int, after string) ([]AgentInstance, error) {
				calls++
				if owner != "owner" || limit != 100 {
					t.Fatal("bad listing scope")
				}
				if mode == "panic" {
					panic("optional discovery unavailable")
				}
				if mode == "error" || (mode == "later-error" && calls == 2) {
					return nil, errors.New("listing failed")
				}
				if mode == "empty" {
					return nil, nil
				}
				if calls == 2 && mode == "pages" {
					if after != "target-0099" {
						t.Fatal("wrong cursor", after)
					}
					return []AgentInstance{fixtureAgent("target-last")}, nil
				}
				out := make([]AgentInstance, 100)
				for i := range out {
					n := (calls-1)*100 + i
					if mode == "stalled" {
						n = i
					}
					out[i] = fixtureAgent(fmt.Sprintf("target-%04d", n))
				}
				return out, nil
			}}
			specs := []ToolSpec{{Name: "agent_dispatch", InputSchema: dispatchInputSchema()}}
			out := attachDispatchTargets(ctx, repo, Scope{OwnerID: "owner", AgentID: "self"}, specs)
			if mode == "pages" {
				if got := len(dispatchTargetProperty(t, out[0])["enum"].([]any)); got != 101 {
					t.Fatal("incomplete enumeration", got)
				}
			} else if !bytes.Equal(out[0].InputSchema, specs[0].InputSchema) {
				t.Fatal("optional failure changed original schema")
			}
			if calls > 10 || (mode == "cancelled" && calls != 0) {
				t.Fatal("listing exceeded bound or ignored cancellation", calls)
			}
		})
	}
}

func TestRuntimeDispatchTargetsOnlyForAuthorizedEnabledTool(t *testing.T) {
	for _, mode := range []string{"authorized", "no-permission", "disabled-tool", "disable-tools"} {
		t.Run(mode, func(t *testing.T) {
			base := newFakeRepo()
			a := fixtureAgent("self")
			a.EnabledTools, a.Permissions = []string{"agent_dispatch"}, []string{"agent.dispatch"}
			if mode == "no-permission" {
				a.Permissions = nil
			}
			if mode == "disabled-tool" {
				a.EnabledTools = nil
			}
			base.agents[a.ID] = a
			listed := 0
			repo := dispatchTargetsRepo{fakeRepo: base, list: func(context.Context, string, int, string) ([]AgentInstance, error) {
				listed++
				return []AgentInstance{fixtureAgent("actual-worker")}, nil
			}}
			tools := NewToolRegistry()
			if err := (Builtins{}).Register(tools); err != nil {
				t.Fatal(err)
			}
			provider := modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
				if mode == "authorized" {
					if len(req.Tools) != 1 || !reflect.DeepEqual(dispatchTargetProperty(t, req.Tools[0])["enum"], []any{"actual-worker"}) {
						t.Fatal("runtime omitted actual dispatch IDs", req.Tools)
					}
				} else if len(req.Tools) != 0 {
					t.Fatal("unauthorized tools exposed")
				}
				return final("done"), nil
			})
			_, err := NewRuntime(repo, tools, map[string]ModelProvider{"mock": provider}).Run(context.Background(), "owner", RunRequest{AgentID: "self", Trigger: UserMessage, Input: json.RawMessage(`{}`), DisableTools: mode == "disable-tools"})
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "authorized" {
				want = 1
			}
			if listed != want {
				t.Fatal("unexpected discovery calls", listed)
			}
			original := tools.Authorized(ExecutionContext{EnabledTools: []string{"agent_dispatch"}, Permissions: []string{"agent.dispatch"}})
			if _, exists := dispatchTargetProperty(t, original[0])["enum"]; exists {
				t.Fatal("global registry schema mutated")
			}
		})
	}
}
