package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesRuntimePreservesReasoningAcrossToolRound(t *testing.T) {
	requests, writes := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("wrong Responses transport")
			w.WriteHeader(400)
			return
		}
		var p struct {
			Input     []map[string]any
			Tools     []map[string]any
			Reasoning map[string]string
			Store     *bool
			Include   []string
			Previous  string `json:"previous_response_id"`
			Max       int    `json:"max_output_tokens"`
			Choice    string `json:"tool_choice"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil || p.Store == nil || *p.Store || p.Previous != "" || len(p.Include) != 1 || p.Include[0] != "reasoning.encrypted_content" || p.Reasoning["effort"] != "high" || p.Max != 1024 || p.Choice != "auto" || len(p.Tools) != 1 || p.Tools[0]["name"] != "write_test" || p.Tools[0]["strict"] != false {
			t.Error("invalid Responses request", p)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			fmt.Fprint(w, `{"status":"completed","output":[{"type":"reasoning","id":"rs_1","status":"completed","summary":[],"encrypted_content":"opaque-blob"},{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"write_test","arguments":"{}"}],"usage":{"input_tokens":10,"output_tokens":9,"total_tokens":19}}`)
		case 2:
			n := len(p.Input)
			if n < 3 || p.Input[n-3]["encrypted_content"] != "opaque-blob" || p.Input[n-2]["id"] != "fc_1" || p.Input[n-1]["type"] != "function_call_output" || p.Input[n-1]["call_id"] != "call_1" || !strings.Contains(fmt.Sprint(p.Input[n-1]["output"]), "saved") {
				t.Error("tool continuation lost original wire items", p.Input)
				w.WriteHeader(400)
				return
			}
			fmt.Fprint(w, `{"status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":22,"output_tokens":6,"total_tokens":28}}`)
		default:
			t.Error("unexpected request")
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	provider, err := NewOpenAIProvider(HTTPConfig{BaseURL: srv.URL + "/v1", APIKey: "test", APIProtocol: "responses"})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewToolRegistry()
	if err = reg.Register(ToolSpec{Name: "write_test", Description: "fixture", Permissions: []string{"test.write"}, InputSchema: object(map[string]any{}), OutputSchema: json.RawMessage(`{"type":"string"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) { writes++; return "saved", nil }); err != nil {
		t.Fatal(err)
	}
	repo := newFakeRepo()
	a := fixtureAgent("a")
	a.EnabledTools = []string{"write_test"}
	a.Permissions = []string{"test.write"}
	a.Model.MaxTokens = 1024
	a.Model.Parameters = map[string]json.RawMessage{"reasoning_effort": json.RawMessage(`"high"`)}
	repo.agents[a.ID] = a
	var transcript Transcript
	ctx := WithTranscript(context.Background(), "owner", a.ID, nil, func(v Transcript) { transcript = v })
	res, err := NewRuntime(repo, reg, map[string]ModelProvider{"mock": provider}).Run(ctx, "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: json.RawMessage(`{}`)})
	if err != nil || res.Output != "done" || writes != 1 || requests != 2 {
		t.Fatal(res, err, writes, requests)
	}
	if got := repo.runs[res.RunID].Usage; got.PromptTokens != 32 || got.CompletionTokens != 15 || got.TotalTokens != 47 {
		t.Fatal(got)
	}
	if len(transcript.Messages) == 0 {
		t.Fatal("logical transcript not captured")
	}
	for _, m := range transcript.Messages {
		if len(m.responsesItems) > 0 || strings.Contains(m.Content, "opaque-blob") {
			t.Fatal("provider reasoning persisted")
		}
	}
}

func TestResponsesIncompleteNeverReturnsToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"incomplete","output":[{"type":"function_call","call_id":"call_1","name":"write_test","arguments":"{}"}]}`)
	}))
	defer srv.Close()
	p, e := NewOpenAIProvider(HTTPConfig{BaseURL: srv.URL, APIKey: "test", APIProtocol: "responses"})
	if e != nil {
		t.Fatal(e)
	}
	out, e := p.Complete(context.Background(), ModelRequest{Config: ModelConfig{Model: "fixture"}, Tools: []ToolSpec{{Name: "write_test", InputSchema: object(map[string]any{})}}})
	if e == nil || len(out.Message.ToolCalls) != 0 {
		t.Fatal(out, e)
	}
}
