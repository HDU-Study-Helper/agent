package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const filterContractInput = `{"item_id":"root","processing_contract":"逐项处理，返回items"}`
const filterIgnored = `{"items":[{"item_id":"root","title":"事项","evidence_refs":[],"artifacts":[],"status":"IGNORED","reason":"目录没有画像或核查价值","routes":[]}]}`
const filterCompleted = `{"items":[{"item_id":"root","title":"事项","evidence_refs":[],"artifacts":[],"status":"COMPLETED","reason":"已完成查询","routes":[]}]}`
const filterPartial = `{"items":[{"item_id":"root","title":"事项","evidence_refs":[],"artifacts":[],"status":"PARTIAL","reason":"未能确定适用路由，尚未处理","routes":[]}]}`

func filterGuardFixture() (AgentInstance, ExecutionContext, RunRequest, context.Context) {
	a := fixtureAgent("filter")
	a.Type = "filter"
	a.EnabledTools = []string{"agent_dispatch", "workflow_get", "data_query"}
	a.Permissions = []string{"agent.dispatch", "workflow.read", "data.read"}
	e := ExecutionContext{Scope: Scope{OwnerID: "owner"}, EnabledTools: a.EnabledTools, Permissions: a.Permissions}
	ctx := WithHostEvidence(context.Background(), "owner", json.RawMessage(`{"entity":{"kind":"course","id":"course-1"}}`), json.RawMessage(`{"complete":true,"entity_id":"course-1","items":[]}`))
	return a, e, RunRequest{Input: json.RawMessage(filterContractInput)}, ctx
}

func TestFilterHandoffValidatesJSONAndItemContract(t *testing.T) {
	for _, output := range []string{
		"# Investigation finished",
		"```json\n" + filterIgnored + "\n```",
		`{"items":[]}`,
		`{"items":[{"item_id":"root","status":"IGNORED","reason":"missing required fields"}]}`,
		`{"items":[{"item_id":"root","title":"事项","evidence_refs":[],"artifacts":[],"status":"DONE","reason":"ok"}]}`,
		`{"items":[{"item_id":"root","title":"事项","evidence_refs":[],"artifacts":[],"status":"IGNORED","reason":" "}]}`,
		`{"items":[{"item_id":"other","status":"IGNORED","reason":"ok"}]}`,
		`{"items":[{"item_id":"root:1","parent_item_id":"root","status":"PARTIAL","reason":"x","routes":[{"item_id":"root:2"}]}]}`,
	} {
		g := filterHandoff{itemID: "root"}
		feedback, err := g.validate(output, nil)
		if err != nil || feedback == "" || !strings.Contains(feedback, `"item_id":"root"`) {
			t.Fatalf("missing contract correction for %q: %q %v", output, feedback, err)
		}
	}
}

func TestFilterHandoffUsesOnlyTrustedRoutingEvidence(t *testing.T) {
	a, e, req, trusted := filterGuardFixture()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		disable bool
		want    bool
	}{
		{"trusted complete empty", trusted, false, true},
		{"no host evidence", context.Background(), false, false},
		{"format only", trusted, true, false},
		{"wrong entity", WithHostEvidence(context.Background(), "owner", json.RawMessage(`{"entity":{"kind":"course","id":"other"}}`), json.RawMessage(`{"complete":true,"entity_id":"course-1","items":[]}`)), false, false},
		{"incomplete", WithHostEvidence(context.Background(), "owner", json.RawMessage(`{"entity":{"kind":"course","id":"course-1"}}`), json.RawMessage(`{"complete":false,"entity_id":"course-1","items":[]}`)), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req.DisableTools = tc.disable
			g := newFilterHandoff(tc.ctx, a, e, req)
			feedback, err := g.validate(filterCompleted, nil)
			if err != nil || (feedback != "") != tc.want {
				t.Fatal(feedback, err)
			}
		})
	}
	g := newFilterHandoff(context.Background(), a, e, RunRequest{Input: json.RawMessage(`{"item_id":"root","processing_contract":"items","host_evidence":{"entity":{"kind":"course","id":"course-1"},"workflow_summaries":{"complete":true,"items":[]}}}`)})
	if g.missingWF {
		t.Fatal("untrusted JSON established routing requirement")
	}
}

