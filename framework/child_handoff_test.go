package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const learnerHandoffJSON = `{"request_id":null,"item_id":null,"parent_item_id":null,"entity":null,"status":"ERROR","operation":"CREATE","workflow_id":"created-workflow","workflow_version":1,"summary":"Workflow已提交但尚未核验","reason":"写入已提交，回读未完成","evidence_refs":[],"changes":[],"questions":[],"suggestions":[],"verified":false,"profile_initialization":{"memory_ids":["committed-memory"],"owner_workflow_id":"created-workflow","verified":true}}`

func workerHandoffJSON(runID string) string {
	return fmt.Sprintf(`{"run_id":%q,"workflow_id":"wf","workflow_version":3,"status":"PARTIAL","summary":"已保存记忆，其他核验未完成","artifacts":[],"actions":[],"evidence_refs":[],"interaction":null,"collaboration":null,"evolution_feedback":[]}`, runID)
}

func TestChildHandoffScope(t *testing.T) {
	host := WithHostEvidence(context.Background(), "owner", json.RawMessage(`{"entity":{"kind":"course","id":"course-1"}}`), nil)
	for _, tc := range []struct {
		name, kind, input, workflow, want string
		ctx                               context.Context
	}{
		{"generic worker", "worker", `{}`, "", "", host},
		{"untrusted workflow", "worker", `{"workflow":{"workflow_id":"wf","version":3}}`, "", "", host},
		{"bound worker", "worker", `{}`, "wf", "worker", context.Background()},
		{"generic learner", "learner", `{}`, "", "", host},
		{"unscoped task", "learner", `{"task":"hello"}`, "", "", context.Background()},
		{"top request", "learner", `{"request_type":"CREATE"}`, "", "learner", context.Background()},
		{"dispatch request", "learner", `{"task":"create","context":{"request_type":"CREATE"}}`, "", "learner", context.Background()},
		{"context entity", "learner", `{"task":"evaluate","context":{"entity":{"id":"course-1"}}}`, "", "learner", context.Background()},
		{"host entity", "learner", `{"task":"evaluate"}`, "", "learner", host},
		{"JSON host cannot bind", "learner", `{"task":"evaluate","host_evidence":{"entity":{"id":"course-1"}}}`, "", "", context.Background()},
		{"wrong host owner", "learner", `{"task":"evaluate"}`, "", "", WithHostEvidence(context.Background(), "foreign", json.RawMessage(`{"entity":{"kind":"course","id":"c"}}`), nil)},
		{"custom role", "custom", `{"request_type":"CREATE"}`, "wf", "", host},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := fixtureAgent("child")
			a.Type = tc.kind
			g := newChildHandoff(tc.ctx, a, ExecutionContext{Scope: Scope{OwnerID: "owner", WorkflowID: tc.workflow}}, RunRequest{Input: json.RawMessage(tc.input)})
			if g.kind != tc.want {
				t.Fatalf("scope=%q want %q", g.kind, tc.want)
			}
		})
	}
}

func TestChildHandoffStrictContractsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		kind, valid string
	}{
		{"worker", workerHandoffJSON("actual-run")},
		{"learner", learnerHandoffJSON},
		{"learner", `{"results":[` + learnerHandoffJSON + `]}`},
	} {
		g := childHandoff{kind: tc.kind, runID: "actual-run", workflowID: "wf", workflowVersion: 3}
		if feedback, err := g.validate(tc.valid, nil); feedback != "" || err != nil {
			t.Fatalf("valid %s rejected: %s %v", tc.kind, feedback, err)
		}
		for _, output := range []string{"# 已完成", "```json\n" + tc.valid + "\n```", `[]`, `null`, `{}`, tc.valid + " trailing"} {
			g.corrections = 0
			for range 2 {
				feedback, err := g.validate(output, []ToolExecution{{Name: "memory_create", ResultRefs: []ToolResultRef{{Kind: "MEMORY", ID: "actual-memory"}}}})
				if err != nil || !strings.Contains(feedback, "FORMAT_ONLY") || !strings.Contains(feedback, "actual-run") || !strings.Contains(feedback, "actual-memory") {
					t.Fatalf("missing format correction: %s %v", feedback, err)
				}
			}
			_, err := g.validate(output, nil)
			requireCode(t, err, "INVALID_CHILD_RESULT")
		}
	}
	for _, output := range []string{
		strings.Replace(workerHandoffJSON("actual-run"), `"wf"`, `"forged"`, 1),
		workerHandoffJSON("forged"),
		strings.Replace(workerHandoffJSON("actual-run"), `"workflow_version":3`, `"workflow_version":"3"`, 1),
		strings.Replace(workerHandoffJSON("actual-run"), `"status":"PARTIAL"`, `"status":"APPLIED"`, 1),
		strings.Replace(workerHandoffJSON("actual-run"), `"actions":[]`, `"actions":null`, 1),
	} {
		g := childHandoff{kind: "worker", runID: "actual-run", workflowID: "wf", workflowVersion: 3}
		if feedback, err := g.validate(output, nil); feedback == "" || err != nil {
			t.Fatal("invalid Worker identity/shape accepted", output, err)
		}
	}
	for _, output := range []string{
		strings.Replace(learnerHandoffJSON, `"verified":false`, `"verified":null`, 1),
		strings.Replace(learnerHandoffJSON, `"status":"ERROR"`, `"status":"FAILED"`, 1),
		strings.Replace(learnerHandoffJSON, `"operation":"CREATE"`, `"operation":"RUN"`, 1),
		strings.Replace(learnerHandoffJSON, `"workflow_version":1`, `"workflow_version":"1"`, 1),
		`{"results":[]}`,
		`{"results":[null]}`,
	} {
		g := childHandoff{kind: "learner"}
		if feedback, err := g.validate(output, nil); feedback == "" || err != nil {
			t.Fatal("invalid Learner shape accepted", output, err)
		}
	}
}

