package framework

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Cleaning limits bound work on unusually deep or large host values. A subtree
// beyond a limit is retained verbatim; limits never truncate business evidence.
const (
	businessCleaningMaxDepth = 64
	businessCleaningMaxNodes = 100000
	businessJSONMaxBytes     = 1 << 20
)

// CleanBusinessData returns a cleaned copy of JSON-shaped business data. It
// removes null and empty object fields, retaining false, zero, every
// nonempty identity field and all text. Array positions are preserved, including
// empty elements, because their index can carry meaning. Only known JSON columns are decoded,
// and only when their string value contains a valid JSON object or array.
// Unknown Go objects and subtrees beyond the work limits are kept unchanged.
// This function does not introduce row defaults or change a query envelope.
func CleanBusinessData(value any) any {
	remaining := businessCleaningMaxNodes
	cleaned, _ := cleanBusinessValue(value, "", 0, &remaining)
	return cleaned
}

func cleanBusinessValue(value any, key string, depth int, remaining *int) (any, bool) {
	if value == nil {
		return nil, false
	}
	if depth >= businessCleaningMaxDepth || *remaining <= 0 {
		return value, true
	}
	*remaining--
	if text, ok := value.(string); ok {
		if text == "" {
			return text, false
		}
		if knownBusinessJSONColumn(key) && len(text) <= businessJSONMaxBytes {
			trimmed := strings.TrimSpace(text)
			if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid([]byte(trimmed)) {
				decoder := json.NewDecoder(strings.NewReader(trimmed))
				decoder.UseNumber() // Native IDs and counters must not lose precision.
				var decoded any
				if decoder.Decode(&decoded) == nil {
					return cleanBusinessValue(decoded, "", depth+1, remaining)
				}
			}
		}
		return text, true
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return value, true
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			name := iter.Key().String()
			if cleaned, keep := cleanBusinessValue(iter.Value().Interface(), name, depth+1, remaining); keep {
				out[name] = cleaned
			}
		}
		return out, len(out) != 0
	case reflect.Slice, reflect.Array:
		// Byte slices can be opaque source payloads, not JSON containers.
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return value, v.Len() != 0
		}
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			// Clean object fields inside an array without shifting the indices
			// of paths, ordered evidence, matrix cells or query rows.
			out[i], _ = cleanBusinessValue(v.Index(i).Interface(), "", depth+1, remaining)
		}
		return out, len(out) != 0
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil, false
		}
	}
	return value, true
}

func knownBusinessJSONColumn(key string) bool {
	switch key {
	case "metadata", "path", "record_ids", "missing", "details":
		return true
	}
	return strings.HasSuffix(key, "_time")
}

// CleanQueryData is a history-only representation of query/search pages. The
// returned map can contain common_fields and common_fields_note, which are not
// part of the strict QueryResult tool schema. Restore a query row with
// merge(common_fields, row), or a search row with merge(common_fields, hit.row),
// with the individual row taking precedence. All rows, hits, pagination and
// coverage remain available. No identity aliases are removed within a row.
func CleanQueryData(value any) any {
	switch page := value.(type) {
	case QueryResult:
		value = map[string]any{"rows": page.Rows, "has_more": page.HasMore}
		if page.NextOffset != nil {
			value.(map[string]any)["next_offset"] = *page.NextOffset
		}
		if page.PageLimitReason != "" {
			value.(map[string]any)["page_limit_reason"] = page.PageLimitReason
		}
	case KeywordSearchResult:
		hits := make([]any, len(page.Hits))
		for i, hit := range page.Hits {
			hits[i] = map[string]any{"dataset": hit.Dataset, "row": hit.Row}
		}
		value = map[string]any{"hits": hits, "coverage": page.Coverage, "has_more": page.HasMore}
		if page.NextCursor != "" {
			value.(map[string]any)["next_cursor"] = page.NextCursor
		}
	}
	original, ok := value.(map[string]any)
	if !ok {
		return CleanBusinessData(value)
	}
	page, ok := CleanBusinessData(original).(map[string]any)
	if !ok {
		return CleanBusinessData(value)
	}

	// Rows/hits are records, including an empty projected record. Keep their
	// cardinality and required empty collections instead of filtering them.
	var rows []map[string]any
	kind := ""
	for _, name := range []string{"rows", "hits"} {
		collection, exists := original[name]
		if !exists || collection == nil {
			continue
		}
		v := reflect.ValueOf(collection)
		if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
			continue
		}
		items := make([]any, v.Len())
		valid := true
		for i := range items {
			item, ok := CleanBusinessData(v.Index(i).Interface()).(map[string]any)
			if !ok {
				valid = false
				break
			}
			items[i] = item
			if name == "rows" {
				rows = append(rows, item)
			} else if row, ok := item["row"].(map[string]any); ok {
				rows = append(rows, row)
			} else {
				// An empty hit.row is still a delivered record.
				rawHit, ok := v.Index(i).Interface().(map[string]any)
				if !ok {
					valid = false
					break
				}
				if _, ok := rawHit["row"].(map[string]any); !ok {
					// Unexpected row shapes are evidence too. Keep the generic
					// cleaned value without attempting envelope transformations.
					valid = false
					break
				}
				item["row"] = map[string]any{}
				rows = append(rows, item["row"].(map[string]any))
			}
		}
		if valid {
			page[name] = items
			kind = name
		}
		break
	}
	if coverage, exists := original["coverage"]; exists && coverage != nil {
		v := reflect.ValueOf(coverage)
		if (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) && v.Len() == 0 {
			page["coverage"] = []any{}
		}
	}
	if kind == "" || len(rows) < 2 {
		return page
	}
	if _, exists := original["common_fields"]; exists {
		return page
	}
	if _, exists := original["common_fields_note"]; exists {
		return page
	}

	// Only documented page context fields are eligible. Record identity,
	// body/text, path, status and personal marks always stay on each row.
	common := map[string]any{}
	for _, key := range []string{"source", "connection_id", "conn", "course_name", "course_ref", "offering_id", "kind", "task_type"} {
		candidate, exists := rows[0][key]
		if !exists {
			continue
		}
		same := true
		for _, row := range rows[1:] {
			other, exists := row[key]
			if !exists || !reflect.DeepEqual(candidate, other) {
				same = false
				break
			}
		}
		if same {
			common[key] = candidate
		}
	}
	if len(common) == 0 {
		return page
	}
	before, err := json.Marshal(page)
	if err != nil {
		return page
	}
	for _, row := range rows {
		for key := range common {
			delete(row, key)
		}
	}
	page["common_fields"] = common
	if kind == "rows" {
		page["common_fields_note"] = "Each row inherits common_fields; merge(common_fields, row), with row values taking precedence. All nonempty fields are retained."
	} else {
		page["common_fields_note"] = "Each hit.row inherits common_fields; merge(common_fields, hit.row), with row values taking precedence. All nonempty fields are retained."
	}
	after, err := json.Marshal(page)
	if err != nil || len(after) >= len(before) {
		for _, row := range rows {
			for key, field := range common {
				row[key] = field
			}
		}
		delete(page, "common_fields")
		delete(page, "common_fields_note")
	}
	return page
}
