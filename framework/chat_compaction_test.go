package framework

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactDispatchKeepsQuestionAndWorkerText(t *testing.T) {
	out, _ := json.Marshal(map[string]any{"text": "周五截止。", "interaction": map[string]any{"type": "CHOICE", "question": "你想哪天复习？", "question_id": "real-id", "options": []string{"周三", "周四"}}, "large_private_details": strings.Repeat("private data", 10000)})
	compact, ok := compactDispatchData(RunResult{RunID: "child", Output: string(out)}, 1024)
	if !ok {
		t.Fatal("could not compact")
	}
	r := compact.(RunResult)
	if !strings.Contains(r.Output, "real-id") || !strings.Contains(r.Output, "哪天复习") || !strings.Contains(r.Output, "周五截止") || strings.Contains(r.Output, "private data") {
		t.Fatal("unusable or leaking handoff", r.Output)
	}
}
func TestPersistenceRefsNeverComeFromModelArtifactText(t *testing.T) {
	for _, v := range []struct{ tool, kind, want string }{{"public_todo_upsert", "todo", "TASK"}, {"planned_job_create", "job", "PLANNED_JOB"}, {"notice_create", "job", "PLANNED_JOB"}, {"notice_create", "notice", "NOTICE"}, {"public_todo_list", "todo", ""}, {"notice_create", "todo", ""}} {
		rs := toolResultRefs(v.tool, ToolResult{OK: true, Data: map[string]any{"kind": v.kind, "id": "actual-id"}})
		if v.want == "" && len(rs) != 0 || v.want != "" && (len(rs) != 1 || rs[0].Kind != v.want || rs[0].ID != "actual-id") {
			t.Fatal(v, rs)
		}
	}
}