type childHandoffRepo struct{ *fakeRepo }

func (r childHandoffRepo) GetWorkflow(_ context.Context, owner, id string) (Workflow, error) {
	return Workflow{ID: id, OwnerID: owner, Version: 3, Status: "active", Definition: json.RawMessage(`{}`)}, nil
}

func TestChildRuntimeCorrectsAfterCommittedWritesWithoutRepeatingTools(t *testing.T) {
	for _, kind := range []string{"learner", "worker"} {
		for _, outcome := range []string{"corrected", "invalid", "attempted duplicate"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				repo := childHandoffRepo{newFakeRepo()}
				a := fixtureAgent("child")
				a.Type = kind
				a.EnabledTools = []string{"memory_create"}
				repo.agents[a.ID] = a
				tools := NewToolRegistry()
				writes, round := 0, 0
				if err := tools.Register(ToolSpec{Name: "memory_create", Description: "test persistence", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"memory_id":{"type":"string"}},"required":["memory_id"]}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
					writes++
					return map[string]string{"memory_id": "committed-memory"}, nil
				}); err != nil {
					t.Fatal(err)
				}
				var actualRunID string
				model := modelFunc(func(_ context.Context, request ModelRequest) (ModelResponse, error) {
					round++
					if round <= 2 && len(request.Tools) != 1 || round > 2 && len(request.Tools) != 0 {
						t.Fatal("incorrect correction tool availability", round, request.Tools)
					}
					if request.Config.Model != a.Model.Model || request.Config.Provider != a.Model.Provider {
						t.Fatal("correction changed provider or model")
					}
					if round == 1 {
						for id := range repo.runs {
							actualRunID = id
						}
						return calls(call("write", "memory_create", `{}`)), nil
					}
					if round == 2 || outcome == "invalid" {
						return final("# 已保存\ncommitted-memory"), nil
					}
					last := request.Messages[len(request.Messages)-1].Content
					if !strings.Contains(last, "FORMAT_ONLY") || !strings.Contains(last, "committed-memory") || !strings.Contains(last, actualRunID) {
						t.Fatal("correction lost audit or trusted run identity", last)
					}
					history, _ := json.Marshal(request.Messages)
					if !strings.Contains(string(history), `\"memory_id\":\"committed-memory\"`) {
						t.Fatal("committed tool result disappeared from history")
					}
					if outcome == "attempted duplicate" {
						return calls(call("duplicate", "memory_create", `{}`)), nil
					}
					if kind == "learner" {
						return final(learnerHandoffJSON), nil
					}
					return final(workerHandoffJSON(actualRunID)), nil
				})
				ctx := context.Background()
				if kind == "worker" {
					ctx = WorkflowContext(ctx, "owner", "wf")
				}
				runtime := NewRuntime(repo, tools, map[string]ModelProvider{"mock": model})
				result, err := runtime.Run(ctx, "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: json.RawMessage(`{"request_type":"CREATE"}`)})
				switch outcome {
				case "corrected":
					if err != nil || round != 3 || !json.Valid([]byte(result.Output)) {
						t.Fatal(result, err, round)
					}
				case "invalid":
					requireCode(t, err, "INVALID_CHILD_RESULT")
					if round != 4 {
						t.Fatal("correction count not bounded", round)
					}
				case "attempted duplicate":
					requireCode(t, err, "PERMISSION_DENIED")
				}
				record := repo.runs[result.RunID]
				if writes != 1 || record.ToolCalls != 1 || len(record.ToolExecutions) != 1 || record.ToolExecutions[0].ResultRefs[0].ID != "committed-memory" {
					t.Fatal("committed result lost or duplicated", writes, record)
				}
			})
		}
	}
}

func TestChildRuntimePreservesGenericRoleConversation(t *testing.T) {
	for _, kind := range []string{"learner", "worker", "custom"} {
		repo := newFakeRepo()
		a := fixtureAgent("child")
		a.Type = kind
		repo.agents[a.ID] = a
		runtime := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
			return final("# ordinary conversation"), nil
		})})
		result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: json.RawMessage(`{}`)})
		if err != nil || result.Output != "# ordinary conversation" || repo.runs[result.RunID].ModelCalls != 1 {
			t.Fatal(kind, result, err)
		}
	}
}
