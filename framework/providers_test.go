package framework

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIChatWireProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("provider endpoint or auth")
		}
		var payload map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid JSON")
		}
		if string(payload["model"]) != `"test-model"` || string(payload["max_completion_tokens"]) != "100" {
			t.Error("model config not transmitted")
		}
		var messages []Message
		_ = json.Unmarshal(payload["messages"], &messages)
		if len(messages) != 3 || messages[2].ToolCallID != "id" {
			t.Error("message history not transmitted")
		}
		var tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		}
		_ = json.Unmarshal(payload["tools"], &tools)
		if len(tools) != 1 || tools[0].Type != "function" || tools[0].Function.Name != "test" {
			t.Error("tool schema mismatch")
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"next","type":"function","function":{"name":"test","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`)
	}))
	defer server.Close()
	p, err := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL + "/v1", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := p.Complete(context.Background(), ModelRequest{Config: ModelConfig{Model: "test-model", MaxTokens: 100}, Messages: []Message{{Role: "user", Content: "hi"}, {Role: "assistant", ToolCalls: []ToolCall{call("id", "test", `{}`)}}, {Role: "tool", ToolCallID: "id", Content: `{"ok":true}`}}, Tools: []ToolSpec{{Name: "test", Description: "test", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil || response.Usage.TotalTokens != 25 || response.Message.ToolCalls[0].ID != "next" {
		t.Fatalf("%+v %v", response, err)
	}
}
func TestProviderErrorsNeverLeakCredentials(t *testing.T) {
	for _, status := range []int{400, 401, 402, 408, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"private-key"}}`)
			}))
			defer server.Close()
			p, _ := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "private-key"})
			_, err := p.Complete(context.Background(), ModelRequest{})
			if err == nil || strings.Contains(err.Error(), "private-key") {
				t.Fatal("provider error leaked", err)
			}
		})
	}
}
func TestDeepSeekOfficialSchemaRetryAndBounds(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/tools/search" {
			t.Error("wrong endpoint")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 3 || body["query"] != "public query" || body["count"] != float64(1) || body["need_content"] != true {
			t.Error("unexpected request schema", body)
		}
		if requests == 1 {
			w.WriteHeader(429)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"title": "Source", "url": "https://example.com/article", "content": "一二三四五六七八九十", "summary": "summary"}, map[string]any{"url": "https://example.com/2"}}})
	}))
	defer server.Close()
	p, err := NewDeepSeekProvider(DeepSeekConfig{HTTPConfig: HTTPConfig{BaseURL: server.URL, APIKey: "key"}, Retries: 1, MaxContentChars: 4, SearchInterval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	results, err := p.Search(context.Background(), SearchRequest{Query: "public query", Count: 1})
	if err != nil || len(results) != 1 || results[0].Content != "一二三四" || results[0].URL != "https://example.com/article" || requests != 2 {
		t.Fatalf("%v %v requests=%d", results, err, requests)
	}
	_, err = p.Search(context.Background(), SearchRequest{Query: " ", Count: 1})
	requireCode(t, err, "INVALID_ARGUMENT")
}
func TestFetchRejectsInternalTargetsAndRemoteRedirectGap(t *testing.T) {
	p, _ := NewDeepSeekProvider(DeepSeekConfig{HTTPConfig: HTTPConfig{APIKey: "key"}})
	for _, target := range []string{"http://127.0.0.1/", "http://[::1]/", "http://10.1.2.3/", "http://169.254.169.254/latest/meta-data", "http://100.100.100.200/", "http://[::ffff:127.0.0.1]/", "http://2130706433/", "http://localhost/", "http://192.168.1.1/"} {
		_, err := p.Fetch(context.Background(), target)
		requireCode(t, err, "PERMISSION_DENIED")
	}
	for _, target := range []string{"file:///etc/passwd", "https://user:password@example.com/", "http://example.com:22/"} {
		if _, err := p.Fetch(context.Background(), target); err == nil {
			t.Fatal("unsafe fetch allowed", target)
		}
	}
	// Literal public address avoids external DNS in the default test suite.
	_, err := p.Fetch(context.Background(), "https://93.184.216.34/article")
	requireCode(t, err, "NOT_IMPLEMENTED")
}
func TestProviderRedirectDoesNotLeakKey(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer server.Close()
	p, _ := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "secret"})
	_, err := p.Complete(context.Background(), ModelRequest{})
	if reached || err == nil {
		t.Fatal("followed provider redirect")
	}
}

type testGuard struct{}

func (testGuard) Check(context.Context, string) error     { return nil }
func (testGuard) Preflight(context.Context, string) error { return nil }
func TestDeepSeekFetchOfficialSchemaAndContentLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/fetch" {
			t.Error("wrong fetch endpoint")
		}
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		if len(input) != 1 || input["url"] != "https://example.com/article" {
			t.Error("wrong fetch schema", input)
		}
		_, _ = io.WriteString(w, `{"title":"Article","content":"一二三四五六"}`)
	}))
	defer server.Close()
	p, err := NewDeepSeekProvider(DeepSeekConfig{HTTPConfig: HTTPConfig{BaseURL: server.URL, APIKey: "key"}, MaxContentChars: 3, RemoteFetchSafe: true})
	if err != nil {
		t.Fatal(err)
	}
	p.guard = testGuard{}
	page, err := p.Fetch(context.Background(), "https://example.com/article")
	if err != nil || page.Content != "一二三" || page.URL != "https://example.com/article" {
		t.Fatal(page, err)
	}
}
func TestFetchRedirectsRevalidateTarget(t *testing.T) {
	g := FetchGuard{}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodHead, "http://169.254.169.254/latest/meta-data", nil)
	requireCode(t, g.checkRedirect(req, nil), "PERMISSION_DENIED")
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodHead, "https://93.184.216.34/", nil)
	requireCode(t, g.checkRedirect(req, make([]*http.Request, 5)), "PERMISSION_DENIED")
}

func TestProviderHTTPTimeoutIsStructured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(50 * time.Millisecond):
		}
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "key", Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Complete(context.Background(), ModelRequest{})
	requireCode(t, err, "TIMEOUT")
}

func TestProviderBodyTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "test", Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Complete(context.Background(), ModelRequest{})
	requireCode(t, err, "TIMEOUT")
	if !strings.Contains(SafeError(err).Message, "body") {
		t.Fatal(err)
	}
}

func TestCompatibleGatewayMissingRole(t *testing.T) {
	for _, tc := range []struct {
		message   string
		wantError bool
	}{
		{`{"content":"OK"}`, false},
		{`{"content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"test","arguments":"{}"}}]}`, false},
		{`{}`, true},
		{`{"role":"system","content":"unexpected"}`, true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"choices":[{"message":`+tc.message+`,"finish_reason":"stop"}]}`)
		}))
		provider, _ := NewOpenAIProvider(HTTPConfig{BaseURL: server.URL, APIKey: "test"})
		result, err := provider.Complete(context.Background(), ModelRequest{})
		if tc.wantError {
			requireCode(t, err, "PROVIDER_ERROR")
		} else if err != nil || result.Message.Role != "assistant" {
			t.Fatalf("%+v %v", result, err)
		}
		server.Close()
	}
}

func TestSearchSourceAnchorsAndInvalidEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"url":"invalid"},{"url":"https://example.com/docs#api","title":"Docs"}]}`)
	}))
	defer server.Close()
	provider, err := NewDeepSeekProvider(DeepSeekConfig{HTTPConfig: HTTPConfig{BaseURL: server.URL, APIKey: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	results, err := provider.Search(context.Background(), SearchRequest{Query: "docs", Count: 2})
	if err != nil || len(results) != 1 || results[0].URL != "https://example.com/docs#api" {
		t.Fatalf("%+v %v", results, err)
	}
}
