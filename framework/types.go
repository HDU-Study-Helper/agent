// Package framework implements a bounded, business-independent in-process agent runtime.
package framework

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string         { return e.Code + ": " + e.Message }
func Fail(code, message string) *Error { return &Error{Code: code, Message: message} }
func SafeError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Fail("TIMEOUT", "operation cancelled or timed out")
	}
	return Fail("INTERNAL_ERROR", "operation failed")
}
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("cryptographic randomness unavailable")
	}
	return hex.EncodeToString(b[:])
}

type ModelConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Credential and endpoint selection stays in trusted provider configuration.
	Temperature *float64                   `json:"temperature,omitempty"`
	MaxTokens   int                        `json:"max_tokens,omitempty"`
	Parameters  map[string]json.RawMessage `json:"parameters,omitempty"`
}
type Limits struct {
	ModelCalls      int           `json:"model_calls"`
	ToolCalls       int           `json:"tool_calls"`
	DispatchDepth   int           `json:"dispatch_depth"`
	ContextBytes    int           `json:"context_bytes"`
	MemoryBytes     int           `json:"memory_bytes"`
	ToolResultBytes int           `json:"tool_result_bytes"`
	RunTimeout      time.Duration `json:"run_timeout"`
	ToolTimeout     time.Duration `json:"tool_timeout"`
	MaxConcurrent   int           `json:"max_concurrent"`
}

func (l Limits) Defaults() Limits {
	if l.ModelCalls <= 0 {
		l.ModelCalls = 8
	}
	if l.ToolCalls <= 0 {
		l.ToolCalls = 32
	}
	if l.DispatchDepth <= 0 {
		l.DispatchDepth = 4
	}
	if l.ContextBytes <= 0 {
		l.ContextBytes = 128 << 10
	}
	if l.MemoryBytes <= 0 {
		l.MemoryBytes = 16 << 10
	}
	if l.ToolResultBytes <= 0 {
		l.ToolResultBytes = 32 << 10
	}
	if l.RunTimeout <= 0 {
		l.RunTimeout = 2 * time.Minute
	}
	if l.ToolTimeout <= 0 {
		l.ToolTimeout = 20 * time.Second
	}
	if l.MaxConcurrent <= 0 {
		l.MaxConcurrent = 4
	}
	return l
}

