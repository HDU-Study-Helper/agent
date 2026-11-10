package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type ToolSpec struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"parameters"`
	OutputSchema json.RawMessage `json:"output_schema"`
	Permissions  []string        `json:"permissions"`
	Timeout      time.Duration   `json:"timeout"`
	// UseRunTimeout allows nested runs to use the remaining run budget.
	UseRunTimeout bool `json:"use_run_timeout,omitempty"`
}
type ToolResult struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data"`
	Error *Error `json:"error"`
}
type ToolHandler func(context.Context, ExecutionContext, json.RawMessage) (any, error)
type registeredTool struct {
	spec          ToolSpec
	handler       ToolHandler
	input, output *jsonschema.Schema
}
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]registeredTool
}

func NewToolRegistry() *ToolRegistry { return &ToolRegistry{tools: map[string]registeredTool{}} }
func compileSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	// A closed loader permits local references but never downloads external schemas.
	c.UseLoader(closedSchemaLoader{})

	if err := c.AddResource("urn:tool:schema", value); err != nil {
		return nil, err
	}
	return c.Compile("urn:tool:schema")
}
func (r *ToolRegistry) Register(spec ToolSpec, handler ToolHandler) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(spec.Name) || handler == nil || spec.Description == "" {
		return Fail("INVALID_ARGUMENT", "invalid tool registration")
	}
	in, err := compileSchema(spec.InputSchema)
	if err != nil {
		return Fail("INVALID_ARGUMENT", "invalid tool input schema")
	}
	out, err := compileSchema(spec.OutputSchema)
	if err != nil {
		return Fail("INVALID_ARGUMENT", "invalid tool output schema")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[spec.Name]; ok {
		return Fail("INVALID_ARGUMENT", "duplicate tool name")
	}
	spec.InputSchema = append(json.RawMessage(nil), spec.InputSchema...)
	spec.OutputSchema = append(json.RawMessage(nil), spec.OutputSchema...)
	spec.Permissions = append([]string(nil), spec.Permissions...)
	r.tools[spec.Name] = registeredTool{spec, handler, in, out}
	return nil
}
func allowed(e ExecutionContext, s ToolSpec) bool {
	if !contains(e.EnabledTools, s.Name) {
		return false
	}
	for _, p := range s.Permissions {
		if !e.Has(p) {
			return false
		}
	}
	return true
}
func (r *ToolRegistry) Authorized(e ExecutionContext) []ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []ToolSpec{}
	for _, t := range r.tools {
		if allowed(e, t.spec) {
			s := t.spec
			s.InputSchema = append(json.RawMessage(nil), s.InputSchema...)
			s.OutputSchema = append(json.RawMessage(nil), s.OutputSchema...)
			s.Permissions = append([]string(nil), s.Permissions...)
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (r *ToolRegistry) Execute(ctx context.Context, e ExecutionContext, name string, args json.RawMessage) (result ToolResult) {
	defer func() {
		if recover() != nil {
			result = ToolResult{Error: Fail("INTERNAL_ERROR", "tool execution failed")}
		}
	}()
	fail := func(err error) ToolResult { return ToolResult{Error: SafeError(err)} }
	if e.budget == nil {
		return fail(Fail("PERMISSION_DENIED", "runtime execution context required"))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	// Failed, unknown and unauthorized attempts consume the shared tool budget too.
	if err := e.budget.take(false); err != nil {
		return fail(err)
	}
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return fail(Fail("TOOL_NOT_FOUND", "tool is not registered"))
	}
	if !allowed(e, t.spec) {
		return fail(Fail("PERMISSION_DENIED", "tool is not authorized"))
	}
	if len(args) > e.Limits.ContextBytes {
		return fail(Fail("INVALID_ARGUMENT", "tool input exceeds budget"))
	}
	var input any
	if err := json.Unmarshal(args, &input); err != nil {
		return fail(Fail("INVALID_ARGUMENT", "invalid JSON arguments"))
	}
	if err := t.input.Validate(input); err != nil {
		return fail(Fail("INVALID_ARGUMENT", inputSchemaError(err, t.spec.InputSchema)))
	}
	timeout := e.Limits.ToolTimeout
	if t.spec.UseRunTimeout {
		timeout = e.Limits.RunTimeout
	}
	if t.spec.Timeout > 0 && (timeout <= 0 || t.spec.Timeout < timeout) {
		timeout = t.spec.Timeout
	}
	// Nested agents inherit the remaining parent context. Unlimited runs must not
	// become an immediate zero-duration deadline or fall back to ToolTimeout.
	callCtx, cancel := context.WithCancel(ctx)
	if timeout > 0 {
		cancel()
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	data, err := t.handler(callCtx, e, args)
	if err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			return fail(Fail("TIMEOUT", fmt.Sprintf("tool %s exceeded its %s deadline", name, timeout)))
		}
		return fail(err)
	}
	if err = callCtx.Err(); err != nil {
		return fail(err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fail(err)
	}
	if len(raw) > e.Limits.ToolResultBytes && name == "agent_dispatch" {
		if compact, ok := compactDispatchData(data, e.Limits.ToolResultBytes); ok {
			data = compact
			raw, err = json.Marshal(data)
			if err != nil {
				return fail(err)
			}
		}
	}
	if len(raw) > e.Limits.ToolResultBytes {
		return fail(Fail("EXECUTION_LIMIT_EXCEEDED", "tool result exceeds budget"))
	}
	var output any
	if err = json.Unmarshal(raw, &output); err != nil {
		return fail(err)
	}
	if err = t.output.Validate(output); err != nil {
		return fail(Fail("INTERNAL_ERROR", "tool output does not match contract"))
	}
	return ToolResult{OK: true, Data: output}
}

// Catalog is intended for trusted host-side discovery and documentation.
// Runtime always uses Authorized instead, so ungranted tools are not sent to models.
func (r *ToolRegistry) Catalog() []ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []ToolSpec{}
	for _, t := range r.tools {
		s := t.spec
		s.InputSchema = append(json.RawMessage(nil), s.InputSchema...)
		s.OutputSchema = append(json.RawMessage(nil), s.OutputSchema...)
		s.Permissions = append([]string(nil), s.Permissions...)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type closedSchemaLoader struct{}

func (closedSchemaLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external schemas are forbidden")
}
