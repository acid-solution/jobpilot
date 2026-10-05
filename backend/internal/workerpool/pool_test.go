package workerpool

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for pool")
		var zero T
		return zero
	}
}

func TestConcurrencyAndRecoveryWhileConsumersAreBusy(t *testing.T) {
	for _, count := range []int{1, 2, 4} {
		t.Run(fmt.Sprintf("workers_%d", count), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{}, count+1)
			recovered := make(chan struct{}, 10)
			finished := make(chan struct{})
			var active, maxActive, processed atomic.Int32
			go func() {
				defer close(finished)
				Run(ctx, Options{
					Name: "test", Concurrency: count, PollInterval: time.Millisecond, RecoveryInterval: 5 * time.Millisecond,
					Process: func(ctx context.Context) (bool, error) {
						n := active.Add(1)
						defer active.Add(-1)
						for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
						}
						processed.Add(1)
						started <- struct{}{}
						<-ctx.Done()
						return true, ctx.Err()
					},
					Recover: func(context.Context) error {
						select {
						case recovered <- struct{}{}:
						default:
						}
						return nil
					},
				})
			}()
			for range count {
				receive(t, started)
			}
			receive(t, recovered) // Startup.
			receive(t, recovered) // Periodic recovery must not wait for consumers.
			cancel()
			receive(t, finished)
			if got := processed.Load(); got != int32(count) || maxActive.Load() != int32(count) || active.Load() != 0 {
				t.Fatalf("processed=%d max=%d active=%d count=%d", got, maxActive.Load(), active.Load(), count)
			}
		})
	}
}

func TestCancellationJoinsConsumersAndSingleRecoveryLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	recoverStarted := make(chan struct{})
	releaseRecovery := make(chan struct{})
	finished := make(chan struct{})
	var recoveryCalls, recoveryActive, recoveryMax atomic.Int32
	go func() {
		defer close(finished)
		Run(ctx, Options{
			Name: "join", Concurrency: 2, PollInterval: time.Hour, RecoveryInterval: time.Millisecond,
			Process: func(ctx context.Context) (bool, error) { started <- struct{}{}; <-ctx.Done(); return false, ctx.Err() },
			Recover: func(ctx context.Context) error {
				if recoveryCalls.Add(1) == 1 {
					return nil
				}
				n := recoveryActive.Add(1)
				defer recoveryActive.Add(-1)
				recoveryMax.Store(n)
				close(recoverStarted)
				<-ctx.Done()
				<-releaseRecovery // Represent cleanup after cancellation.
				return ctx.Err()
			},
		})
	}()
	for range 2 {
		receive(t, started)
	}
	receive(t, recoverStarted)
	cancel()
	select {
	case <-finished:
		t.Fatal("pool returned while recovery was still running")
	default:
	}
	close(releaseRecovery)
	receive(t, finished)
	if recoveryCalls.Load() != 2 || recoveryMax.Load() != 1 || recoveryActive.Load() != 0 {
		t.Fatal("recovery ran concurrently or outlived pool")
	}
}

func TestProcessErrorsWaitForPollInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	finished := make(chan struct{})
	var calls atomic.Int32
	go func() {
		defer close(finished)
		Run(ctx, Options{
			Name: "backoff", Concurrency: 1, PollInterval: time.Hour, RecoveryInterval: time.Hour,
			Process: func(context.Context) (bool, error) {
				calls.Add(1)
				started <- struct{}{}
				return true, errors.New("database unavailable")
			},
			Recover: func(context.Context) error { return nil },
		})
	}()
	receive(t, started)
	cancel()
	receive(t, finished)
	if calls.Load() != 1 {
		t.Fatal("error path spun instead of backing off")
	}
}
