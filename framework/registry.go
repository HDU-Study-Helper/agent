package framework

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type Registry struct{ Repository Repository }

func (r Registry) Create(ctx context.Context, owner string, d AgentDefinition) (AgentInstance, error) {
	return r.Save(ctx, owner, AgentInstance{ID: NewID(), AgentDefinition: d, Status: "enabled"})
}
func (r Registry) Save(ctx context.Context, owner string, a AgentInstance) (AgentInstance, error) {
	if owner == "" || a.ID == "" || strings.TrimSpace(a.Name) == "" || a.Model.Provider == "" || (a.Model.Model == "" && a.Model.Mode != "default") || (a.Model.Mode != "" && a.Model.Mode != "default" && a.Model.Mode != "custom") || a.SystemPrompt == "" || (a.Status != "enabled" && a.Status != "disabled") {
		return AgentInstance{}, Fail("INVALID_ARGUMENT", "invalid agent configuration")
	}
	if err := ValidateModelParameters(a.Model.Parameters); err != nil {
		return AgentInstance{}, err
	}
	a.OwnerID = owner
	a.Limits = a.Limits.Defaults()
	if len(a.SystemPrompt) > a.Limits.ContextBytes/2 {
		return AgentInstance{}, Fail("INVALID_ARGUMENT", "system prompt exceeds context budget")
	}
	return r.Repository.PutAgent(ctx, a)
}
func LoadMemories(ctx context.Context, repo Repository, scope Scope, max int) (string, error) {
	memories, err := repo.ListMemory(ctx, scope, "", 1000)
	if err != nil {
		return "", err
	}
	// Repository order: priority DESC, created_at ASC, memory_id ASC.
	result := []Memory{}
	size := 2
	for _, m := range memories {
		now := time.Now()
		if (m.Status != "" && m.Status != "active") || (m.ValidFrom != nil && m.ValidFrom.After(now)) || (m.ValidUntil != nil && !m.ValidUntil.After(now)) {
			continue
		}
		encoded, _ := json.Marshal(m)
		if size+len(encoded)+1 > max {
			continue
		}
		size += len(encoded) + 1
		result = append(result, m)
	}
	if len(result) == 0 {
		return "", nil
	}
	encoded, _ := json.Marshal(result)
	return string(encoded), nil
}
func ExecuteWorkflow(context.Context, Workflow) error {
	_, err := (UnsupportedWorkflowExecutor{}).Execute(context.Background(), Scope{}, "", nil)
	return err
}
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}
