package workerpool

import (
	"context"
	"errors"
	"sync"
	"time"
)

// RunLeased renews an already claimed job while work executes. A renewal error
// cancels work; work must honor cancellation. Before returning (even on panic),
// it stops and joins the heartbeat. Cancellation caused by that stop is normal;
// a genuine renewal failure, including lease loss, is returned separately.
//
// Callers keep their own error policy and fenced completion transaction. They
// must discard results on lease loss, and must not publish on parent cancellation.
func RunLeased[T any](ctx context.Context, interval time.Duration, renew func(context.Context) error, work func(context.Context) (T, error)) (T, error, error) {
	if interval <= 0 || renew == nil || work == nil {
		panic("workerpool: invalid lease execution")
	}
	taskCtx, cancelTask := context.WithCancel(ctx)
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				done <- nil
				return
			case <-ticker.C:
				if heartbeatCtx.Err() != nil {
					done <- nil
					return
				}
				if err := renew(heartbeatCtx); err != nil {
					if heartbeatCtx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
						done <- nil
						return
					}
					cancelTask()
					done <- err
					return
				}
			}
		}
	}()
	stop := sync.OnceValue(func() error {
		cancelHeartbeat()
		cancelTask()
		return <-done
	})
	defer stop()
	var result T
	if err := ctx.Err(); err != nil {
		return result, err, stop()
	}
	result, err := work(taskCtx)
	leaseErr := stop()
	if ctx.Err() != nil {
		err = ctx.Err() // A late provider result cannot turn shutdown into success.
	}
	return result, err, leaseErr
}
