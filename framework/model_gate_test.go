package framework

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func queuedModelAcquire(t *testing.T, gate *modelGate, ctx context.Context, limit int) <-chan error {
	t.Helper()
	queued := make(chan struct{}, 1)
	result := make(chan error, 1)
	ctx = withModelObserver(ctx, func(state string) {
		if state == "model_wait" {
			queued <- struct{}{}
		}
	})
	go func() { result <- gate.acquire(ctx, limit) }()
	select {
	case <-queued:
	case err := <-result:
		if err == nil {
			gate.release()
		}
		t.Fatalf("request did not queue: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("request was not observed queued")
	}
	return result
}
func acquiredModelSlot(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued request did not acquire released slot")
	}
}
func noModelSlot(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("later request overtook queue: %v", err)
	default:
	}
}
func TestModelGateFIFOWithoutNewArrivalBarging(t *testing.T) {
	gate := &modelGate{}
	if err := gate.acquire(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	first := queuedModelAcquire(t, gate, context.Background(), 1)
	second := queuedModelAcquire(t, gate, context.Background(), 1)
	gate.release()
	// Even before the first goroutine resumes, its slot is reserved. A fresh
	// request must join behind the existing second waiter.
	third := queuedModelAcquire(t, gate, context.Background(), 1)
	acquiredModelSlot(t, first)
	noModelSlot(t, second)
	noModelSlot(t, third)
	gate.release()
	acquiredModelSlot(t, second)
	noModelSlot(t, third)
	gate.release()
	acquiredModelSlot(t, third)
	gate.release()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.active != 0 || len(gate.waiters) != 0 {
		t.Fatal("gate leaked state", gate.active, len(gate.waiters))
	}
}
func TestModelGateCancelMiddleAndHeadDoesNotBlockQueue(t *testing.T) {
	for _, position := range []int{0, 1} {
		t.Run(fmt.Sprint(position), func(t *testing.T) {
			gate := &modelGate{}
			if err := gate.acquire(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			contexts := []context.Context{context.Background(), context.Background(), context.Background()}
			contexts[position] = ctx
			pending := []<-chan error{}
			for _, c := range contexts {
				pending = append(pending, queuedModelAcquire(t, gate, c, 1))
			}
			cancel()
			select {
			case err := <-pending[position]:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled waiter did not return")
			}
			for i, result := range pending {
				if i == position {
					continue
				}
				gate.release()
				acquiredModelSlot(t, result)
			}
			gate.release()
			gate.mu.Lock()
			defer gate.mu.Unlock()
			if gate.active != 0 || len(gate.waiters) != 0 {
				t.Fatal("cancelled queue leaked state", gate.active, len(gate.waiters))
			}
		})
	}
}
func TestModelGateCancelGrantedSlotReclaimsReservation(t *testing.T) {
	gate := &modelGate{}
	if err := gate.acquire(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting, continueObserver := make(chan struct{}), make(chan struct{})
	ctx = withModelObserver(ctx, func(state string) {
		if state == "model_wait" {
			close(waiting)
			<-continueObserver
		}
	})
	result := make(chan error, 1)
	go func() { result <- gate.acquire(ctx, 1) }()
	select {
	case <-waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("not queued")
	}
	gate.release() // reserves a slot while the observer has not returned
	cancel()
	close(continueObserver)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("grant cancellation stuck")
	}
	if err := gate.acquire(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	gate.release()
	gate.release() // defensive release must never go negative
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.active != 0 || len(gate.waiters) != 0 {
		t.Fatal("slot reservation leaked", gate.active, len(gate.waiters))
	}
}
func TestModelGateReducedLimitDrainsAndPreservesFIFO(t *testing.T) {
	gate := &modelGate{}
	for i := 0; i < 2; i++ {
		if err := gate.acquire(context.Background(), 2); err != nil {
			t.Fatal(err)
		}
	}
	reduced := queuedModelAcquire(t, gate, context.Background(), 1)
	increased := queuedModelAcquire(t, gate, context.Background(), 2)
	gate.release()
	// Old active work is allowed to complete; a later higher-limit snapshot
	// cannot bypass an earlier waiter from the reduced configuration.
	noModelSlot(t, reduced)
	noModelSlot(t, increased)
	gate.release()
	acquiredModelSlot(t, reduced)
	acquiredModelSlot(t, increased)
	gate.mu.Lock()
	active := gate.active
	gate.mu.Unlock()
	if active != 2 {
		t.Fatal("expected two reserved slots", active)
	}
	gate.release()
	gate.release()
}
func TestModelGateConcurrentAcquireNeverExceedsConfiguredLimit(t *testing.T) {
	gate := sharedModelGate("test-fifo-"+t.Name(), "same-key")
	if sharedModelGate("test-fifo-"+t.Name(), "same-key") != gate {
		t.Fatal("same endpoint/key did not share gate")
	}
	if sharedModelGate("test-fifo-"+t.Name(), "other-key") == gate {
		t.Fatal("different key shared gate")
	}
	const limit = 3
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := gate.acquire(context.Background(), limit); err != nil {
				t.Error(err)
				return
			}
			gate.mu.Lock()
			active := gate.active
			gate.mu.Unlock()
			if active < 1 || active > limit {
				t.Errorf("active %d outside limit %d", active, limit)
			}
			gate.release()
		}()
	}
	wg.Wait()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.active != 0 || len(gate.waiters) != 0 {
		t.Fatal("concurrent requests leaked state", gate.active, len(gate.waiters))
	}
}
