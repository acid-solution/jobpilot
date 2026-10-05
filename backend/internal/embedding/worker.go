package embedding

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/workerpool"
	"github.com/google/uuid"
)

var ErrNoJob = errors.New("no embedding job")
var ErrLeaseLost = errors.New("embedding lease lost")

type Job struct {
	ID, SourceID, LeaseToken uuid.UUID
	SourceType, SourceHash   string
	UserID                   *uuid.UUID
}
type Source struct {
	Text, GoalSignature, Hash string
	UserID                    *uuid.UUID
}
type VectorChunk struct {
	Chunk
	Vector []float32
}

type Repository interface {
	RecoverExpired(context.Context) error
	Claim(context.Context) (Job, error)
	Heartbeat(context.Context, Job) error
	Source(context.Context, Job) (Source, error)
	Complete(context.Context, Job, Source, []VectorChunk) error
	Fail(context.Context, Job, string) error
}

type Worker struct {
	repository Repository
	client     *Client
}

func NewWorker(repository Repository, client *Client) *Worker {
	return &Worker{repository: repository, client: client}
}

func (w *Worker) Run(ctx context.Context, concurrency int) {
	if !w.client.Configured() {
		slog.Warn("embedding worker disabled: platform key or endpoint missing")
		return
	}
	workerpool.Run(ctx, workerpool.Options{
		Name: "embedding", Concurrency: concurrency, PollInterval: 2 * time.Second,
		RecoveryInterval: time.Minute, Recover: w.repository.RecoverExpired,
		Process: func(ctx context.Context) (bool, error) {
			job, err := w.repository.Claim(ctx)
			if errors.Is(err, ErrNoJob) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			w.runOne(ctx, job)
			return true, nil
		},
	})
}

func (w *Worker) runOne(ctx context.Context, job Job) {
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				if err := w.repository.Heartbeat(jobCtx, job); err != nil {
					cancel()
					heartbeatDone <- err
					return
				}
			}
		}
	}()
	source, err := w.repository.Source(jobCtx, job)
	var chunks []VectorChunk
	if err == nil {
		parts := Split(source.Text)
		if job.SourceType == "ability" {
			parts = []Chunk{{Start: 0, End: len([]rune(source.Text)), Text: source.Text}}
		}
		chunks = make([]VectorChunk, 0, len(parts))
		for start := 0; start < len(parts); start += 10 {
			end := start + 10
			if end > len(parts) {
				end = len(parts)
			}
			texts := make([]string, end-start)
			for i := start; i < end; i++ {
				texts[i-start] = parts[i].Text
			}
			vectors, embedErr := w.client.Embed(jobCtx, texts)
			if embedErr != nil {
				err = embedErr
				break
			}
			for i, vector := range vectors {
				chunks = append(chunks, VectorChunk{Chunk: parts[start+i], Vector: vector})
			}
		}
	}
	cancel()
	heartbeatErr := <-heartbeatDone
	if errors.Is(heartbeatErr, ErrLeaseLost) {
		return
	}
	if err == nil {
		err = heartbeatErr
	}
	if err == nil {
		err = w.repository.Complete(ctx, job, source, chunks)
	}
	if err != nil && !errors.Is(err, ErrLeaseLost) {
		slog.Warn("embedding job failed", "job_id", job.ID, "error", err)
		_ = w.repository.Fail(ctx, job, "embedding_failed")
	}
}
