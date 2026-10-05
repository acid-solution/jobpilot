// Package workerpool runs concurrent consumers with one recovery loop per queue.
package workerpool

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type Options struct {
	Name             string
	Concurrency      int
	PollInterval     time.Duration
	RecoveryInterval time.Duration
	// Process returns true after claiming work, so the consumer can drain the queue.
	Process func(context.Context) (bool, error)
	Recover func(context.Context) error
}

// Run blocks until cancellation and until all consumers and recovery have exited.
// Process and Recover must honor their context. Shared dependencies must be safe
// for concurrent calls; each claimed task keeps its own lease and heartbeat.
func Run(ctx context.Context, options Options) {
	if options.Concurrency < 1 || options.PollInterval <= 0 || options.RecoveryInterval <= 0 || options.Process == nil || options.Recover == nil {
		panic("workerpool: invalid options")
	}
	if ctx.Err() != nil {
		return
	}
	recoverJobs := func() {
		if err := options.Recover(ctx); err != nil && ctx.Err() == nil {
			slog.Error("recover worker queue", "queue", options.Name, "error", err)
		}
	}
	recoverJobs()
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(options.RecoveryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() == nil {
					recoverJobs()
				}
			}
		}
	}()
	for index := range options.Concurrency {
		group.Add(1)
		go func() {
			defer group.Done()
			timer := time.NewTimer(options.PollInterval)
			defer timer.Stop()
			for ctx.Err() == nil {
				worked, err := options.Process(ctx)
				if err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
					slog.Error("process worker queue", "queue", options.Name, "worker", index+1, "error", err)
				}
				if worked && err == nil {
					continue
				}
				timer.Reset(options.PollInterval)
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
			}
		}()
	}
	slog.Info("worker queue started", "queue", options.Name, "workers", options.Concurrency)
	group.Wait()
	slog.Info("worker queue stopped", "queue", options.Name)
}
