package framework

import (
	"encoding/json"
	"sort"
	"strings"
)

// Tracks unfinished authorized keyword searches within one execution, rather
// than accepting a final answer that merely tells the user to continue paging.
type investigationProgress struct {
	pending       map[string]KeywordSearchRequest
	completed     map[string]bool
	queryPending  map[string]Query
	repair        string
	lastRejection string
	rejections    int
}

func (p *investigationProgress) observe(call ToolCall, result ToolResult) {
	if result.Error != nil {
		if call.Function.Name == "workflow_create" && result.Error.Code == "INVALID_ARGUMENT" {
			p.repair = result.Error.Message
		}
		return
	}
	if call.Function.Name == "workflow_create" || call.Function.Name == "workflow_update" {
		p.repair = ""
	}
	if call.Function.Name == "data_query" {
		var q Query
		var page QueryResult
		raw, _ := json.Marshal(result.Data)
		if json.Unmarshal([]byte(call.Function.Arguments), &q) == nil && json.Unmarshal(raw, &page) == nil {
			q.Offset = 0
			keyRaw, _ := json.Marshal(q)
			key := string(keyRaw)
			if p.queryPending == nil {
				p.queryPending = map[string]Query{}
			}
			if page.HasMore && page.NextOffset != nil {
				q.Offset = *page.NextOffset
				p.queryPending[key] = q
			} else {
				delete(p.queryPending, key)
			}
		}
		return
	}
	if call.Function.Name != "data_search" {
		return
	}
	var request KeywordSearchRequest
	if json.Unmarshal([]byte(call.Function.Arguments), &request) != nil {
		return
	}
	issued := request.Cursor == ""
	for key, pending := range p.pending {
		if request.Cursor != "" && pending.Cursor == request.Cursor {
			issued = true
			delete(p.pending, key)
		}
	}
	if !issued {
		return
	}
	request.Cursor = ""
	request.Limit = 100
	for i := range request.Keywords {
		request.Keywords[i] = strings.ToLower(strings.TrimSpace(request.Keywords[i]))
	}
	sort.Strings(request.Keywords)
	unique := request.Keywords[:0]
	for _, keyword := range request.Keywords {
		if len(unique) == 0 || unique[len(unique)-1] != keyword {
			unique = append(unique, keyword)
		}
	}
	request.Keywords = unique
	if len(request.Datasets) == 0 {
		request.Datasets = []string{"platform_records", "academic_offerings", "academic_items", "schedules", "dws_messages", "dws_attachments"}
	}
	sort.Strings(request.Datasets)
	keyBytes, _ := json.Marshal(request)
	key := string(keyBytes)
	if p.pending == nil {
		p.pending = map[string]KeywordSearchRequest{}
	}
	raw, _ := json.Marshal(result.Data)
	var page KeywordSearchResult
	if json.Unmarshal(raw, &page) != nil {
		return
	}
	if p.completed == nil {
		p.completed = map[string]bool{}
	}
	for _, coverage := range page.Coverage {
		if coverage.Complete {
			p.completed[coverage.Dataset] = true
		}
	}
	if page.HasMore && page.NextCursor != "" {
		request.Cursor = page.NextCursor
		p.pending[key] = request
	} else {
		delete(p.pending, key)
	}
}
func (p *investigationProgress) hasCompleteCourseDiscovery() bool {
	for _, dataset := range []string{"platform_records", "academic_offerings", "academic_items", "schedules", "dws_messages", "dws_attachments"} {
		if !p.completed[dataset] {
			return false
		}
	}
	return true
}

func learnerCourseCreation(raw string) bool {
	var p struct {
		Definition struct {
			Entity struct {
				Kind string `json:"kind"`
			} `json:"entity"`
		} `json:"definition"`
	}
	return json.Unmarshal([]byte(raw), &p) == nil && p.Definition.Entity.Kind == "course"
}

func learnerCourseMissingProfile(raw string) bool {
	var p struct {
		Definition struct {
			Entity struct {
				Kind string `json:"kind"`
			} `json:"entity"`
			Manifest json.RawMessage `json:"profile_manifest"`
		} `json:"definition"`
		Seeds []json.RawMessage `json:"initial_private_memories"`
	}
	if json.Unmarshal([]byte(raw), &p) != nil || p.Definition.Entity.Kind != "course" {
		return false
	}
	return len(p.Seeds) == 0 || len(p.Definition.Manifest) == 0 || string(p.Definition.Manifest) == "null"
}

func (p *investigationProgress) continuation() string {
	if len(p.pending) > 0 {
		next := []KeywordSearchRequest{}
		for _, q := range p.pending {
			next = append(next, q)
		}
		raw, _ := json.Marshal(next)
		return "当前Run的关键词调查尚未完成。工具仍可继续，不能把分页当作用户缺信息或最终ERROR。请先调用data_search继续以下原样请求，直到has_more=false，再调查关联数据并创建带私有画像的Workflow。未写入的参数错误可自行修正。待继续请求：" + string(raw)
	}
	if len(p.queryPending) > 0 {
		queries := []Query{}
		for _, q := range p.queryPending {
			queries = append(queries, q)
		}
		raw, _ := json.Marshal(queries)
		return "相关业务明细仍有has_more=true的可读页，请继续data_query直到末页，再完成画像；这不是用户缺信息。待继续：" + string(raw)
	}
	if p.repair != "" {
		return "workflow_create的参数在写入前被拒绝，应依据真实已查证据补齐必填字段并在当前Run重新调用；不能把可修正的Schema错误直接作为最终结果。错误：" + p.repair
	}
	return ""
}
func (p *investigationProgress) rejectFinal(message string) bool {
	if strings.TrimSpace(message) == p.lastRejection {
		p.rejections++
	} else {
		p.lastRejection = message
		p.rejections = 1
	}
	return p.rejections <= 3
}
