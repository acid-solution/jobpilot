package profile

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

var ErrNoReassessmentJob = errors.New("no material reassessment job")
var ErrReassessmentLeaseLost = errors.New("material reassessment lease lost")
var ErrReassessmentSourceChanged = errors.New("material reassessment source changed")

type ReassessmentJob struct {
	ID, UserID, MaterialID, LeaseToken uuid.UUID
	SourceHash                         string
}
type ReassessmentRepository interface {
	EmbeddingsReady(context.Context) (bool, error)
	RecoverReassessment(context.Context) error
	ClaimReassessment(context.Context) (ReassessmentJob, error)
	HeartbeatReassessment(context.Context, ReassessmentJob) error
	LoadReassessment(context.Context, ReassessmentJob) (Material, []CapabilityInput, error)
	CompleteReassessment(context.Context, ReassessmentJob, []EvidenceDraft) error
	FailReassessment(context.Context, ReassessmentJob, string) error
}
type ReassessmentWorker struct {
	repo     ReassessmentRepository
	assessor Assessor
}

func NewReassessmentWorker(repo ReassessmentRepository, assessor Assessor) *ReassessmentWorker {
	return &ReassessmentWorker{repo: repo, assessor: assessor}
}
func (w *ReassessmentWorker) Run(ctx context.Context) {
	_ = w.repo.RecoverReassessment(ctx)
	claim := time.NewTicker(3 * time.Second)
	defer claim.Stop()
	recovery := time.NewTicker(time.Minute)
	defer recovery.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-recovery.C:
			if err := w.repo.RecoverReassessment(ctx); err != nil {
				slog.Error("recover material reassessment", "error", err)
			}
		case <-claim.C:
			ready, err := w.repo.EmbeddingsReady(ctx)
			if err != nil {
				slog.Error("check ability embeddings", "error", err)
				continue
			}
			if !ready {
				continue
			}
			job, err := w.repo.ClaimReassessment(ctx)
			if errors.Is(err, ErrNoReassessmentJob) {
				continue
			}
			if err != nil {
				slog.Error("claim material reassessment", "error", err)
				continue
			}
			w.runOne(ctx, job)
		}
	}
}
func (w *ReassessmentWorker) runOne(ctx context.Context, job ReassessmentJob) {
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-taskCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				if err := w.repo.HeartbeatReassessment(taskCtx, job); err != nil {
					cancel()
					heartbeatDone <- err
					return
				}
			}
		}
	}()
	material, inputs, err := w.repo.LoadReassessment(taskCtx, job)
	var drafts []EvidenceDraft
	if err == nil {
		drafts, err = w.assessor.ExtractEvidence(taskCtx, job.UserID, material, inputs)
	}
	if err == nil {
		err = validateEvidenceDrafts(material.Text, inputs, drafts)
	}
	cancel()
	heartbeatErr := <-heartbeatDone
	if errors.Is(heartbeatErr, ErrReassessmentLeaseLost) {
		return
	}
	if err == nil {
		err = heartbeatErr
	}
	if err == nil {
		err = w.repo.CompleteReassessment(ctx, job, drafts)
	}
	if err != nil && !errors.Is(err, ErrReassessmentLeaseLost) {
		slog.Warn("material reassessment failed", "job_id", job.ID, "error", err)
		code := "reassessment_failed"
		if errors.Is(err, ErrReassessmentSourceChanged) {
			code = "source_changed"
		}
		_ = w.repo.FailReassessment(ctx, job, code)
	}
}