type AgentDefinition struct {
	Name         string      `json:"name"`
	Type         string      `json:"agent_type"`
	SystemPrompt string      `json:"system_prompt"`
	Model        ModelConfig `json:"model_config"`
	EnabledTools []string    `json:"enabled_tools"`
	Permissions  []string    `json:"permissions"`
	Limits       Limits      `json:"limits"`
}
type AgentInstance struct {
	ID      string `json:"agent_id"`
	OwnerID string `json:"owner_user_id"`
	AgentDefinition
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Scope struct {
	OwnerID string `json:"owner_user_id"`
	AgentID string `json:"agent_id"`
}
type Trigger string

const (
	UserMessage   Trigger = "USER_MESSAGE"
	DataChanged   Trigger = "DATA_CHANGED"
	AgentDispatch Trigger = "AGENT_DISPATCH"
	WorkflowEvent Trigger = "WORKFLOW_EVENT"
	SystemEvent   Trigger = "SYSTEM_EVENT"
)

type RunRequest struct {
	AgentID string          `json:"agent_id"`
	Trigger Trigger         `json:"trigger"`
	Input   json.RawMessage `json:"input"`
}
type RunRecord struct {
	ID           string     `json:"run_id"`
	OwnerID      string     `json:"owner_user_id"`
	AgentID      string     `json:"agent_id"`
	ParentRunID  string     `json:"parent_run_id,omitempty"`
	DispatcherID string     `json:"dispatcher_agent_id,omitempty"`
	Trigger      Trigger    `json:"trigger"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	DurationMS   int64      `json:"duration_ms"`
	ModelCalls   int        `json:"model_calls"`
	ToolCalls    int        `json:"tool_calls"`
	Usage        Usage      `json:"usage"`
	Error        *Error     `json:"error"`
	StopReason   string     `json:"stop_reason"`
}
type RunResult struct {
	RunID  string `json:"run_id"`
	Output string `json:"output"`
	Error  *Error `json:"error"`
}
type Memory struct {
	ID        string    `json:"memory_id"`
	OwnerID   string    `json:"owner_user_id"`
	AgentID   string    `json:"agent_id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Category  string    `json:"category"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Workflow struct {
	ID          string          `json:"workflow_id"`
	OwnerID     string          `json:"owner_user_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ParentID    string          `json:"parent_workflow_id,omitempty"`
	Definition  json.RawMessage `json:"definition"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	CreatedBy   string          `json:"created_by_agent_id"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// Repository implementations must enforce scope, active owners, atomic version checks and parent ownership.
// All PostgreSQL implementation is supplied by the host dbbridge, never by this module.
type Repository interface {
	PutAgent(context.Context, AgentInstance) (AgentInstance, error)
	GetAgent(context.Context, Scope) (AgentInstance, error)
	ListAgents(context.Context, string, int, string) ([]AgentInstance, error)
	DeleteAgent(context.Context, Scope) error
	SaveMemory(context.Context, Scope, Memory, bool) (Memory, error)
	GetMemory(context.Context, Scope, string) (Memory, error)
	ListMemory(context.Context, Scope, string, int) ([]Memory, error)
	DeleteMemory(context.Context, Scope, string) error
	SaveWorkflow(context.Context, Scope, Workflow, bool) (Workflow, error)
	GetWorkflow(context.Context, string, string) (Workflow, error)
	ListWorkflows(context.Context, string, int, string) ([]Workflow, error)
	WorkflowVersions(context.Context, string, string) ([]Workflow, error)
	DeleteWorkflow(context.Context, string, string) error
	SaveRun(context.Context, RunRecord) error
	GetRun(context.Context, string, string) (RunRecord, error)
}
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
type ModelRequest struct {
	Config   ModelConfig
	Messages []Message
	Tools    []ToolSpec
}
type ModelResponse struct {
	Message      Message
	Usage        Usage
	FinishReason string
}
type ModelProvider interface {
	Complete(context.Context, ModelRequest) (ModelResponse, error)
}
type Runner interface {
	Run(context.Context, string, RunRequest) (RunResult, error)
}
type Dispatcher interface {
	Dispatch(context.Context, ExecutionContext, RunRequest) (RunResult, error)
}

// budget is shared by every descendant and cannot be supplied by tool arguments.
type budget struct {
	mu            sync.Mutex
	models, tools int
}

func (b *budget) take(model bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := &b.tools
	if model {
		p = &b.models
	}
	if *p <= 0 {
		return Fail("EXECUTION_LIMIT_EXCEEDED", "shared execution budget exhausted")
	}
	*p--
	return nil
}

type ExecutionContext struct {
	Scope        Scope
	RunID        string
	ParentRunID  string
	DispatcherID string
	Permissions  []string
	EnabledTools []string
	Limits       Limits
	ancestry     []string
	budget       *budget
}

func (e ExecutionContext) Has(permission string) bool {
	for _, p := range e.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}
func contains(values []string, v string) bool {
	for _, s := range values {
		if s == v {
			return true
		}
	}
	return false
}

// AsyncRunner is reserved for a host implementation backed by its durable Job/Event service.
// No in-memory queue or fake successful submission is supplied in this phase.
type AsyncRunner interface {
	Submit(context.Context, string, RunRequest) (string, error)
	Result(context.Context, string, string) (RunResult, error)
}

// WorkflowExecutor is separate from definition persistence and has no engine in this phase.
type WorkflowExecutor interface {
	Execute(context.Context, Scope, string, json.RawMessage) (RunResult, error)
}
type UnsupportedWorkflowExecutor struct{}

func (UnsupportedWorkflowExecutor) Execute(context.Context, Scope, string, json.RawMessage) (RunResult, error) {
	return RunResult{}, Fail("NOT_IMPLEMENTED", "workflow execution engine is not implemented")
}
