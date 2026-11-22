package framework

import "context"

// OutputValidationKind identifies a host-requested correction. Format corrections
// may only rewrite the final text; semantic corrections may use the run's original
// authorized tools. The framework never selects a policy from an Agent role.
type OutputValidationKind string

const (
	OutputFormat         OutputValidationKind = "format"
	OutputSemantic       OutputValidationKind = "semantic"
	MaxOutputCorrections                      = 2
)

// OutputValidationFailure is a recoverable host validation result. Feedback is
// shown to the model inside the existing Run; it must not contain credentials.
// Return an ordinary error for cancellation, permission, conflicts or storage
// failures, which are never retried by this mechanism.
type OutputValidationFailure struct {
	Kind     OutputValidationKind
	Code     string
	Feedback string
}

func (f *OutputValidationFailure) Error() string { return f.Code + ": " + f.Feedback }

type OutputValidationInput struct {
	Output string
	Record RunRecord
}

type OutputValidation struct {
	OwnerID        string
	AgentID        string
	MaxCorrections int
	Validate       func(context.Context, OutputValidationInput) (*OutputValidationFailure, error)
}

type outputValidationKey struct{}

// WithOutputValidation is a trusted in-process host setting, unavailable in JSON.
// Binding both identities prevents a caller's validator from applying to a child.
// Nil/default policy leaves Runtime behavior unchanged. Corrections cannot exceed
// two additional attempts, and still consume the same Run budgets and deadline.
func WithOutputValidation(ctx context.Context, policy OutputValidation) context.Context {
	return context.WithValue(ctx, outputValidationKey{}, policy)
}

func outputValidationFor(ctx context.Context, owner, agent string) (OutputValidation, bool) {
	p, ok := ctx.Value(outputValidationKey{}).(OutputValidation)
	return p, ok && p.Validate != nil && p.OwnerID == owner && p.AgentID == agent
}

func recoverableOutputFailure(f *OutputValidationFailure) bool {
	if f == nil || (f.Kind != OutputFormat && f.Kind != OutputSemantic) || f.Feedback == "" {
		return false
	}
	switch f.Code {
	case "CONFLICT", "PERMISSION_DENIED", "EXECUTION_LIMIT_EXCEEDED", "TIMEOUT", "CANCELLED", "INTERNAL_ERROR", "PROVIDER_ERROR", "RATE_LIMITED":
		return false
	}
	return true
}
