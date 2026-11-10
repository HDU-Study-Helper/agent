package framework

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCleanBusinessDataPreservesAssignmentTimeAndPersonalEvidence(t *testing.T) {
	assignment := map[string]any{
		"id": "assignment", "item_id": "assignment", "native_id": strings.Repeat("native-", 60),
		"title": strings.Repeat("完整作业标题", 80), "text": strings.Repeat("完整作业要求", 800),
		"start_time":    `{"raw":"11月8日 18:00","precision":"partial","reason":"year_missing","value":""}`,
		"deadline_time": `{"raw":"","precision":"missing","reason":"source_not_provided","value":null}`,
		"metadata":      `{"current_user_state":{},"assessment_time_fields":{"node":{},"test":{}},"reply_count":0,"is_current_user":false,"current_user_mark":false}`,
		"path":          `["chapter-1","node-2"]`, "record_ids": `["60619","9007199254740993123"]`,
		"missing": `["deadline_year"]`, "details": `{"description":"full description","unknown":{"empty":null,"score":0}}`,
		"status": "open", "source_status": "0", "used_try_count": 0, "submitted": false,
		"empty": "", "null": nil, "empty_object": map[string]any{}, "empty_array": []any{},
	}
	got := CleanBusinessData(assignment).(map[string]any)
	for _, key := range []string{"id", "item_id", "native_id", "title", "text", "status", "source_status", "used_try_count", "submitted"} {
		if !reflect.DeepEqual(got[key], assignment[key]) {
			t.Errorf("nonempty evidence %s changed", key)
		}
	}
	start := got["start_time"].(map[string]any)
	deadline := got["deadline_time"].(map[string]any)
	if start["raw"] != "11月8日 18:00" || start["precision"] != "partial" || start["reason"] != "year_missing" || deadline["precision"] != "missing" || deadline["reason"] != "source_not_provided" {
		t.Fatalf("explicit missing/partial time evidence lost: %#v / %#v", start, deadline)
	}
	meta := got["metadata"].(map[string]any)
	if !reflect.DeepEqual(meta, map[string]any{"reply_count": json.Number("0"), "is_current_user": false, "current_user_mark": false}) {
		t.Fatalf("empty placeholders or personal evidence incorrect: %#v", meta)
	}
	for _, key := range []string{"empty", "null", "empty_object", "empty_array"} {
		if _, exists := got[key]; exists {
			t.Errorf("empty field %s survived", key)
		}
	}
	if !reflect.DeepEqual(got["path"], []any{"chapter-1", "node-2"}) || !reflect.DeepEqual(got["record_ids"], []any{"60619", "9007199254740993123"}) {
		t.Fatal("source paths or record IDs changed")
	}
	if _, exists := assignment["empty"]; !exists || assignment["metadata"] != `{"current_user_state":{},"assessment_time_fields":{"node":{},"test":{}},"reply_count":0,"is_current_user":false,"current_user_mark":false}` {
		t.Fatal("cleaning mutated source evidence")
	}
	if !reflect.DeepEqual(CleanBusinessData(got), got) {
		t.Fatal("cleaning is not idempotent")
	}
}

func TestCleanBusinessDataParsesOnlyKnownContainerJSONAndPreservesPositions(t *testing.T) {
	input := map[string]any{
		"metadata": `{"id":9007199254740993123,"nested":{"empty":"","false":false}}`,
		"text":     `{"explanation":"user-authored JSON stays text"}`,
		"title":    `["an ordinary title"]`, "unknown": `{"field":1}`,
		"details": `false`, "missing": `"not a container"`, "start_time": `{"invalid":`,
		"path": `["first",null,"",{},[],"last"]`,
		"rows": []map[string]any{{"id": "first", "empty": nil}, {}, {"id": "last"}},
	}
	got := CleanBusinessData(input).(map[string]any)
	for _, key := range []string{"text", "title", "unknown", "details", "missing", "start_time"} {
		if got[key] != input[key] {
			t.Errorf("arbitrary text %s was parsed or changed", key)
		}
	}
	if got["metadata"].(map[string]any)["id"] != json.Number("9007199254740993123") {
		t.Fatal("JSON numeric identity lost precision")
	}
	path := got["path"].([]any)
	if len(path) != 6 || path[0] != "first" || path[1] != nil || path[2] != "" || path[5] != "last" {
		t.Fatalf("path positions changed: %#v", path)
	}
	if rows := got["rows"].([]any); len(rows) != 3 || len(rows[1].(map[string]any)) != 0 {
		t.Fatalf("empty projected row removed: %#v", rows)
	}
}

