package workerpool

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestLeasedWorkJoinsRenewalBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	entered := make(chan struct{})
	var active atomic.Bool
	value, err, leaseErr := RunLeased(ctx, time.Millisecond, func(ctx context.Context) error {
		active.Store(true)
		defer active.Store(false)
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}, func(ctx context.Context) (int, error) {
		select {
		case <-entered:
			return 42, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	if value != 42 || err != nil || leaseErr != nil || active.Load() {
		t.Fatalf("value=%d work=%v lease=%v active=%v", value, err, leaseErr, active.Load())
	}
}

func TestRenewalFailureCancelsWorkAndSurvivesLateSuccess(t *testing.T) {
	for _, name := range []string{"lease_lost", "database_unavailable"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			failure := errors.New(name)
			_, _, leaseErr := RunLeased(ctx, time.Millisecond, func(context.Context) error { return failure }, func(ctx context.Context) (int, error) {
				<-ctx.Done()
				return 42, nil // Some providers return a result after cancellation.
			})
			if !errors.Is(leaseErr, failure) {
				t.Fatalf("renewal failure lost: %v", leaseErr)
			}
		})
	}
}

func TestRenewalErrorAtStopIsNotMistakenForNormalCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	entered := make(chan struct{})
	failure := errors.New("database disconnected")
	_, _, leaseErr := RunLeased(ctx, time.Millisecond, func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return failure
	}, func(ctx context.Context) (int, error) {
		select {
		case <-entered:
			return 42, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	if !errors.Is(leaseErr, failure) {
		t.Fatalf("stop hid a genuine renewal failure: %v", leaseErr)
	}
}

func TestShutdownAndPanicCannotLeaveHeartbeatRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	_, err, _ := RunLeased(ctx, time.Second, func(context.Context) error { return nil }, func(context.Context) (int, error) {
		cancel()
		return 42, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late provider result turned shutdown into success: %v", err)
	}
	entered, exited := make(chan struct{}), make(chan struct{})
	defer func() {
		if recover() != "work panic" {
			t.Error("work panic was swallowed")
		}
		select {
		case <-exited:
		default:
			t.Error("panic returned before heartbeat exited")
		}
	}()
	_, _, _ = RunLeased(t.Context(), time.Millisecond, func(ctx context.Context) error {
		close(entered)
		defer close(exited)
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context) (int, error) {
		<-entered
		panic("work panic")
	})
}
