package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOutputValidationSameRunFormatAndSemantic(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.EnabledTools = []string{"fix"}
	repo.agents[a.ID] = a
	tools := NewToolRegistry()
	writes := 0
	if err := tools.Register(ToolSpec{Name: "fix", Description: "Fix synthetic state", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
		writes++
		return map[string]bool{"fixed": true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	round := 0
	var seenRun string
	p := modelFunc(func(_ context.Context, r ModelRequest) (ModelResponse, error) {
		round++
		identity := strings.Split(r.Messages[0].Content, "Trusted run_id=")[1]
		if seenRun == "" {
			seenRun = identity
		} else if seenRun != identity {
			t.Fatal("new Run during correction")
		}
		response := final(`{"ready":true}`)
		response.Usage = Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}
		switch round {
		case 1:
			response.Message.Content = "{broken"
		case 2:
			if len(r.Tools) != 0 || !strings.Contains(r.Messages[len(r.Messages)-1].Content, "format-only") {
				t.Fatal("format correction has tools or lost feedback")
			}
			response.Message.Content = `{"ready":false}`
		case 3:
			if len(r.Tools) != 1 {
				t.Fatal("semantic tools not restored")
			}
			response = calls(call("repair", "fix", `{}`))
			response.Usage = Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}
		case 4:
			if writes != 1 {
				t.Fatal("semantic tool not called")
			}
		default:
			t.Fatal("unexpected model call", round)
		}
		return response, nil
	})
	ctx := WithOutputValidation(context.Background(), OutputValidation{OwnerID: "owner", AgentID: "a", MaxCorrections: 2, Validate: func(_ context.Context, in OutputValidationInput) (*OutputValidationFailure, error) {
		if !json.Valid([]byte(in.Output)) {
			return &OutputValidationFailure{Kind: OutputFormat, Code: "INVALID_JSON", Feedback: "Fix JSON syntax."}, nil
		}
		if writes == 0 {
			return &OutputValidationFailure{Kind: OutputSemantic, Code: "NOT_READY", Feedback: "Call fix once before completing."}, nil
		}
		return nil, nil
	}})
	result, err := NewRuntime(repo, tools, map[string]ModelProvider{"mock": p}).Run(ctx, "owner", RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(result, err)
	}
	record := repo.runs[result.RunID]
	if record.ModelCalls != 4 || record.ToolCalls != 1 || record.Usage.TotalTokens != 48 || len(record.OutputCorrections) != 2 || record.OutputCorrections[0].Kind != OutputFormat || record.OutputCorrections[1].Kind != OutputSemantic {
		t.Fatal(record)
	}
}

func TestOutputValidationFormatRejectsReturnedCalls(t *testing.T) {
	repo := newFakeRepo()
	repo.agents["a"] = fixtureAgent("a")
	round := 0
	p := modelFunc(func(_ context.Context, r ModelRequest) (ModelResponse, error) {
		round++
		if round == 1 {
			return final("bad"), nil
		}
		if len(r.Tools) != 0 {
			t.Fatal("format tools exposed")
		}
		return calls(call("bad", "write", `{}`)), nil
	})
	ctx := WithOutputValidation(context.Background(), OutputValidation{OwnerID: "owner", AgentID: "a", MaxCorrections: 2, Validate: func(context.Context, OutputValidationInput) (*OutputValidationFailure, error) {
		return &OutputValidationFailure{Kind: OutputFormat, Code: "INVALID_JSON", Feedback: "Correct JSON."}, nil
	}})
	result, err := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": p}).Run(ctx, "owner", RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)})
	requireCode(t, err, "PERMISSION_DENIED")
	if repo.runs[result.RunID].ToolCalls != 0 {
		t.Fatal("format executed tools")
	}
}

func TestOutputValidationBoundCancelFatalAndBudgets(t *testing.T) {
	for _, mode := range []string{"bounded", "cancel", "fatal", "model budget", "context budget", "nil", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			repo := newFakeRepo()
			a := fixtureAgent("a")
			if mode == "model budget" {
				a.Limits.ModelCalls = 1
			}
			if mode == "context budget" {
				a.Limits.ContextBytes = 2200
			}
			repo.agents["a"] = a
			round := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			policy := OutputValidation{OwnerID: "owner", AgentID: "a", MaxCorrections: 99, Validate: func(context.Context, OutputValidationInput) (*OutputValidationFailure, error) {
				if mode == "cancel" {
					cancel()
				}
				if mode == "fatal" {
					return nil, Fail("CONFLICT", "stale storage baseline")
				}
				feedback := "Fix syntax"
				if mode == "context budget" {
					feedback = strings.Repeat("large-feedback", 400)
				}
				return &OutputValidationFailure{Kind: OutputFormat, Code: "INVALID_JSON", Feedback: feedback}, nil
			}}
			if mode == "nil" {
				policy.Validate = nil
			}
			if mode == "foreign" {
				policy.AgentID = "other"
			}
			ctx = WithOutputValidation(ctx, policy)
			result, err := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) { round++; return final("bad"), nil })}).Run(ctx, "owner", RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{}`)})
			switch mode {
			case "bounded":
				requireCode(t, err, "INVALID_JSON")
				if round != 3 || len(repo.runs[result.RunID].OutputCorrections) != 3 {
					t.Fatal(round, repo.runs[result.RunID])
				}
			case "cancel":
				requireCode(t, err, "CANCELLED")
				if round != 1 {
					t.Fatal(round)
				}
			case "fatal":
				requireCode(t, err, "CONFLICT")
				if round != 1 {
					t.Fatal(round)
				}
			case "model budget", "context budget":
				requireCode(t, err, "EXECUTION_LIMIT_EXCEEDED")
				if round != 1 {
					t.Fatal(round)
				}
			default:
				if err != nil || round != 1 {
					t.Fatal(result, err, round)
				}
			}
		})
	}
}
