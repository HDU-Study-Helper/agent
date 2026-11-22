package framework

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTranscriptCapturesLogicalTurnsWithoutReplayingEffects(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.SystemPrompt = "host-private-policy"
	a.EnabledTools = []string{"write"}
	a.Permissions = []string{"write.allowed"}
	repo.agents[a.ID] = a
	tools := NewToolRegistry()
	writes := 0
	err := tools.Register(ToolSpec{Name: "write", Description: "test", Permissions: a.Permissions, InputSchema: object(map[string]any{}), OutputSchema: json.RawMessage(`{"type":"string"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) {
		writes++
		return "receipt-123", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	round := 0
	r := NewRuntime(repo, tools, map[string]ModelProvider{"mock": modelFunc(func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		round++
		switch round {
		case 1:
			return calls(call("execute", "write", "{}")), nil
		case 2:
			return final("completed action; one question"), nil
		default:
			for _, m := range req.Messages {
				if m.Role == "tool" || len(m.ToolCalls) > 0 {
					t.Fatal("replayed a historical tool frame")
				}
			}
			raw, _ := json.Marshal(req.Messages)
			if !strings.Contains(string(raw), "receipt-123") || strings.Contains(string(raw), "test-secret") {
				t.Fatal("missing receipt or retained secret")
			}
			return final("answered"), nil
		}
	})})
	var archive Transcript
	ctx := WithTranscript(context.Background(), "owner", "a", nil, func(x Transcript) { archive = x })
	req := RunRequest{AgentID: "a", Trigger: UserMessage, Input: json.RawMessage(`{"task":"save","password":"test-secret"}`)}
	if _, e := r.Run(ctx, "owner", req); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(archive)
	if writes != 1 || strings.Contains(string(raw), "test-secret") || strings.Contains(string(raw), a.SystemPrompt) || len(archive.Messages) != 4 {
		t.Fatalf("invalid capture (%d messages)", len(archive.Messages))
	}
	req.Input = json.RawMessage(`{"answer":"yes"}`)
	ctx = WithTranscript(context.Background(), "owner", "a", &archive, nil)
	if _, e := r.Run(ctx, "owner", req); e != nil || writes != 1 {
		t.Fatal(e, writes)
	}
	a.Permissions = nil
	repo.agents[a.ID] = a
	_, e := r.Run(ctx, "owner", req)
	requireCode(t, e, "PERMISSION_DENIED")
	archive.Owner = "other"
	_, e = r.Run(ctx, "owner", req)
	requireCode(t, e, "PERMISSION_DENIED")
}
