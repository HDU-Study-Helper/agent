package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPConfig struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
	Client  *http.Client
	// Model-only request policy; search providers retain their own retry policy.
	MaxConcurrent int
	Retries       int
	RetryBackoff  time.Duration
}

func newHTTP(c HTTPConfig) (*http.Client, string, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, "", Fail("INVALID_ARGUMENT", "invalid provider endpoint")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, "", Fail("INVALID_ARGUMENT", "provider API key required")
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	client := &http.Client{}
	if c.Client != nil {
		*client = *c.Client
	}
	client.Timeout = c.Timeout
	// Provider redirects must never leak bearer credentials to another host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, strings.TrimRight(c.BaseURL, "/"), nil
}
func postJSON(ctx context.Context, client *http.Client, endpoint, key string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return Fail("INVALID_ARGUMENT", "invalid provider request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Fail("PROVIDER_ERROR", "provider request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return &Error{Code: "TIMEOUT", Message: "provider request timed out", Retryable: true}
		}
		return &Error{Code: "PROVIDER_ERROR", Message: "provider connection failed", Retryable: true}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if res.StatusCode == 429 {
			return &Error{Code: "RATE_LIMITED", Message: "provider rate limit exceeded", Retryable: true}
		}
		return &Error{Code: "PROVIDER_ERROR", Message: fmt.Sprintf("provider request rejected (HTTP %d)", res.StatusCode), Retryable: res.StatusCode == 408 || res.StatusCode >= 500}
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return &Error{Code: "TIMEOUT", Message: "provider response body timed out", Retryable: true}
		}
		return Fail("PROVIDER_ERROR", "provider response could not be read")
	}
	if len(raw) > 8<<20 {
		return Fail("PROVIDER_ERROR", "invalid or oversized provider response")
	}
	if err = json.Unmarshal(raw, output); err != nil {
		return Fail("PROVIDER_ERROR", "invalid provider JSON response")
	}
	return nil
}

// OpenAIProvider translates domain messages to Chat Completions; no SDK types escape it.
type OpenAIProvider struct {
	client                 *http.Client
	base, key              string
	gate                   *modelGate
	maxConcurrent, retries int
	backoff                time.Duration
}

func NewOpenAIProvider(c HTTPConfig) (*OpenAIProvider, error) {
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Minute
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 2
	}
	if c.Retries < 0 || c.Retries > 5 || c.MaxConcurrent > 32 {
		return nil, Fail("INVALID_ARGUMENT", "invalid model request policy")
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = time.Second
	}
	client, base, err := newHTTP(c)
	if err != nil {
		return nil, err
	}
	return &OpenAIProvider{client: client, base: base, key: c.APIKey, gate: sharedModelGate(base, c.APIKey), maxConcurrent: c.MaxConcurrent, retries: c.Retries, backoff: c.RetryBackoff}, nil
}
func (p *OpenAIProvider) Complete(ctx context.Context, r ModelRequest) (ModelResponse, error) {
	payload := map[string]any{"model": r.Config.Model, "messages": r.Messages, "stream": false}
	if r.Config.Temperature != nil {
		payload["temperature"] = *r.Config.Temperature
	}
	if r.Config.MaxTokens > 0 {
		payload["max_completion_tokens"] = r.Config.MaxTokens
	}
	// Adapter owns provider-specific parameters. Endpoint, auth, tools and messages cannot be overridden.
	for key, value := range r.Config.Parameters {
		switch key {
		case "top_p", "seed", "presence_penalty", "frequency_penalty", "reasoning_effort", "max_tokens":
			var v any
			if json.Unmarshal(value, &v) != nil {
				return ModelResponse{}, Fail("INVALID_ARGUMENT", "invalid model parameter")
			}
			payload[key] = v
		default:
			return ModelResponse{}, Fail("INVALID_ARGUMENT", "unsupported model parameter")
		}
	}
	if len(r.Tools) > 0 {
		tools := []any{}
		for _, t := range r.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.InputSchema}})
		}
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	var response struct {
		Choices []struct {
			Message struct {
				Role      string     `json:"role"`
				Content   *string    `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			Finish string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := p.request(ctx, payload, &response); err != nil {
		return ModelResponse{}, err
	}
	if len(response.Choices) != 1 {
		return ModelResponse{}, Fail("PROVIDER_ERROR", "provider returned no unique choice")
	}
	choice := response.Choices[0]
	// Some compatible gateways omit role on a complete assistant message.
	// Accept that omission only when the message actually contains a response.
	if choice.Message.Role == "" && (choice.Message.Content != nil || len(choice.Message.ToolCalls) > 0) {
		choice.Message.Role = "assistant"
	}
	if choice.Message.Role != "assistant" {
		if choice.Message.Role == "" {
			return ModelResponse{}, Fail("PROVIDER_ERROR", "provider returned an empty assistant message")
		}
		return ModelResponse{}, Fail("PROVIDER_ERROR", "provider returned a non-assistant message role")
	}
	message := Message{Role: choice.Message.Role, ToolCalls: choice.Message.ToolCalls}
	if choice.Message.Content != nil {
		message.Content = *choice.Message.Content
	}
	return ModelResponse{Message: message, Usage: response.Usage, FinishReason: choice.Finish}, nil
}
