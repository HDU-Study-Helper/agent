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
	if errors.Is(err, context.DeadlineExceeded) {
		return Fail("TIMEOUT", "operation deadline exceeded")
	}
	if errors.Is(err, context.Canceled) {
		return Fail("CANCELLED", "operation cancelled by caller")
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
	// Mode default inherits the host provider model; custom pins Model. Empty preserves legacy host semantics.
	Mode     string `json:"mode,omitempty"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Credential and endpoint selection stays in trusted provider configuration.
	Temperature *float64                   `json:"temperature,omitempty"`
	MaxTokens   int                        `json:"max_tokens,omitempty"`
	Parameters  map[string]json.RawMessage `json:"parameters,omitempty"`
}
type Limits struct {
	// Unlimited removes model/tool count and total run deadlines; cancellation and scope guards remain.
	Unlimited       bool          `json:"unlimited,omitempty"`
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
	if l.Unlimited {
		l.ModelCalls, l.ToolCalls, l.RunTimeout = 0, 0, 0
	}
	if !l.Unlimited && l.ModelCalls <= 0 {
		l.ModelCalls = 8
	}
	if !l.Unlimited && l.ToolCalls <= 0 {
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
	if !l.Unlimited && l.RunTimeout <= 0 {
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
	Name string `json:"name"`
	// Type is descriptive host metadata; Runtime never selects business policy by role.
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
	// WorkflowID is bound by trusted host context or a validated dispatch handler,
	// never copied directly from model/run JSON.
	WorkflowID string `json:"-"`
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
	// DisableTools is a trusted host-only restriction for output correction runs.
	// It cannot be enabled through run JSON or model-provided input.
	DisableTools bool `json:"-"`
}

// ToolResultRef identifies an actual, schema-validated persisted tool result.
// It carries no tool arguments, credentials or returned private content.
type ToolResultRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ToolExecution stores diagnostics and safe object references, not private data.
type ToolExecution struct {
	Name       string          `json:"name"`
	DurationMS int64           `json:"duration_ms"`
	Error      *Error          `json:"error,omitempty"`
	ResultRefs []ToolResultRef `json:"result_refs,omitempty"`
}

// OutputCorrection records a rejected final response without storing private text.
type OutputCorrection struct {
	Attempt int                  `json:"attempt"`
	Kind    OutputValidationKind `json:"kind"`
	Code    string               `json:"code"`
}

type RunRecord struct {
	ID                string             `json:"run_id"`
	OwnerID           string             `json:"owner_user_id"`
	AgentID           string             `json:"agent_id"`
	ParentRunID       string             `json:"parent_run_id,omitempty"`
	WorkflowID        string             `json:"workflow_id,omitempty"`
	WorkflowVersion   int                `json:"workflow_version,omitempty"`
	WorkflowSnapshot  *Workflow          `json:"workflow_snapshot,omitempty"`
	DispatcherID      string             `json:"dispatcher_agent_id,omitempty"`
	Trigger           Trigger            `json:"trigger"`
	Status            string             `json:"status"`
	StartedAt         time.Time          `json:"started_at"`
	FinishedAt        *time.Time         `json:"finished_at,omitempty"`
	DurationMS        int64              `json:"duration_ms"`
	ModelCalls        int                `json:"model_calls"`
	ToolCalls         int                `json:"tool_calls"`
	ToolExecutions    []ToolExecution    `json:"tool_executions,omitempty"`
	OutputCorrections []OutputCorrection `json:"output_corrections,omitempty"`
	Usage             Usage              `json:"usage"`
	Error             *Error             `json:"error"`
	StopReason        string             `json:"stop_reason"`
}
type RunResult struct {
	RunID  string `json:"run_id"`
	Output string `json:"output"`
	Error  *Error `json:"error"`
}
type Memory struct {
	ID                string          `json:"memory_id"`
	OwnerID           string          `json:"owner_user_id"`
	Visibility        string          `json:"visibility"`
	AgentID           string          `json:"owner_agent_id,omitempty"`
	WorkflowID        string          `json:"owner_workflow_id,omitempty"`
	MemoryType        string          `json:"memory_type"`
	IsPersonal        bool            `json:"is_personal"`
	Level             string          `json:"level"`
	ParentID          string          `json:"parent_memory_id,omitempty"`
	Title             string          `json:"title"`
	Content           string          `json:"content"`
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
	SourceRefs        json.RawMessage `json:"source_refs,omitempty"`
	ValidFrom         *time.Time      `json:"valid_from,omitempty"`
	ValidUntil        *time.Time      `json:"valid_until,omitempty"`
	Status            string          `json:"status"`
	Category          string          `json:"category"` // retained legacy metadata
	Priority          int             `json:"priority"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}
