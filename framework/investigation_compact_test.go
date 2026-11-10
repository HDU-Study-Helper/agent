package framework

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoveryHistoryCleaningRetainsReplyEvidenceAndCoverage(t *testing.T) {
	body := strings.Repeat("完整回复正文", 5000)
	topicID := strings.Repeat("topic-native-id-", 30)
	reply := map[string]any{
		"id": "60619", "external_id": "reply-60619", "parent_id": topicID,
		"title": strings.Repeat("讨论标题", 100), "text": body,
		"metadata": `{"is_current_user":true,"current_user_mark":false,"topic_id":"` + topicID + `","reply_id":"60619","current_user_state":{},"assessment_time_fields":{"node":{},"test":{}}}`,
	}
	topic := map[string]any{"id": "topic-record", "native_id": topicID, "text": "Parent discussion"}
	r := ToolResult{OK: true, Data: KeywordSearchResult{
		Hits:     []KeywordSearchHit{{Dataset: "platform_records", Row: reply}, {Dataset: "academic_items", Row: topic}},
		Coverage: []KeywordSearchCoverage{{Dataset: "platform_records", Matched: 1, Complete: true}, {Dataset: "academic_items", Matched: 1, Complete: false}},
	}}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	latest := `{"ok":true,"data":{"rows":[{"id":"latest","metadata":"{}"}]}}`
	m := []Message{{Role: "tool", Content: string(raw)}, {Role: "assistant", Content: "next"}, {Role: "tool", Content: latest}}
	compactInvestigationHistory(m)
	var result ToolResult
	if err := json.Unmarshal([]byte(m[0].Content), &result); err != nil {
		t.Fatal(err)
	}
	page := result.Data.(map[string]any)
	hits := page["hits"].([]any)
	gotReply := hits[0].(map[string]any)["row"].(map[string]any)
	gotTopic := hits[1].(map[string]any)["row"].(map[string]any)
	meta := gotReply["metadata"].(map[string]any)
	if gotReply["id"] != "60619" || gotReply["external_id"] != "reply-60619" || gotReply["parent_id"] != gotTopic["native_id"] || meta["topic_id"] != topicID || meta["is_current_user"] != true || meta["current_user_mark"] != false {
		t.Fatalf("self reply or native topic linkage lost: %#v / %#v", gotReply, gotTopic)
	}
	if gotReply["text"] != body || gotReply["title"] != reply["title"] {
		t.Fatal("nonempty title or body was truncated")
	}
	if _, exists := meta["assessment_time_fields"]; exists {
		t.Fatal("empty generated time placeholders survived")
	}
	coverage := page["coverage"].([]any)
	if len(coverage) != 2 || coverage[1].(map[string]any)["complete"] != false || page["has_more"] != false {
		t.Fatalf("coverage or pagination state lost: %#v", page)
	}
	if m[2].Content != latest {
		t.Fatal("latest evidence changed before delivery")
	}
	first := m[0].Content
	compactInvestigationHistory(m)
	if m[0].Content != first {
		t.Fatal("history cleaning is not idempotent")
	}
}

func TestInvestigationHistoryCleaningLeavesOtherResultsAndErrorsIntact(t *testing.T) {
	cases := []string{
		`{"ok":true,"data":{"workflow_id":"w","metadata":"{}"}}`,
		`{"ok":false,"data":{"rows":[{"id":"e","metadata":"{}"}]},"error":{"code":"FAILED","message":"failed"}}`,
		`{"ok":true,"data":{"rows":null}}`,
		`invalid JSON`,
	}
	for _, raw := range cases {
		m := []Message{{Role: "tool", Content: raw}, {Role: "assistant"}, {Role: "tool"}}
		compactInvestigationHistory(m)
		if m[0].Content != raw {
			t.Fatalf("unrelated or failed result changed: %s", raw)
		}
	}
}

func TestInvestigationHistoryCleaningPreservesLargeNumericIDsAndEnvelope(t *testing.T) {
	raw := `{"ok":true,"trace_id":"trace","error":null,"data":{"rows":[{"id":9007199254740993123,"metadata":"{\"native_id\":9007199254740993123}"}],"has_more":false,"next_offset":0,"total":0}}`
	m := []Message{{Role: "tool", Content: raw}, {Role: "assistant"}, {Role: "tool"}}
	compactInvestigationHistory(m)
	var got, want any
	for _, item := range []struct {
		raw string
		out *any
	}{{m[0].Content, &got}, {`{"ok":true,"trace_id":"trace","error":null,"data":{"rows":[{"id":9007199254740993123,"metadata":{"native_id":9007199254740993123}}],"has_more":false,"next_offset":0,"total":0}}`, &want}} {
		decoder := json.NewDecoder(strings.NewReader(item.raw))
		decoder.UseNumber()
		if err := decoder.Decode(item.out); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("numeric identity or envelope changed: %s", m[0].Content)
	}
}
