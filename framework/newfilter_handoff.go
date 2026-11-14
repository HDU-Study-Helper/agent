package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// filterHandoff validates only the item-processing contract, never arbitrary
// Filter conversations. Corrections reuse the current model, tools and budget.
type filterHandoff struct {
	itemID      string
	corrections int
	reminded    bool
	missingWF   bool
	course      bool
}

func newFilterHandoff(ctx context.Context, a AgentInstance, e ExecutionContext, req RunRequest) filterHandoff {
	var guard filterHandoff
	if a.Type != "filter" {
		return guard
	}
	var input struct {
		ItemID   string `json:"item_id"`
		Contract string `json:"processing_contract"`
		Original struct {
			ItemID   string `json:"item_id"`
			Contract string `json:"processing_contract"`
		} `json:"original_input"`
	}
	if json.Unmarshal(req.Input, &input) != nil {
		return guard
	}
	if req.DisableTools && input.Contract == "" {
		input.Contract = input.Original.Contract
	}
	if input.ItemID == "" || input.Contract == "" {
		return guard
	}
	guard.itemID = input.ItemID
	// Only in-process, owner-checked evidence can establish the routing hint.
	// Model input and business text cannot assert host completeness.
	if req.DisableTools || !e.Has("agent.dispatch") || !e.Has("workflow.read") || !e.Has("data.read") || !contains(e.EnabledTools, "agent_dispatch") || !contains(e.EnabledTools, "workflow_get") || !contains(e.EnabledTools, "data_query") {
		return guard
	}
	snapshot, ok := ctx.Value(hostEvidenceKey{}).(hostEvidence)
	if !ok || snapshot.owner != e.Scope.OwnerID {
		return guard
	}
	var data struct {
		Entity struct{ Kind, ID string } `json:"entity"`
	}
	var workflows struct {
		Complete bool              `json:"complete"`
		EntityID string            `json:"entity_id"`
		Items    []json.RawMessage `json:"items"`
	}
	if json.Unmarshal(snapshot.data, &data) == nil && json.Unmarshal(snapshot.workflows, &workflows) == nil {
		guard.course = data.Entity.Kind == "course" && data.Entity.ID != "" && workflows.EntityID == data.Entity.ID && workflows.Complete && workflows.Items != nil
		guard.missingWF = guard.course && len(workflows.Items) == 0
	}
	return guard
}

type filterItemOutput struct {
	ID        string            `json:"item_id"`
	ParentID  string            `json:"parent_item_id"`
	Title     string            `json:"title"`
	Status    string            `json:"status"`
	Reason    string            `json:"reason"`
	Evidence  []json.RawMessage `json:"evidence_refs"`
	Artifacts []json.RawMessage `json:"artifacts"`
	Routes    []struct {
		ItemID     string `json:"item_id"`
		Target     string `json:"target"`
		WorkflowID string `json:"workflow_id"`
		Execution  *struct {
			RunID string          `json:"run_id"`
			Error json.RawMessage `json:"error"`
		} `json:"execution"`
	} `json:"routes"`
}

func (g *filterHandoff) validate(output string, executions []ToolExecution) (string, error) {
	if g.itemID == "" {
		return "", nil
	}
	var result struct {
		Items []filterItemOutput `json:"items"`
	}
	problem := ""
	if json.Unmarshal([]byte(output), &result) != nil || len(result.Items) == 0 {
		problem = "最终输出必须是单个JSON对象且包含非空items数组，不能使用Markdown、代码围栏或总结文章。"
	} else {
		seen := map[string]bool{}
		for _, item := range result.Items {
			validStatus := item.Status == "COMPLETED" || item.Status == "IGNORED" || item.Status == "PARTIAL" || item.Status == "NEEDS_USER_INPUT" || item.Status == "FAILED"
			if item.ID == "" || seen[item.ID] || !validStatus || strings.TrimSpace(item.Title) == "" || item.Evidence == nil || item.Artifacts == nil || item.Routes == nil || strings.TrimSpace(item.Reason) == "" || (item.ID != g.itemID && (item.ParentID != g.itemID || !strings.HasPrefix(item.ID, g.itemID+":"))) || (item.ID == g.itemID && item.ParentID != "" && item.ParentID != g.itemID) {
				problem = "每项必须有原始item_id或其:1/:2子项ID、正确parent_item_id、非空title、合法status、非空reason，以及evidence_refs/routes/artifacts数组（可为空数组）。"
				break
			}
			seen[item.ID] = true
			for _, route := range item.Routes {
				if route.ItemID != item.ID {
					problem = "每条routes.item_id必须与所属事项item_id一致。"
				}
			}
		}
		if len(result.Items) > 1 && seen[g.itemID] {
			problem = "拆分子事项时不能同时返回父事项。"
		}
	}
	if problem == "" && (g.course || g.missingWF) {
		dispatched := false
		for _, execution := range executions {
			if execution.Name == "agent_dispatch" {
				dispatched = true
			}
		}
		needsRoute, claimsCompleted := false, false
		for _, item := range result.Items {
			if !dispatched || len(item.Routes) == 0 {
				needsRoute = needsRoute || item.Status == "COMPLETED" || item.Status == "PARTIAL" || item.Status == "NEEDS_USER_INPUT"
				claimsCompleted = claimsCompleted || item.Status == "COMPLETED"
			}
		}
		if needsRoute {
			if !g.reminded {
				g.reminded = true
				problem = "可信Host已确认课程实体。Filter不替代Learner或Worker：没有本课程流程应agent_dispatch给固定Learner做CREATE评估，不需要再次询问用户；有适用流程应派发带workflow的固定Worker。其他课程流程不匹配只是应创建本课流程，不是停止理由。Filter不能读取Workflow PRI是正常隔离，派发绑定Worker才能读。仍有真实歧义或派发失败才如实NEEDS_USER_INPUT/PARTIAL；不得虚构路由或完成。"
			} else if claimsCompleted {
				return "", Fail("INCOMPLETE_FILTER_HANDOFF", "Filter仍声称COMPLETED，但没有实际路由；必须报告未完成原因")
			}
		}
	}
	if problem == "" {
		return "", nil
	}
	if g.corrections >= 2 {
		return "", Fail("INVALID_ITEM_RESULT", "Filter在两次纠正后仍未满足items输出契约")
	}
	g.corrections++
	example, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"item_id": g.itemID, "parent_item_id": nil, "title": "事项标题", "status": "PARTIAL", "reason": "如实说明实际进展及未完成原因", "evidence_refs": []any{}, "routes": []any{}, "artifacts": []any{}}}})
	return fmt.Sprintf("Host Filter final validation: %s 保留所有工具结果及真实执行ID，所有实际子调用必须报告；只修正契约和未完成业务，不重复已成功执行。JSON结构示例（值必须依据真实结果）：%s", problem, example), nil
}
