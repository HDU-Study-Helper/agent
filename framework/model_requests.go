package framework

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"sync"
	"time"
)

type modelObserverKey struct{}

func withModelObserver(ctx context.Context, fn func(string)) context.Context {
	return context.WithValue(ctx, modelObserverKey{}, fn)
}
func modelState(ctx context.Context, state string) {
	if fn, ok := ctx.Value(modelObserverKey{}).(func(string)); ok {
		fn(state)
	}
}

type modelWaiter struct {
	ctx     context.Context
	limit   int
	ready   chan struct{}
	granted bool // protected by modelGate.mu
}

type modelGate struct {
	mu      sync.Mutex
	active  int
	waiters []*modelWaiter
}

var modelGates struct {
	sync.Mutex
	values map[[32]byte]*modelGate
}

func sharedModelGate(base, key string) *modelGate {
	id := sha256.Sum256([]byte(base + "\x00" + key))
	modelGates.Lock()
	defer modelGates.Unlock()
	if modelGates.values == nil {
		modelGates.values = map[[32]byte]*modelGate{}
	}
	gate := modelGates.values[id]
	if gate == nil {
		gate = &modelGate{}
		modelGates.values[id] = gate
	}
	return gate
}
func (g *modelGate) acquire(ctx context.Context, limit int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if limit < 1 {
		limit = 1
	}
	waiter := &modelWaiter{ctx: ctx, limit: limit, ready: make(chan struct{})}
	g.mu.Lock()
	g.waiters = append(g.waiters, waiter)
	g.grantLocked()
	queued := !waiter.granted
	g.mu.Unlock()
	if queued {
		modelState(ctx, "model_wait")
	}
	select {
	case <-ctx.Done():
	case <-waiter.ready:
	}
	// Cancellation may race a grant. Reclaim the reserved slot rather than
	// leaking it, including cancellation caused synchronously by an observer.
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		if waiter.granted {
			g.active--
		} else {
			for i, pending := range g.waiters {
				if pending == waiter {
					g.waiters = append(g.waiters[:i], g.waiters[i+1:]...)
					break
				}
			}
		}
		g.grantLocked()
		return err
	}
	return nil
}

// grantLocked reserves slots in arrival order. New arrivals cannot overtake
// queued requests. Each request keeps its configuration snapshot's limit: a
// reduced limit waits for existing requests to drain; it never cancels them.
func (g *modelGate) grantLocked() {
	for len(g.waiters) > 0 {
		waiter := g.waiters[0]
		if waiter.ctx.Err() != nil {
			g.waiters[0] = nil
			g.waiters = g.waiters[1:]
			continue
		}
		if g.active >= waiter.limit {
			return
		}
		g.waiters[0] = nil
		g.waiters = g.waiters[1:]
		g.active++
		waiter.granted = true
		close(waiter.ready)
	}
}
func (g *modelGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active > 0 {
		g.active--
	}
	g.grantLocked()
}

// request retries model HTTP only. No returned tool call is executed until one
// complete response is accepted, and no tool side effect is retried here.
func (p *OpenAIProvider) request(ctx context.Context, payload, output any) error {
	for attempt := 0; ; attempt++ {
		if err := p.gate.acquire(ctx, p.maxConcurrent); err != nil {
			return err
		}
		modelState(ctx, "model")
		err := postJSON(ctx, p.client, p.base+"/chat/completions", p.key, payload, output)
		p.gate.release()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		safe := SafeError(err)
		if !safe.Retryable || attempt >= p.retries {
			return err
		}
		delay := p.backoff * time.Duration(1<<attempt)
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		modelState(ctx, "model_retry")
		slog.Warn("model request retry", "attempt", attempt+1, "max_retries", p.retries, "error_code", safe.Code, "backoff_ms", delay.Milliseconds())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
