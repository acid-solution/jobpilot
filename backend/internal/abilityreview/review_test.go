package abilityreview

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type heartbeatRepository struct {
	Repository
	entered                   chan struct{}
	leaseLost                 bool
	active, completed, failed atomic.Int32
}

func (r *heartbeatRepository) Claim(context.Context, time.Duration) (Input, error) {
	return Input{ID: uuid.New(), LeaseToken: uuid.New(), Attempts: 1, MaxAttempts: 3}, nil
}
func (r *heartbeatRepository) ResolveWithoutModel(context.Context, Input) (bool, error) {
	return false, nil
}
func (r *heartbeatRepository) ReserveUsage(context.Context, Input, string) (uuid.UUID, time.Time, error) {
	return uuid.New(), time.Time{}, nil
}
func (r *heartbeatRepository) Heartbeat(ctx context.Context, _ Input, _ time.Duration) error {
	r.active.Add(1)
	defer r.active.Add(-1)
	close(r.entered)
	if r.leaseLost {
		return ErrLeaseLost
	}
	<-ctx.Done()
	return ctx.Err()
}
func (r *heartbeatRepository) Complete(context.Context, Input, Result, uuid.UUID) error {
	if r.active.Load() != 0 {
		return errors.New("heartbeat still running during completion")
	}
	r.completed.Add(1)
	return nil
}
func (r *heartbeatRepository) Fail(context.Context, Input, uuid.UUID, string, bool) error {
	r.failed.Add(1)
	return nil
}

type reviewerFunc func(context.Context) (Result, error)

func (f reviewerFunc) ReviewAbility(ctx context.Context, _, _ string, _ Input) (Result, error) {
	return f(ctx)
}

func TestReviewJoinsHeartbeatAndDiscardsStaleOrCanceledResult(t *testing.T) {
	for _, kind := range []string{"complete", "lease_lost", "shutdown"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			repo := &heartbeatRepository{entered: make(chan struct{}), leaseLost: kind == "lease_lost"}
			worker := NewWorker(repo, reviewerFunc(func(modelCtx context.Context) (Result, error) {
				select {
				case <-repo.entered:
				case <-modelCtx.Done():
					return Result{}, modelCtx.Err()
				}
				if kind == "shutdown" {
					cancel()
				}
				if kind != "complete" {
					<-modelCtx.Done()
				}
				return Result{}, nil
			}), true, "test", "test", time.Second)
			worker.heartbeat = time.Millisecond
			err := worker.runOne(ctx)
			if kind == "complete" {
				if err != nil || repo.completed.Load() != 1 {
					t.Fatalf("completion=%d err=%v", repo.completed.Load(), err)
				}
			} else {
				want := ErrLeaseLost
				if kind == "shutdown" {
					want = context.Canceled
				}
				if !errors.Is(err, want) || repo.completed.Load() != 0 {
					t.Fatalf("stale completion=%d err=%v", repo.completed.Load(), err)
				}
			}
			if repo.failed.Load() != 0 || repo.active.Load() != 0 {
				t.Fatalf("failed=%d active heartbeat=%d", repo.failed.Load(), repo.active.Load())
			}
		})
	}
}
