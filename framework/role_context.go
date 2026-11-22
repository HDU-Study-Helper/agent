package framework

import "context"

type roleContextKey struct{}
type roleContext struct{ owner, agent, text string }

// WithRoleContext adds host-selected role-specific operating guidance; no
// request field or child agent can introduce a system instruction.
func WithRoleContext(ctx context.Context, owner, agent, text string) context.Context {
	return context.WithValue(ctx, roleContextKey{}, roleContext{owner, agent, text})
}