func TestCleanBusinessDataBoundsRetainUnprocessedEvidence(t *testing.T) {
	var nested any = map[string]any{"id": "deep-evidence", "empty": ""}
	for i := 0; i < businessCleaningMaxDepth+5; i++ {
		nested = map[string]any{"child": nested}
	}
	cleaned := CleanBusinessData(nested)
	for i := 0; i < businessCleaningMaxDepth+5; i++ {
		cleaned = cleaned.(map[string]any)["child"]
	}
	if cleaned.(map[string]any)["id"] != "deep-evidence" {
		t.Fatal("depth limit dropped evidence")
	}
	large := `{"text":"` + strings.Repeat("x", businessJSONMaxBytes) + `"}`
	if got := CleanBusinessData(map[string]any{"metadata": large}).(map[string]any)["metadata"]; got != large {
		t.Fatal("oversized source JSON was truncated or dropped")
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if got := CleanBusinessData(cycle).(map[string]any); got["self"] == nil {
		t.Fatal("cyclic unknown input lost at depth bound")
	}
	type opaque struct{ Evidence string }
	unknown := opaque{"retain exactly"}
	if got := CleanBusinessData(unknown); got != unknown {
		t.Fatal("unknown Go object changed")
	}
	var empty *opaque
	if got := CleanBusinessData(map[string]any{"empty": empty}).(map[string]any); len(got) != 0 {
		t.Fatal("typed null survived")
	}
}

func TestCleanQueryDataHoists187NodeContextAndRestoresEveryRow(t *testing.T) {
	rows := make([]map[string]any, 187)
	for i := range rows {
		id := fmt.Sprintf("node-%03d-%s", i, strings.Repeat("identity", 30))
		rows[i] = map[string]any{
			"id": id, "item_id": id, "native_id": fmt.Sprintf("native-%d", i), "external_id": fmt.Sprintf("external-%d", i),
			"source": "mooc", "connection_id": strings.Repeat("connection-", 12), "course_name": strings.Repeat("高等数学课程", 10),
			"course_ref": strings.Repeat("course-ref-", 12), "offering_id": strings.Repeat("offering-", 12), "kind": "chapter_node",
			"metadata": `{"current_user_state":{},"assessment_time_fields":{"node":{},"test":{}},"coverage_reason":"source reports node progress","current_user_mark":false,"question_count":0}`,
			"path":     fmt.Sprintf(`["chapter-%d","node-%d"]`, i/20, i), "record_ids": []string{fmt.Sprint(60000 + i)},
			"status": "not_started", "text": fmt.Sprintf("Full node instructions %d", i), "current_user_mark": false,
		}
	}
	next := 0
	input := QueryResult{Rows: rows, HasMore: false, NextOffset: &next}
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	page := CleanQueryData(input).(map[string]any)
	common, ok := page["common_fields"].(map[string]any)
	if !ok || common["source"] != "mooc" || !strings.Contains(page["common_fields_note"].(string), "merge(common_fields, row)") {
		t.Fatalf("missing explicit common context: %#v", page)
	}
	gotRows := page["rows"].([]any)
	if len(gotRows) != 187 || page["has_more"] != false || page["next_offset"] != 0 {
		t.Fatalf("row count or pagination changed: %d / %#v", len(gotRows), page["has_more"])
	}
	for i, value := range gotRows {
		row := value.(map[string]any)
		for _, key := range []string{"id", "item_id", "native_id", "external_id", "path", "record_ids", "status", "metadata", "current_user_mark"} {
			if _, exists := row[key]; !exists {
				t.Fatalf("row %d lost local evidence %s", i, key)
			}
		}
		restored := map[string]any{}
		for key, field := range common {
			restored[key] = field
		}
		for key, field := range row {
			restored[key] = field
		}
		if want := CleanBusinessData(rows[i]); !reflect.DeepEqual(restored, want) {
			t.Fatalf("row %d cannot be exactly restored\ngot %#v\nwant %#v", i, restored, want)
		}
	}
	after, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("187 nodes: %d -> %d bytes (%.1f%% reduction), every cleaned row restored exactly", len(before), len(after), 100*(1-float64(len(after))/float64(len(before))))
	if len(after)*100 >= len(before)*70 {
		t.Fatal("repeated page metadata was not materially reduced")
	}
	if rows[0]["source"] != "mooc" {
		t.Fatal("history hoisting mutated source rows")
	}
	if !reflect.DeepEqual(CleanQueryData(page), page) {
		t.Fatal("page cleaning is not idempotent")
	}
	generic := CleanBusinessData(map[string]any{"rows": rows}).(map[string]any)
	if _, exists := generic["common_fields"]; exists {
		t.Fatal("generic cleaner introduced schema-breaking defaults")
	}
}

func TestCleanQueryDataDoesNotInventDefaultsForMissingOrDifferentFields(t *testing.T) {
	shared := strings.Repeat("shared course", 100)
	input := map[string]any{"rows": []map[string]any{
		{"id": "same-id", "item_id": "same-id", "course_name": shared, "source": "mooc", "course_ref": "one", "status": "open"},
		{"id": "same-id", "item_id": "same-id", "course_ref": "two", "status": "open"},
	}, "has_more": false}
	got := CleanQueryData(input).(map[string]any)
	if _, exists := got["common_fields"]; exists {
		t.Fatal("missing fields inherited invented context or identity/status was hoisted")
	}
	if !reflect.DeepEqual(got, CleanBusinessData(input)) {
		t.Fatal("different or missing page fields changed")
	}
	small := map[string]any{"rows": []map[string]any{{"id": "1", "source": "x"}, {"id": "2", "source": "x"}}}
	if page := CleanQueryData(small).(map[string]any); page["common_fields"] != nil {
		t.Fatal("hoisting increased page size")
	}
}

func TestCleanQueryDataSearchAndEmptyCollections(t *testing.T) {
	commonTitle := strings.Repeat("same course context", 40)
	search := KeywordSearchResult{
		Hits: []KeywordSearchHit{
			{Dataset: "academic_items", Row: map[string]any{"id": "a", "course_name": commonTitle, "source": "mooc", "metadata": map[string]any{"is_current_user": true}}},
			{Dataset: "platform_records", Row: map[string]any{"id": "b", "course_name": commonTitle, "source": "mooc", "metadata": map[string]any{"is_current_user": false}}},
		},
		Coverage: []KeywordSearchCoverage{{Dataset: "academic_items", Matched: 0, Complete: false}}, HasMore: true, NextCursor: "cursor-full",
	}
	page := CleanQueryData(search).(map[string]any)
	if page["common_fields"] == nil || !strings.Contains(page["common_fields_note"].(string), "hit.row") || page["next_cursor"] != "cursor-full" || page["has_more"] != true {
		t.Fatalf("search context/pagination lost: %#v", page)
	}
	for i, hit := range page["hits"].([]any) {
		row := hit.(map[string]any)["row"].(map[string]any)
		if row["metadata"].(map[string]any)["is_current_user"] != (i == 0) {
			t.Fatal("personal participation moved or lost")
		}
	}
	for _, input := range []any{
		QueryResult{Rows: []map[string]any{}},
		QueryResult{Rows: []map[string]any{{}, {"empty": ""}}},
		KeywordSearchResult{Hits: []KeywordSearchHit{}, Coverage: []KeywordSearchCoverage{}},
		KeywordSearchResult{Hits: []KeywordSearchHit{{Dataset: "academic_items", Row: map[string]any{}}}, Coverage: []KeywordSearchCoverage{}},
	} {
		got := CleanQueryData(input).(map[string]any)
		if got["has_more"] != false {
			t.Fatal("false pagination removed")
		}
		if _, query := input.(QueryResult); query {
			if _, exists := got["rows"]; !exists {
				t.Fatal("required empty rows removed")
			}
		} else if _, exists := got["hits"]; !exists || got["coverage"] == nil {
			t.Fatal("required empty hits/coverage removed")
		}
	}
}
