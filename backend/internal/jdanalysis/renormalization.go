package jdanalysis

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

var ErrNoNormalizationJob = errors.New("no JD normalization job")
var ErrNormalizationLeaseLost = errors.New("JD normalization lease lost")
var ErrNormalizationSourceChanged = errors.New("JD normalization source changed")
var ErrNormalizationStateChanged = errors.New("JD normalization option changed during analysis")

type NormalizationJob struct {
	ID, UserID, JobDescriptionID, LeaseToken uuid.UUID
	SourceHash                               string
}

type NormalizationRepository interface {
	RecoverNormalization(context.Context) error
	ClaimNormalization(context.Context) (NormalizationJob, error)
	HeartbeatNormalization(context.Context, NormalizationJob) error
	LoadNormalization(context.Context, NormalizationJob) (Catalog, Result, error)
	CompleteNormalization(context.Context, NormalizationJob, Result) error
	FailNormalization(context.Context, NormalizationJob, string) error
	EmbeddingsReady(context.Context) (bool, error)
}

type NormalizationWorker struct {
	repo        NormalizationRepository
	credentials CredentialProvider
	normalizer  AbilityNormalizer
}

func NewNormalizationWorker(repo NormalizationRepository, credentials CredentialProvider, normalizer AbilityNormalizer) *NormalizationWorker {
	return &NormalizationWorker{repo: repo, credentials: credentials, normalizer: normalizer}
}

func (w *NormalizationWorker) Run(ctx context.Context) {
	if err := w.repo.RecoverNormalization(ctx); err != nil {
		slog.Error("recover JD normalization", "error", err)
	}
	claim := time.NewTicker(3 * time.Second)
	recovery := time.NewTicker(time.Minute)
	defer claim.Stop()
	defer recovery.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-recovery.C:
			if err := w.repo.RecoverNormalization(ctx); err != nil {
				slog.Error("recover JD normalization", "error", err)
			}
		case <-claim.C:
			ready, err := w.repo.EmbeddingsReady(ctx)
			if err != nil {
				slog.Error("check JD normalization vectors", "error", err)
				continue
			}
			if !ready {
				continue
			}
			job, err := w.repo.ClaimNormalization(ctx)
			if errors.Is(err, ErrNoNormalizationJob) {
				continue
			}
			if err != nil {
				slog.Error("claim JD normalization", "error", err)
				continue
			}
			w.runOne(ctx, job)
		}
	}
}

func (w *NormalizationWorker) runOne(ctx context.Context, job NormalizationJob) {
	taskCtx, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan error, 1)
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-taskCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				if err := w.repo.HeartbeatNormalization(taskCtx, job); err != nil {
					cancel()
					heartbeatDone <- err
					return
				}
			}
		}
	}()
	catalog, input, err := w.repo.LoadNormalization(taskCtx, job)
	credentialUnavailable := false
	if err == nil {
		creds, e := w.credentials.Credentials(taskCtx, job.UserID)
		err = e
		credentialUnavailable = e != nil
		if err == nil {
			input, err = w.normalizer.Normalize(taskCtx, job.UserID, creds.APIKey, creds.Model, catalog, input)
		}
	}
	cancel()
	heartbeatErr := <-heartbeatDone
	if errors.Is(heartbeatErr, ErrNormalizationLeaseLost) {
		return
	}
	if err == nil {
		err = heartbeatErr
	}
	if err == nil {
		err = w.repo.CompleteNormalization(ctx, job, input)
	}
	if err != nil && !errors.Is(err, ErrNormalizationLeaseLost) {
		slog.Warn("JD normalization failed", "job_id", job.ID, "error", err)
		code := "normalization_failed"
		if errors.Is(err, ErrNormalizationSourceChanged) {
			code = "source_changed"
		} else if errors.Is(err, ErrNormalizationStateChanged) {
			code = "state_changed"
		} else if credentialUnavailable {
			code = "credential_unavailable"
		}
		_ = w.repo.FailNormalization(ctx, job, code)
	}
}
