package framework

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReasoningEffortWirePreservesNullAndLevels(t *testing.T) {
	for _, value := range []string{`null`, `"low"`, `"high"`, `"max"`} {
		t.Run(value, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var p map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&p) != nil {
					t.Error("bad JSON")
				}
				if string(p["reasoning_effort"]) != value {
					t.Error("effort not preserved", string(p["reasoning_effort"]))
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			provider, _ := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "test"})
			_, err := provider.Complete(context.Background(), ModelRequest{Config: ModelConfig{Model: "gpt-6-luna", Parameters: map[string]json.RawMessage{"reasoning_effort": json.RawMessage(value)}}})
			if err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
}
func TestCustomModelParameterValidation(t *testing.T) {
	for _, raw := range []string{`{"reasoning_effort":null,"top_p":0.5,"seed":7,"presence_penalty":-1,"frequency_penalty":2,"max_tokens":1024}`, `{"reasoning_effort":"max"}`} {
		var p map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &p)
		if err := ValidateModelParameters(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"messages":[]}`, `{"reasoning_effort":false}`, `{"top_p":2}`, `{"seed":1.5}`, `{"max_tokens":0}`, `{"frequency_penalty":3}`} {
		var p map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &p)
		if err := ValidateModelParameters(p); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}
func TestRegistryRejectsInvalidParametersBeforeSaving(t *testing.T) {
	r := newFakeRepo()
	a := fixtureAgent("a")
	a.Model.Parameters = map[string]json.RawMessage{"messages": json.RawMessage(`[]`)}
	if _, err := (Registry{Repository: r}).Save(context.Background(), "owner", a); err == nil {
		t.Fatal("invalid override saved")
	}
	if len(r.agents) != 0 {
		t.Fatal("saved invalid configuration")
	}
}
