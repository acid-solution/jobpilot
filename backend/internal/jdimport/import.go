package jdimport

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/LeoninCS/jobpilot-next/backend/internal/workerpool"
	"github.com/google/uuid"
)

var (
	ErrNotFound  = errors.New("JD import not found")
	ErrConflict  = errors.New("JD import not ready or selection invalid")
	ErrNoJob     = errors.New("no JD import job")
	ErrLeaseLost = errors.New("JD import lease lost")
)

type Candidate struct {
	ID        uuid.UUID  `json:"id"`
	Title     string     `json:"title"`
	Company   string     `json:"company"`
	RawText   string     `json:"raw_text"`
	BlockIDs  []string   `json:"block_ids"`
	JDID      *uuid.UUID `json:"jd_id,omitempty"`
	Duplicate bool       `json:"duplicate,omitempty"`
}

type Session struct {
	ID            uuid.UUID   `json:"id"`
	TargetID      uuid.UUID   `json:"target_id"`
	ReplacementID *uuid.UUID  `json:"replacement_jd_id,omitempty"`
	Status        string      `json:"status"`
	Candidates    []Candidate `json:"candidates"`
	ErrorCode     string      `json:"error_code,omitempty"`
	HasPending    bool        `json:"has_pending"`
	NextPollMS    int         `json:"next_poll_ms"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type Job struct {
	ID, UserID, TargetID, LeaseToken uuid.UUID
	RawText                          string
	Attempts                         int
}

type Repository interface {
	WindowRepository
	Create(context.Context, uuid.UUID, uuid.UUID, string, *uuid.UUID) (Session, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (Session, error)
	List(context.Context, uuid.UUID, uuid.UUID) ([]Session, error)
	Confirm(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) (Session, error)
	Retry(context.Context, uuid.UUID, uuid.UUID) (Session, error)
	Claim(context.Context) (Job, error)
	Heartbeat(context.Context, Job) error
	Recover(context.Context) error
	Complete(context.Context, Job, []Candidate) error
	Fail(context.Context, Job, string, bool) error
}
type TargetReader interface {
	Current(context.Context, uuid.UUID) (target.Target, error)
}
type Detector interface {
	DetectJDs(context.Context, string, string, string) ([]Candidate, error)
}

type WindowRepository interface {
	LoadWindow(context.Context, Job, int) ([]Candidate, bool, error)
	SaveWindow(context.Context, Job, int, []Candidate) error
}
type WindowCheckpoints struct {
	Load func(int) ([]Candidate, bool, error)
	Save func(int, []Candidate) error
}
type windowKey struct{}

func WithWindows(ctx context.Context, value WindowCheckpoints) context.Context {
	return context.WithValue(ctx, windowKey{}, value)
}
func WindowsFrom(ctx context.Context) (WindowCheckpoints, bool) {
	value, ok := ctx.Value(windowKey{}).(WindowCheckpoints)
	return value, ok
}

type Service struct {
	Repo    Repository
	Targets TargetReader
}

func (s *Service) Start(ctx context.Context, user uuid.UUID, raw string, replacement *uuid.UUID) (Session, error) {
	if err := market.ValidateJDText(raw); err != nil {
		return Session{}, err
	}
	t, err := s.Targets.Current(ctx, user)
	if err != nil {
		return Session{}, err
	}
	if t.CatalogStatus != target.CatalogStatusValid {
		return Session{}, target.ErrNeedsReselection
	}
	return s.Repo.Create(ctx, user, t.ID, raw, replacement)
}
func (s *Service) Get(ctx context.Context, user, id uuid.UUID) (Session, error) {
	return s.Repo.Get(ctx, user, id)
}
func (s *Service) List(ctx context.Context, user uuid.UUID) ([]Session, error) {
	t, e := s.Targets.Current(ctx, user)
	if e != nil {
		return nil, e
	}
	return s.Repo.List(ctx, user, t.ID)
}
func (s *Service) Confirm(ctx context.Context, user, id uuid.UUID, ids []uuid.UUID) (Session, error) {
	if len(ids) == 0 || len(ids) > 50 {
		return Session{}, ErrConflict
	}
	return s.Repo.Confirm(ctx, user, id, ids)
}
func (s *Service) Retry(ctx context.Context, user, id uuid.UUID) (Session, error) {
	return s.Repo.Retry(ctx, user, id)
}

type Worker struct {
	Repo        Repository
	Credentials jdanalysis.CredentialProvider
	Detector    Detector
}

func (w *Worker) Run(ctx context.Context, n int) {
	workerpool.Run(ctx, workerpool.Options{Name: "jd_import", Concurrency: n, PollInterval: 2 * time.Second, RecoveryInterval: time.Minute, Recover: w.Repo.Recover, Process: w.ProcessOnce})
}
func (w *Worker) ProcessOnce(ctx context.Context) (bool, error) {
	job, err := w.Repo.Claim(ctx)
	if errors.Is(err, ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	task := modelusage.For(modelusage.Resource(ctx, job.TargetID, job.ID), job.UserID, job.ID, "jd_import", "user")
	candidates, err, leaseErr := workerpool.RunLeased(task, 30*time.Second,
		func(heartbeatCtx context.Context) error { return w.Repo.Heartbeat(heartbeatCtx, job) },
		func(workCtx context.Context) ([]Candidate, error) {
			workCtx = WithWindows(workCtx, WindowCheckpoints{
				Load: func(index int) ([]Candidate, bool, error) { return w.Repo.LoadWindow(workCtx, job, index) },
				Save: func(index int, candidates []Candidate) error {
					return w.Repo.SaveWindow(workCtx, job, index, candidates)
				},
			})
			creds, err := w.Credentials.Credentials(workCtx, job.UserID)
			if err != nil {
				return nil, err
			}
			return w.Detector.DetectJDs(workCtx, creds.APIKey, creds.Model, job.RawText)
		})
	if leaseErr != nil {
		return true, leaseErr
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err != nil {
		code, retry := "model_configuration_unavailable", false
		var classified *jdanalysis.ClassifiedError
		if errors.As(err, &classified) {
			code, retry = classified.FailureCode, classified.CanRetry
		}
		slog.Warn("JD import failed", "import_id", job.ID, "code", code)
		return true, w.Repo.Fail(ctx, job, code, retry)
	}
	if err = w.Repo.Complete(ctx, job, candidates); err != nil {
		return true, err
	}
	if len(candidates) == 1 {
		_, err = w.Repo.Confirm(ctx, job.UserID, job.ID, []uuid.UUID{candidates[0].ID})
	}
	return true, err
}
