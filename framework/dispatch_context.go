package framework

import (
	"context"
	"encoding/json"
	"reflect"
)

type dispatchEventKey struct{}
type dispatchEvent struct {
	owner string
	event json.RawMessage
}

// WithDispatchEvent retains the original owner-scoped event in trusted Go context.
// JSON input and tool arguments cannot establish this provenance or permissions.
func WithDispatchEvent(ctx context.Context, owner string, event json.RawMessage) context.Context {
	return context.WithValue(ctx, dispatchEventKey{}, dispatchEvent{owner: owner, event: append(json.RawMessage(nil), event...)})
}

func dispatchContext(ctx context.Context, owner string, raw json.RawMessage, requireEvent bool) (map[string]json.RawMessage, error) {
	child := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &child); err != nil || child == nil {
			return nil, Fail("INVALID_ARGUMENT", "context must be an object")
		}
	}
	bound, hasHost := ctx.Value(dispatchEventKey{}).(dispatchEvent)
	if hasHost && bound.owner != owner {
		return nil, Fail("PERMISSION_DENIED", "original dispatch event belongs to another owner")
	}
	supplied, hasSupplied := child["event"]
	if hasHost {
		if err := validateDispatchEvent(bound.event); err != nil {
			return nil, Fail("INVALID_ARGUMENT", "host original dispatch event is invalid")
		}
		if !hasSupplied {
			child["event"] = append(json.RawMessage(nil), bound.event...)
		} else {
			if err := validateDispatchEvent(supplied); err != nil {
				return nil, err
			}
			var original, provided map[string]any
			_ = json.Unmarshal(bound.event, &original)
			_ = json.Unmarshal(supplied, &provided)
			// Keep explicit compatible legacy copies, but do not allow replacing the
			// original event identity, evidence or source under a trusted event binding.
			for _, key := range []string{"event_id", "content", "source", "evidence_refs"} {
				if !reflect.DeepEqual(original[key], provided[key]) {
					return nil, Fail("INVALID_ARGUMENT", "context.event differs from the host original event; omit context.event to forward original evidence unchanged")
				}
			}
		}
	} else if requireEvent && !hasSupplied {
		return nil, Fail("INVALID_ARGUMENT", "Workflow dispatch requires context.event:{event_id,content,source,evidence_refs} when no host original event is bound")
	}
	if event, ok := child["event"]; ok {
		if err := validateDispatchEvent(event); err != nil {
			return nil, err
		}
	}
	return child, nil
}

func validateDispatchEvent(raw json.RawMessage) error {
	encoded, _ := json.Marshal(dispatchEventSchema())
	schema, err := compileSchema(encoded)
	if err != nil {
		return Fail("INTERNAL_ERROR", "dispatch event schema unavailable")
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return Fail("INVALID_ARGUMENT", "context.event must be valid JSON")
	}
	if err = schema.Validate(value); err != nil {
		return Fail("INVALID_ARGUMENT", inputSchemaError(err, encoded))
	}
	return nil
}