func TestFilterHandoffAllowsIgnoredAndHonestPartialButBoundsCorrections(t *testing.T) {
	a, e, req, ctx := filterGuardFixture()
	g := newFilterHandoff(ctx, a, e, req)
	if feedback, err := g.validate(filterIgnored, nil); feedback != "" || err != nil {
		t.Fatal(feedback, err)
	}
	if feedback, err := g.validate(filterPartial, nil); feedback == "" || err != nil {
		t.Fatal(feedback, err)
	}
	if feedback, err := g.validate(filterPartial, nil); feedback != "" || err != nil {
		t.Fatal(feedback, err)
	}
	_, err := g.validate(filterCompleted, nil)
	requireCode(t, err, "INCOMPLETE_FILTER_HANDOFF")
	g = newFilterHandoff(ctx, a, e, req)
	if feedback, err := g.validate(filterCompleted, []ToolExecution{{Name: "agent_dispatch"}}); feedback == "" || err != nil {
		t.Fatal("actual dispatch must still be reported in the item's routes", feedback, err)
	}
	g = filterHandoff{itemID: "root"}
	for range 2 {
		if feedback, err := g.validate("Markdown", nil); feedback == "" || err != nil {
			t.Fatal(feedback, err)
		}
	}
	_, err = g.validate("Markdown", nil)
	requireCode(t, err, "INVALID_ITEM_RESULT")
}

func TestFilterRuntimeContinuesWithToolsAndPreservesActualResults(t *testing.T) {
	repo := newFakeRepo()
	a, _, req, ctx := filterGuardFixture()
	repo.agents[a.ID] = a
	req.AgentID, req.Trigger = a.ID, DataChanged
	tools := NewToolRegistry()
	for _, name := range a.EnabledTools {
		name := name
		err := tools.Register(ToolSpec{Name: name, Description: "test", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
			return map[string]any{"run_id": "actual-child", "tool": name}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	round := 0
	model := modelFunc(func(_ context.Context, request ModelRequest) (ModelResponse, error) {
		round++
		if len(request.Tools) != 3 || request.Config.Model != a.Model.Model {
			t.Fatal("tools or model changed")
		}
		switch round {
		case 1:
			return calls(call("q", "data_query", `{}`)), nil
		case 2:
			return final("# Course analysis\nRead-only query completed"), nil
		case 3:
			if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "JSON") {
				t.Fatal("missing JSON correction")
			}
			return final(filterCompleted), nil
		case 4:
			if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "Learner") {
				t.Fatal("missing routing correction")
			}
			return calls(call("d", "agent_dispatch", `{}`)), nil
		case 5:
			if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "actual-child") {
				t.Fatal("lost actual child result")
			}
			return final(`{"items":[{"item_id":"root","title":"事项","evidence_refs":[],"artifacts":[],"status":"COMPLETED","reason":"真实子调用已完成","routes":[{"item_id":"root","target":"LEARNER","workflow_id":"","execution":{"run_id":"actual-child"}}]}]}`), nil
		}
		t.Fatal("unexpected extra model call")
		return final(""), nil
	})
	runtime := NewRuntime(repo, tools, map[string]ModelProvider{"mock": model})
	result, err := runtime.Run(ctx, "owner", req)
	if err != nil || !strings.Contains(result.Output, "actual-child") {
		t.Fatal(result, err)
	}
	record := repo.runs[result.RunID]
	if record.ModelCalls != 5 || record.ToolCalls != 2 || len(record.ToolExecutions) != 2 || record.ToolExecutions[0].Name != "data_query" || record.ToolExecutions[1].Name != "agent_dispatch" {
		t.Fatal(record)
	}
}

func TestFilterRuntimeFormatOnlyAndUnrelatedRuns(t *testing.T) {
	for _, tc := range []struct {
		name, input, output string
		disable             bool
		calls               int
	}{
		{"unrelated conversation", `{}`, "Markdown is allowed here", false, 1},
		{"format only", `{"mode":"FORMAT_ONLY","item_id":"root","original_input":{"processing_contract":"items"}}`, filterCompleted, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			a, _, _, ctx := filterGuardFixture()
			repo.agents[a.ID] = a
			round := 0
			model := modelFunc(func(_ context.Context, request ModelRequest) (ModelResponse, error) {
				round++
				if tc.disable && len(request.Tools) != 0 {
					t.Fatal("format-only tools enabled")
				}
				if tc.calls == 2 && round == 1 {
					return final("Markdown"), nil
				}
				return final(tc.output), nil
			})
			runtime := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": model})
			result, err := runtime.Run(ctx, "owner", RunRequest{AgentID: a.ID, Trigger: SystemEvent, Input: json.RawMessage(tc.input), DisableTools: tc.disable})
			if err != nil || result.Output != tc.output || round != tc.calls {
				t.Fatal(result, err, round)
			}
		})
	}
}
