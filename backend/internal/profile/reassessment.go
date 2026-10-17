package profile

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/LeoninCS/jobpilot-next/backend/internal/workerpool"
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
func (w *ReassessmentWorker) Run(ctx context.Context, concurrency int) {
	workerpool.Run(ctx, workerpool.Options{
		Name: "material_reassessment", Concurrency: concurrency, PollInterval: 3 * time.Second,
		RecoveryInterval: time.Minute, Recover: w.repo.RecoverReassessment,
		Process: func(ctx context.Context) (bool, error) {
			ready, err := w.repo.EmbeddingsReady(ctx)
			if err != nil {
				return false, err
			}
			if !ready {
				return false, nil
			}
			job, err := w.repo.ClaimReassessment(ctx)
			if errors.Is(err, ErrNoReassessmentJob) {
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
func (w *ReassessmentWorker) runOne(ctx context.Context, job ReassessmentJob) {
	taskCtx := modelusage.For(ctx, job.UserID, job.ID, "material_analysis", "user")
	drafts, err, heartbeatErr := workerpool.RunLeased(taskCtx, 30*time.Second,
		func(heartbeatCtx context.Context) error { return w.repo.HeartbeatReassessment(heartbeatCtx, job) },
		func(workCtx context.Context) ([]EvidenceDraft, error) {
			material, inputs, err := w.repo.LoadReassessment(workCtx, job)
			if err != nil {
				return nil, err
			}
			drafts, err := w.assessor.ExtractEvidence(workCtx, job.UserID, material, inputs)
			if err == nil {
				err = validateEvidenceDrafts(material.Text, inputs, drafts)
			}
			return drafts, err
		})
	if ctx.Err() != nil {
		return
	}
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