type MemorySearch struct {
	Query      string `json:"query"`
	Visibility string `json:"visibility,omitempty"`
	AgentID    string `json:"owner_agent_id,omitempty"`
	WorkflowID string `json:"owner_workflow_id,omitempty"`
	MemoryType string `json:"memory_type,omitempty"`
	IsPersonal *bool  `json:"is_personal,omitempty"`
	Level      string `json:"level,omitempty"`
	Status     string `json:"status,omitempty"`
	ParentID   string `json:"parent_memory_id,omitempty"`
	Validity   string `json:"validity,omitempty"` // current (default), expired, future, all
	Limit      int    `json:"limit"`
}
type MemoryRelation struct {
	ID          string    `json:"relation_id"`
	OwnerID     string    `json:"owner_user_id"`
	SourceID    string    `json:"source_memory_id"`
	TargetID    string    `json:"target_memory_id"`
	Type        string    `json:"relation_type"`
	Description string    `json:"description"`
	CreatedBy   string    `json:"created_by_agent_id"`
	CreatedAt   time.Time `json:"created_at"`
}
type MemoryRelationQuery struct {
	MemoryID  string `json:"memory_id"`
	Type      string `json:"relation_type,omitempty"`
	Direction string `json:"direction,omitempty"` // outgoing, incoming, both
	Limit     int    `json:"limit"`
	Depth     int    `json:"depth,omitempty"` // only 1 supported
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

// WorkflowProfileRepository is an optional atomic creation capability. Implementations
// must authorize both workflow.write and memory.write for the same active agent,
// derive private memory ownership from the newly inserted workflow, and roll back
// the workflow, version and all memories together on any failure. This capability
// never grants the caller a Workflow scope for subsequent memory operations.
// A failed/uncertain response must be reconciled with workflow_list/get before retrying.
type WorkflowProfileRepository interface {
	CreateWorkflowWithMemories(context.Context, Scope, Workflow, []Memory) (WorkflowProfileCreation, error)
}

// WorkflowProfileReceipt contains persisted IDs, never copies of private contents.
type WorkflowProfileReceipt struct {
	MemoryIDs       []string `json:"memory_ids"`
	Count           int      `json:"count"`
	Visibility      string   `json:"visibility"`
	OwnerWorkflowID string   `json:"owner_workflow_id"`
	Verified        bool     `json:"verified"`
	ContentSHA256   string   `json:"content_sha256"` // canonical JSON of persisted memories, in seed order
}

type WorkflowProfileCreation struct {
	Workflow
	ProfileInitialization WorkflowProfileReceipt `json:"profile_initialization"`
}

// Bootstrap resource limits bound writes without limiting the number of facts
// recorded inside a profile memory. Ordinary execution input budgets also apply.
const MaxWorkflowPrivateMemories = 256
const MaxWorkflowPrivateMemoryBytes = 1 << 20

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
	SearchMemory(context.Context, Scope, MemorySearch) ([]Memory, error)
	CreateMemoryRelation(context.Context, Scope, MemoryRelation) (MemoryRelation, error)
	GetMemoryRelations(context.Context, Scope, MemoryRelationQuery) ([]MemoryRelation, error)
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
	// Responses items are transient wire history, not part of the logical transcript.
	responsesItems []json.RawMessage
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
	unlimited     bool
}

func (b *budget) take(model bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.unlimited {
		return nil
	}
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
	Scope            Scope
	RunID            string
	ParentRunID      string
	DispatcherID     string
	Permissions      []string
	EnabledTools     []string
	Limits           Limits
	ancestry         []string
	budget           *budget
	workflowSnapshot *Workflow
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

// ModelProviderResolver lets a trusted host atomically replace named provider configurations.
// It is consulted when a name is absent from the immutable Providers map.
type ModelProviderResolver interface{ ResolveProvider(string) ModelProvider }

// ModelConfigResolver optionally resolves provider and effective model from one host configuration snapshot.
// Runtime keeps the resolved configuration fixed for the duration of a run.
type ModelConfigResolver interface {
	ResolveModel(ModelConfig) (ModelProvider, ModelConfig, error)
}
