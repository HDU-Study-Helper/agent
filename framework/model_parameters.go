package framework

import (
	"encoding/json"
	"math"
)

// ValidateModelParameters checks adapter-owned custom parameters before saving
// or sending them. Provider credentials, endpoints, tools and messages cannot be
// overridden by this map. Omitted parameters are never auto-filled here.
func ValidateModelParameters(parameters map[string]json.RawMessage) error {
	if len(parameters) > 16 {
		return Fail("INVALID_ARGUMENT", "too many custom model parameters")
	}
	for key, raw := range parameters {
		if len(raw) > 1024 {
			return Fail("INVALID_ARGUMENT", "model parameter value is too large: "+key)
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return Fail("INVALID_ARGUMENT", "invalid JSON model parameter: "+key)
		}
		switch key {
		case "reasoning_effort":
			if value == nil {
				continue
			}
			s, ok := value.(string)
			if !ok || s == "" || len(s) > 64 {
				return Fail("INVALID_ARGUMENT", "reasoning_effort must be a nonempty string or null")
			}
		case "top_p", "presence_penalty", "frequency_penalty":
			n, ok := value.(float64)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
				return Fail("INVALID_ARGUMENT", "model parameter must be a number: "+key)
			}
			if key == "top_p" && (n < 0 || n > 1) || key != "top_p" && (n < -2 || n > 2) {
				return Fail("INVALID_ARGUMENT", "model parameter is out of range: "+key)
			}
		case "seed", "max_tokens":
			n, ok := value.(float64)
			if !ok || math.Trunc(n) != n || math.Abs(n) > 9007199254740991 || key == "max_tokens" && n <= 0 {
				return Fail("INVALID_ARGUMENT", "model parameter must be a valid integer: "+key)
			}
		default:
			return Fail("INVALID_ARGUMENT", "unsupported model parameter: "+key)
		}
	}
	return nil
}
