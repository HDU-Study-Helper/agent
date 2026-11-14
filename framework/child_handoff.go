package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// childHandoff checks business child results, not arbitrary role conversations.
// A correction is format-only: the same run retains its history and audit, but
// cannot execute more tools after presenting a final result.
type childHandoff struct {
	kind, runID, workflowID string
	workflowVersion         int
	corrections             int
}

func newChildHandoff(ctx context.Context, a AgentInstance, e ExecutionContext, req RunRequest) childHandoff {
	g := childHandoff{runID: e.RunID, workflowID: e.Scope.WorkflowID}
	if e.workflowSnapshot != nil {
		g.workflowVersion = e.workflowSnapshot.Version
	}
	if a.Type == "worker" && e.Scope.WorkflowID != "" {
		g.kind = "worker"
		return g
	}
	if a.Type != "learner" {
		return g
	}
	var input struct {
		Task        string `json:"task"`
		RequestType string `json:"request_type"`
		Context     struct {
			RequestType string          `json:"request_type"`
			Entity      json.RawMessage `json:"entity"`
		} `json:"context"`
	}
	if json.Unmarshal(req.Input, &input) != nil {
		return g
	}
	requestType := input.RequestType
	if requestType == "" {
		requestType = input.Context.RequestType
	}
	switch requestType {
	case "CREATE", "UPDATE", "DELETE", "ARCHIVE", "FEEDBACK":
		g.kind = "learner"
		return g
	}
	if strings.TrimSpace(input.Task) == "" {
		return g
	}
	var entity map[string]json.RawMessage
	if json.Unmarshal(input.Context.Entity, &entity) == nil && len(entity) > 0 {
		g.kind = "learner"
		return g
	}
	if snapshot, ok := ctx.Value(hostEvidenceKey{}).(hostEvidence); ok && snapshot.owner == e.Scope.OwnerID {
		var data struct {
			Entity struct{ Kind, ID string } `json:"entity"`
		}
		if json.Unmarshal(snapshot.data, &data) == nil && data.Entity.Kind != "" && data.Entity.ID != "" {
			g.kind = "learner"
		}
	}
	return g
}

func (g *childHandoff) validate(output string, executions []ToolExecution) (string, error) {
	if g.kind == "" {
		return "", nil
	}
	var result map[string]json.RawMessage
	problem := ""
	if json.Unmarshal([]byte(output), &result) != nil || result == nil {
		problem = "最终输出必须是单个JSON对象，不能使用Markdown、代码围栏、前后说明或JSON数组。"
	} else if g.kind == "worker" {
		problem = g.workerProblem(result)
	} else if raw, ok := result["results"]; ok {
		var results []map[string]json.RawMessage
		if json.Unmarshal(raw, &results) != nil || len(results) == 0 {
			problem = "多实体results必须是非空管理结果对象数组。"
		} else {
			for _, item := range results {
				if problem = learnerHandoffProblem(item); problem != "" {
					break
				}
			}
		}
	} else {
		problem = learnerHandoffProblem(result)
	}
	if problem == "" {
		return "", nil
	}
	if g.corrections >= 2 {
		return "", Fail("INVALID_CHILD_RESULT", "child final output still violates its JSON contract after two format-only corrections")
	}
	g.corrections++
	contract := `Learner字段：request_id/item_id/parent_item_id/entity（未知null）、status（APPLIED/NEEDS_INFO/CONFLICT/NO_CHANGE/REJECTED/ERROR）、operation（CREATE/UPDATE/DELETE/NONE）、workflow_id/workflow_version（未知null，版本为整数）、summary、reason、evidence_refs/changes/questions/suggestions数组、verified布尔值。多实体可用非空results数组。已有profile_initialization等真实回执原样保留。`
	if g.kind == "worker" {
		contract = `Worker字段：run_id、workflow_id、workflow_version整数、status（COMPLETED/NEEDS_USER_INPUT/PARTIAL/FAILED）、summary、artifacts/actions/evidence_refs/evolution_feedback数组，以及interaction/collaboration（未知null）。artifacts与actions保留现有契约和真实事实。`
	}
	audit, _ := json.Marshal(executions)
	return fmt.Sprintf("Host child final validation (FORMAT_ONLY): %s %s 只把已有最终答复和现有工具结果整理为契约JSON，禁止执行任何业务、再次调用工具、重建Workflow或重复写入。工具现已禁用。保留真实业务状态、已提交结果、未核验/失败/缺项与全部真实回执；不得为通过格式检查虚构完成、APPLIED、verified或任何ID。run_id只能使用当前可信run_id=%s；当前可信workflow_id=%s，workflow_version=%d（空/0表示未绑定，Learner新建的Workflow使用实际工具结果ID/版本，不得用run_id替代）。现有工具执行审计引用（仅辅助定位；完整结果仍在历史中）：%s", problem, contract, g.runID, g.workflowID, g.workflowVersion, audit), nil
}

func (g *childHandoff) workerProblem(result map[string]json.RawMessage) string {
	if !handoffEnum(result["status"], "COMPLETED", "NEEDS_USER_INPUT", "PARTIAL", "FAILED") || !handoffText(result["summary"]) {
		return "Worker必须有合法status及非空summary。"
	}
	var runID, workflowID string
	var version int
	if json.Unmarshal(result["run_id"], &runID) != nil || runID != g.runID || json.Unmarshal(result["workflow_id"], &workflowID) != nil || workflowID != g.workflowID || json.Unmarshal(result["workflow_version"], &version) != nil || version != g.workflowVersion {
		return "Worker的run_id、workflow_id和整数workflow_version必须与本次可信执行身份完全一致。"
	}
	if !handoffArrays(result, "artifacts", "actions", "evidence_refs", "evolution_feedback") || !handoffObjectOrNull(result["interaction"]) || !handoffObjectOrNull(result["collaboration"]) {
		return "Worker必须包含artifacts/actions/evidence_refs/evolution_feedback数组（可为空），以及interaction/collaboration对象或null。"
	}
	return ""
}

func learnerHandoffProblem(result map[string]json.RawMessage) string {
	if !handoffEnum(result["status"], "APPLIED", "NEEDS_INFO", "CONFLICT", "NO_CHANGE", "REJECTED", "ERROR") || !handoffEnum(result["operation"], "CREATE", "UPDATE", "DELETE", "NONE") || !handoffText(result["summary"]) || !handoffText(result["reason"]) {
		return "Learner必须有合法管理status/operation及非空summary/reason。"
	}
	var verified *bool
	if json.Unmarshal(result["verified"], &verified) != nil || verified == nil || !handoffArrays(result, "evidence_refs", "changes", "questions", "suggestions") {
		return "Learner必须包含verified布尔值及evidence_refs/changes/questions/suggestions数组（可为空）。"
	}
	for _, key := range []string{"request_id", "item_id", "parent_item_id", "workflow_id"} {
		var id string
		if json.Unmarshal(result[key], &id) != nil {
			return "Learner的request_id/item_id/parent_item_id/workflow_id必须为字符串或null；未知值用null。"
		}
	}
	var version int
	if json.Unmarshal(result["workflow_version"], &version) != nil || !handoffObjectOrNull(result["entity"]) {
		return "Learner必须包含entity对象或null，以及整数workflow_version或null。"
	}
	return ""
}

func handoffText(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

func handoffEnum(raw json.RawMessage, allowed ...string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && contains(allowed, value)
}

func handoffArrays(result map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		var values []json.RawMessage
		if json.Unmarshal(result[key], &values) != nil || values == nil {
			return false
		}
	}
	return true
}

func handoffObjectOrNull(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil
}
