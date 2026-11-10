package framework

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelDispatchPreservesResultsAndSerializesOtherTools(t *testing.T) {
	for _, name := range []string{"agent_dispatch", "other"} {
		t.Run(name, func(t *testing.T) {
			repo := newFakeRepo()
			a := fixtureAgent("parent")
			a.EnabledTools = []string{name}
			repo.agents[a.ID] = a
			tools := NewToolRegistry()
			var active, max atomic.Int32
			ready := make(chan struct{}, 2)
			err := tools.Register(ToolSpec{Name: name, Description: "test", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"string"}`)}, func(ctx context.Context, _ ExecutionContext, raw json.RawMessage) (any, error) {
				n := active.Add(1)
				defer active.Add(-1)
				for {
					v := max.Load()
					if n <= v || max.CompareAndSwap(v, n) {
						break
					}
				}
				if name == "agent_dispatch" {
					ready <- struct{}{}
					select {
					case <-ready:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				time.Sleep(30 * time.Millisecond)
				return string(raw), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			rt := NewRuntime(repo, tools, map[string]ModelProvider{"mock": modelFunc(func(_ context.Context, r ModelRequest) (ModelResponse, error) {
				if r.Messages[len(r.Messages)-1].Role == "tool" {
					n := len(r.Messages)
					if r.Messages[n-2].ToolCallID != "a" || r.Messages[n-1].ToolCallID != "b" {
						t.Error("tool results out of order")
					}
					return final("done"), nil
				}
				return calls(call("a", name, `{"id":"a"}`), call("b", name, `{"id":"b"}`)), nil
			})})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = rt.Run(ctx, "owner", RunRequest{AgentID: a.ID, Trigger: UserMessage, Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			want := int32(1)
			if name == "agent_dispatch" {
				want = 2
			}
			if max.Load() != want {
				t.Fatalf("max concurrency %d, want %d", max.Load(), want)
			}
		})
	}
}
func TestWorkflowGateCancellationAndDifferentWorkflow(t *testing.T) {
	r := NewRuntime(nil, nil, nil)
	ctx := context.Background()
	s := Scope{OwnerID: "owner", WorkflowID: "one"}
	release, e := r.lockWorkflow(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	c, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, e = r.lockWorkflow(c, s); e == nil {
		t.Fatal("same workflow did not wait")
	}
	other, e := r.lockWorkflow(ctx, Scope{OwnerID: "owner", WorkflowID: "two"})
	if e != nil {
		t.Fatal(e)
	}
	other()
	release()
	next, e := r.lockWorkflow(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	next()
}
func TestWorkflowRunsSameIDSerialDifferentIDsParallel(t *testing.T) {
	repo := &multiWorkflowRepo{fakeRepo: newFakeRepo()}
	a := fixtureAgent("worker")
	a.Limits.MaxConcurrent = 8
	repo.agents[a.ID] = a
	var mu sync.Mutex
	active := map[string]int{}
	maxTotal := 0
	total := 0
	rt := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": modelFunc(func(ctx context.Context, _ ModelRequest) (ModelResponse, error) {
		key := ctx.Value(heldWorkflowKey{}).(string)
		mu.Lock()
		active[key]++
		total++
		if active[key] > 1 {
			t.Error("same workflow overlapped")
		}
		if total > maxTotal {
			maxTotal = total
		}
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		active[key]--
		total--
		mu.Unlock()
		return final("done"), nil
	})})
	var wg sync.WaitGroup
	for _, wf := range []string{"one", "one", "two", "two"} {
		wg.Add(1)
		go func(wf string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(WorkflowContext(context.Background(), "owner", wf), time.Second)
			defer cancel()
			if _, e := rt.Run(ctx, "owner", RunRequest{AgentID: a.ID, Trigger: WorkflowEvent, Input: json.RawMessage(`{}`)}); e != nil {
				t.Error(e)
			}
		}(wf)
	}
	wg.Wait()
	if maxTotal != 2 {
		t.Fatal("independent workflows did not overlap", maxTotal)
	}
}

type multiWorkflowRepo struct{ *fakeRepo }

func (r *multiWorkflowRepo) GetWorkflow(_ context.Context, owner, id string) (Workflow, error) {
	return Workflow{ID: id, OwnerID: owner, Status: "active", Version: 1}, nil
}

type changingWorkflowRepo struct {
	*fakeRepo
	wm      sync.Mutex
	version int
}

func (r *changingWorkflowRepo) GetWorkflow(_ context.Context, owner, id string) (Workflow, error) {
	r.wm.Lock()
	defer r.wm.Unlock()
	return Workflow{ID: id, OwnerID: owner, Status: "active", Version: r.version}, nil
}
func TestQueuedWorkflowRevalidatesVersion(t *testing.T) {
	repo := &changingWorkflowRepo{fakeRepo: newFakeRepo(), version: 1}
	a := fixtureAgent("worker")
	repo.agents[a.ID] = a
	started := make(chan struct{})
	release := make(chan struct{})
	queued := make(chan struct{}, 2)
	var modelCalls atomic.Int32
	rt := NewRuntime(repo, NewToolRegistry(), map[string]ModelProvider{"mock": modelFunc(func(ctx context.Context, _ ModelRequest) (ModelResponse, error) {
		if modelCalls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ModelResponse{}, ctx.Err()
			}
		}
		return final("done"), nil
	})})
	rt.Observer = func(_ context.Context, _ RunRecord, state, _ string) {
		if state == "queued" {
			queued <- struct{}{}
		}
	}
	ctx, cancel := context.WithTimeout(WorkflowContext(context.Background(), "owner", "wf"), 2*time.Second)
	defer cancel()
	request := RunRequest{AgentID: a.ID, Trigger: WorkflowEvent, Input: json.RawMessage(`{}`)}
	first := make(chan error, 1)
	go func() { _, e := rt.Run(ctx, "owner", request); first <- e }()
	<-started
	<-queued
	second := make(chan error, 1)
	go func() { _, e := rt.Run(ctx, "owner", request); second <- e }()
	<-queued
	repo.wm.Lock()
	repo.version = 2
	repo.wm.Unlock()
	close(release)
	if e := <-first; e != nil {
		t.Fatal(e)
	}
	requireCode(t, <-second, "CONFLICT")
	if modelCalls.Load() != 1 {
		t.Fatal("queued run executed stale workflow")
	}
}
