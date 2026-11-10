package framework

import (
	"encoding/json"
	"strings"
)

// Clean previously delivered query/search pages without discarding evidence.
// Other business tool results and the most recent messages remain unchanged.
func compactInvestigationHistory(messages []Message) {
	for i := 0; i < len(messages)-2; i++ {
		m := &messages[i]
		if m.Role != "tool" {
			continue
		}
		var result map[string]any
		decoder := json.NewDecoder(strings.NewReader(m.Content))
		decoder.UseNumber()
		if !json.Valid([]byte(m.Content)) || decoder.Decode(&result) != nil || result["error"] != nil {
			continue
		}
		data, ok := result["data"].(map[string]any)
		if !ok {
			continue
		}
		_, rows := data["rows"].([]any)
		_, hits := data["hits"].([]any)
		if !rows && !hits {
			continue
		}
		result["data"] = CleanQueryData(data)
		if encoded, err := json.Marshal(result); err == nil {
			m.Content = string(encoded)
		}
	}
}
