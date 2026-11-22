package framework

import (
	"encoding/json"
	"strings"
)

const toolReturnContractMarker = "\nReturn contract (the data field of the {ok,data,error} envelope): "

// ModelFacingTools exposes return contracts through standard function descriptions.
// It does not add nonstandard provider wire fields or grant tools/permissions.
// The host must authorize and specialize tools before calling this projection.
func ModelFacingTools(specs []ToolSpec) []ToolSpec {
	out := make([]ToolSpec, len(specs))
	for i, s := range specs {
		s.InputSchema = append(json.RawMessage(nil), s.InputSchema...)
		s.OutputSchema = append(json.RawMessage(nil), s.OutputSchema...)
		s.Permissions = append([]string(nil), s.Permissions...)
		// Replacing our suffix makes repeated adapter projection idempotent and lets
		// trusted hosts update the output contract without stale duplicate text.
		if at := strings.Index(s.Description, toolReturnContractMarker); at >= 0 {
			s.Description = s.Description[:at]
		}
		if len(s.OutputSchema) > 0 {
			var v any
			if json.Unmarshal(s.OutputSchema, &v) == nil {
				raw, _ := json.Marshal(v)
				s.Description += toolReturnContractMarker + string(raw) + ". On error, data is absent/null and error contains code, message and retryable; do not treat an error as an empty successful result."
			}
		}
		out[i] = s
	}
	return out
}
