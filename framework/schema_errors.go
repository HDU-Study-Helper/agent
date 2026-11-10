package framework

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"strings"
	"unicode/utf8"
)

// Return schema-owned names and diagnostic categories only. The library's full
// Error string can echo enum values, property names or user content, so do not
// serialize it or arbitrary instance-location tokens into model/log errors.
func inputSchemaError(err error, raw json.RawMessage) string {
	const prefix = "arguments do not match tool schema"
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return prefix
	}
	var schema any
	if json.Unmarshal(raw, &schema) != nil {
		return prefix
	}
	names := map[string]bool{}
	var collect func(any)
	collect = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for key, val := range x {
				if key == "properties" {
					if props, ok := val.(map[string]any); ok {
						for name := range props {
							names[name] = true
						}
					}
				}
				collect(val)
			}
		case []any:
			for _, val := range x {
				collect(val)
			}
		}
	}
	collect(schema)
	pointer := func(tokens []string) string {
		parts := make([]string, len(tokens))
		for i, token := range tokens {
			if !names[token] {
				digits := token != ""
				for _, r := range token {
					if r < '0' || r > '9' {
						digits = false
						break
					}
				}
				if !digits {
					token = "<field>"
				}
			}
			parts[i] = strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
		}
		return "/" + strings.Join(parts, "/")
	}
	details := []string{}
	var visit func(*jsonschema.ValidationError)
	visit = func(e *jsonschema.ValidationError) {
		if len(details) >= 6 {
			return
		}
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				visit(c)
			}
			return
		}
		path := pointer(e.InstanceLocation)
		keyword := e.ErrorKind.KeywordPath()
		label := "schema"
		if len(keyword) > 0 {
			label = keyword[0]
		}
		reason := "constraint failed"
		switch k := e.ErrorKind.(type) {
		case *kind.Type:
			reason = "expected " + strings.Join(k.Want, " or ")
		case *kind.Required:
			missing := []string{}
			for _, name := range k.Missing {
				if names[name] {
					missing = append(missing, name)
				}
			}
			reason = "missing required field"
			if len(missing) > 0 {
				reason += " " + strings.Join(missing, ",")
			}
		case *kind.AdditionalProperties:
			reason = "unexpected field; use only properties listed in the tool schema"
		case *kind.Enum:
			reason = "value is outside the schema enum"
		case *kind.Const:
			reason = "value differs from required constant"
		case *kind.MinLength:
			reason = "string is too short"
		case *kind.MaxLength:
			reason = "string is too long"
		case *kind.Format:
			reason = "invalid format"
		}
		details = append(details, fmt.Sprintf("%s [%s]: %s", path, label, reason))
	}
	visit(validation)
	message := prefix
	if len(details) > 0 {
		message += ": " + strings.Join(details, "; ")
	}
	if len(message) > 1024 {
		message = message[:1024]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return message
}
