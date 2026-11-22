package framework

import (
	"bytes"
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

func dispatchContext(ctx context.Context, owner string, raw json.RawMessage) (map[string]json.RawMessage, error) {
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
	if hasHost {
		original, err := dispatchEventObject(bound.event)
		if err != nil {
			return nil, Fail("INVALID_ARGUMENT", "host original dispatch event must be a JSON object")
		}
		if supplied, ok := child["event"]; ok {
			provided, err := dispatchEventObject(supplied)
			if err != nil || !reflect.DeepEqual(original, provided) {
				return nil, Fail("INVALID_ARGUMENT", "context.event differs from the host original event; omit context.event to forward the original event unchanged")
			}
		}
		// Forward the complete host value, including fields unknown to the framework.
		child["event"] = append(json.RawMessage(nil), bound.event...)
	}
	return child, nil
}

func dispatchEventObject(raw json.RawMessage) (map[string]any, error) {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if !json.Valid(raw) || decoder.Decode(&value) != nil || value == nil {
		return nil, Fail("INVALID_ARGUMENT", "dispatch event must be a JSON object")
	}
	return value, nil
}
