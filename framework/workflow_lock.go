package framework

import "context"

type heldWorkflowKey struct{}

// WorkflowLocker serializes workflows across processes when supplied by a host.
type WorkflowLocker interface {
	LockWorkflow(context.Context, Scope) (func(), error)
}

func (r *Runtime) lockWorkflow(ctx context.Context, s Scope) (func(), error) {
	if locker, ok := r.Repository.(WorkflowLocker); ok {
		return locker.LockWorkflow(ctx, s)
	}
	key := s.OwnerID + ":" + s.WorkflowID
	r.workflowMu.Lock()
	if r.workflowLocks == nil {
		r.workflowLocks = map[string]chan struct{}{}
	}
	ch := r.workflowLocks[key]
	if ch == nil {
		ch = make(chan struct{}, 1)
		r.workflowLocks[key] = ch
	}
	r.workflowMu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
