package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const modelOK = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

func TestModelRequestsRetryOnlyTransientAndCancelBackoff(t *testing.T) {
	for _, status := range []int{429, 500, 502, 408, 401} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
					return
				}
				io.WriteString(w, modelOK)
			}))
			defer server.Close()
			p, err := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "key", Retries: 2, RetryBackoff: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Complete(context.Background(), ModelRequest{})
			if status == 401 {
				if err == nil || calls.Load() != 1 {
					t.Fatal(err, calls.Load())
				}
			} else if err != nil || calls.Load() != 2 {
				t.Fatal(err, calls.Load())
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	p, _ := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "key", Retries: 5, RetryBackoff: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	ctx = withModelObserver(ctx, func(state string) {
		if state == "model_retry" {
			cancel()
		}
	})
	_, err := p.Complete(ctx, ModelRequest{})
	if err != context.Canceled {
		t.Fatal(err)
	}
}
func TestModelRequestTimeoutRetriesWithoutTools(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			timer := time.NewTimer(100 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
			case <-timer.C:
			}
			return
		}
		io.WriteString(w, modelOK)
	}))
	defer server.Close()
	p, _ := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "key", Timeout: 20 * time.Millisecond, Retries: 1, RetryBackoff: time.Millisecond})
	if _, err := p.Complete(context.Background(), ModelRequest{}); err != nil || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
}
func TestProvidersShareConcurrencyGateAndWaitCancellation(t *testing.T) {
	var active, max atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := max.Load(); n > old && !max.CompareAndSwap(old, n); old = max.Load() {
		}
		entered <- struct{}{}
		select {
		case <-release:
			io.WriteString(w, modelOK)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	p, _ := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "same-key", MaxConcurrent: 2})
	q, _ := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "same-key", MaxConcurrent: 2})
	var wg sync.WaitGroup
	for _, provider := range []*OpenAIProvider{p, q} {
		wg.Add(1)
		go func(p *OpenAIProvider) {
			defer wg.Done()
			if _, err := p.Complete(context.Background(), ModelRequest{}); err != nil {
				t.Error(err)
			}
		}(provider)
	}
	<-entered
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	ctx = withModelObserver(ctx, func(state string) {
		if state == "model_wait" {
			cancel()
		}
	})
	if _, err := q.Complete(ctx, ModelRequest{}); err != context.Canceled {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if max.Load() != 2 {
		t.Fatal(max.Load())
	}
}
func TestUnlimitedRuntimePassesOldModelAndToolCaps(t *testing.T) {
	repo := newFakeRepo()
	a := fixtureAgent("unlimited")
	a.Limits = Limits{Unlimited: true, ModelCalls: 1, ToolCalls: 1, RunTimeout: time.Nanosecond}
	a.EnabledTools = []string{"noop"}
	repo.agents[a.ID] = a
	tools := NewToolRegistry()
	if err := tools.Register(ToolSpec{Name: "noop", Description: "No-op regression tool", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"string"}`)}, func(context.Context, ExecutionContext, json.RawMessage) (any, error) { return "ok", nil }); err != nil {
		t.Fatal(err)
	}
	count := 0
	runtime := NewRuntime(repo, tools, map[string]ModelProvider{"mock": modelFunc(func(ctx context.Context, r ModelRequest) (ModelResponse, error) {
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("unexpected unlimited run deadline")
		}
		count++
		if count <= 40 {
			return calls(call(fmt.Sprint(count), "noop", `{}`)), nil
		}
		return final("done"), nil
	})})
	result, err := runtime.Run(context.Background(), "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: json.RawMessage(`{}`)})
	if err != nil || result.Output != "done" || count != 41 {
		t.Fatal(result, err, count)
	}
}
func TestUnlimitedNestedToolUsesParentCancellation(t *testing.T) {
	tools := NewToolRegistry()
	var deadline bool
	if err := tools.Register(ToolSpec{Name: "nested", Description: "Nested context regression tool", UseRunTimeout: true, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"string"}`)}, func(ctx context.Context, _ ExecutionContext, _ json.RawMessage) (any, error) {
		_, deadline = ctx.Deadline()
		return "ok", nil
	}); err != nil {
		t.Fatal(err)
	}
	e := toolContext([]string{"nested"}, nil)
	e.Limits = Limits{Unlimited: true}.Defaults()
	e.budget = &budget{unlimited: true}
	if result := tools.Execute(context.Background(), e, "nested", json.RawMessage(`{}`)); !result.OK || deadline {
		t.Fatal(result, deadline)
	}
}
