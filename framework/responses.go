package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

// completeResponses replays only this run's wire items. It deliberately does
// not use previous_response_id or ask the provider to store the conversation.
func (p *OpenAIProvider) completeResponses(ctx context.Context, r ModelRequest) (ModelResponse, error) {
	input := make([]any, 0, len(r.Messages))
	for _, m := range r.Messages {
		switch m.Role {
		case "system", "developer", "user", "assistant":
			if m.Role == "assistant" && len(m.responsesItems) > 0 {
				for _, item := range m.responsesItems {
					input = append(input, item)
				}
				continue
			}
			if len(m.ToolCalls) > 0 && m.Role != "assistant" {
				return ModelResponse{}, Fail("INVALID_ARGUMENT", "invalid Responses input tool call role")
			}
			if m.Content != "" || len(m.ToolCalls) == 0 {
				input = append(input, map[string]any{"role": m.Role, "content": m.Content})
			}
			for _, call := range m.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
			}
		case "tool":
			if m.ToolCallID == "" {
				return ModelResponse{}, Fail("INVALID_ARGUMENT", "Responses tool output requires a call ID")
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content})
		default:
			return ModelResponse{}, Fail("INVALID_ARGUMENT", "unsupported Responses message role")
		}
	}
	payload := map[string]any{"model": r.Config.Model, "input": input, "store": false, "stream": false, "include": []string{"reasoning.encrypted_content"}}
	if r.Config.Temperature != nil {
		payload["temperature"] = *r.Config.Temperature
	}
	if r.Config.MaxTokens > 0 {
		payload["max_output_tokens"] = r.Config.MaxTokens
	}
	for key, value := range r.Config.Parameters {
		switch key {
		case "reasoning_effort":
			effort := value
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				effort = json.RawMessage(`"none"`)
			}
			payload["reasoning"] = map[string]any{"effort": effort}
		case "top_p":
			payload[key] = value
		case "max_tokens":
			if r.Config.MaxTokens == 0 {
				payload["max_output_tokens"] = value
			}
		default:
			return ModelResponse{}, Fail("INVALID_ARGUMENT", "model parameter not supported by Responses: "+key)
		}
	}
	if len(r.Tools) > 0 {
		tools := make([]any, 0, len(r.Tools))
		for _, t := range ModelFacingTools(r.Tools) {
			tools = append(tools, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.InputSchema, "strict": false})
		}
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	var response struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
		Usage  struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
			Total  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := p.request(ctx, "/responses", payload, &response); err != nil {
		return ModelResponse{}, err
	}
	// An incomplete/failed response must never cause even a partial tool write.
	if response.Status != "completed" {
		return ModelResponse{}, Fail("PROVIDER_ERROR", "Responses provider did not complete the response")
	}
	m := Message{Role: "assistant"}
	var text strings.Builder
	seen := map[string]bool{}
	for _, raw := range response.Output {
		var item struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			Status    string `json:"status"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &item) != nil || (item.Status != "" && item.Status != "completed") {
			return ModelResponse{}, Fail("PROVIDER_ERROR", "invalid or incomplete Responses output item")
		}
		switch item.Type {
		case "reasoning":
			// Opaque provider items are only forwarded to the same provider in
			// this run. They are not exposed as assistant content or persisted.
		case "message":
			if item.Role != "assistant" {
				return ModelResponse{}, Fail("PROVIDER_ERROR", "invalid Responses output role")
			}
			for _, part := range item.Content {
				if part.Type == "refusal" {
					return ModelResponse{}, Fail("PROVIDER_ERROR", "Responses provider declined the response")
				}
				if part.Type == "output_text" {
					text.WriteString(part.Text)
				}
			}
		case "function_call":
			if len(r.Tools) == 0 || item.CallID == "" || item.Name == "" || seen[item.CallID] {
				return ModelResponse{}, Fail("PROVIDER_ERROR", "invalid Responses function call")
			}
			seen[item.CallID] = true
			m.ToolCalls = append(m.ToolCalls, ToolCall{ID: item.CallID, Type: "function", Function: FunctionCall{Name: item.Name, Arguments: item.Arguments}})
		default:
			return ModelResponse{}, Fail("PROVIDER_ERROR", "unsupported Responses output item")
		}
		m.responsesItems = append(m.responsesItems, raw)
	}
	m.Content = text.String()
	finish := "stop"
	if len(m.ToolCalls) > 0 {
		finish = "tool_calls"
	} else if strings.TrimSpace(m.Content) == "" {
		return ModelResponse{}, Fail("PROVIDER_ERROR", "Responses provider returned no assistant text")
	}
	return ModelResponse{Message: m, FinishReason: finish, Usage: Usage{PromptTokens: response.Usage.Input, CompletionTokens: response.Usage.Output, TotalTokens: response.Usage.Total}}, nil
}
