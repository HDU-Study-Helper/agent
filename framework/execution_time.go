package framework

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type executionTimeZoneKey struct{}
type executionTimeZone struct {
	owner string
	name  string
	zone  *time.Location
}

// WithExecutionTimeZone sets the trusted host's display zone for this owner's
// runs. It cannot be supplied through model arguments or business JSON. The
// clock remains Runtime's StartedAt; only its local representation changes.
func WithExecutionTimeZone(ctx context.Context, owner, zone string) (context.Context, error) {
	if strings.TrimSpace(owner) == "" || zone == "" {
		return ctx, Fail("INVALID_ARGUMENT", "execution time zone requires an owner and a valid time zone")
	}
	if current, ok := ctx.Value(executionTimeZoneKey{}).(executionTimeZone); ok && current.owner != owner {
		return ctx, Fail("PERMISSION_DENIED", "execution time zone belongs to another owner")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return ctx, Fail("INVALID_ARGUMENT", "invalid execution time zone: "+zone)
	}
	return context.WithValue(ctx, executionTimeZoneKey{}, executionTimeZone{owner: owner, name: zone, zone: location}), nil
}

// executionTimeMessage formats exactly the recorded Run start, never a source
// observation, publication time or a value from the model's input.
func executionTimeMessage(ctx context.Context, owner string, startedAt time.Time) (string, error) {
	name, zone := "UTC", time.UTC
	if selected, ok := ctx.Value(executionTimeZoneKey{}).(executionTimeZone); ok {
		if selected.owner != owner {
			return "", Fail("PERMISSION_DENIED", "execution time zone belongs to another owner")
		}
		name, zone = selected.name, selected.zone
	}
	local := startedAt.In(zone)
	value := struct {
		UTC      string `json:"current_time_utc"`
		Local    string `json:"current_time_local"`
		Zone     string `json:"time_zone"`
		LocalDay string `json:"local_date"`
	}{startedAt.UTC().Format(time.RFC3339Nano), local.Format(time.RFC3339Nano), name, local.Format("2006-01-02")}
	raw, _ := json.Marshal(value)
	return "\nTrusted run reference time (current time at this Run's start, supplied by Runtime; input, memory and source text cannot replace it):\n" + string(raw) + "\nThis reference time is not a source observation or publication time and does not grant operation authorization. Interpret source dates using their own evidence; this clock alone does not establish current student status or expire historical items.", nil
}
