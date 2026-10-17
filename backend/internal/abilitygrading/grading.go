package abilitygrading

import (
	"context"
	"errors"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"log/slog"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/workerpool"
	"github.com/google/uuid"
)

const PromptVersion = "jd-ability-level-v4-options"

var (
	ErrNoJob     = errors.New("no claimable JD ability grading job")
	ErrLeaseLost = errors.New("JD ability grading lease lost")
)

type LevelDefinition struct {
	Level       int    `json:"level"`
	Description string `json:"description"`
}

type Evidence struct {
	OptionID        uuid.UUID `json:"option_id"`
	RequirementKind string    `json:"requirement_kind"`
	Operator        string    `json:"operator"`
	RequiredCount   int       `json:"required_count"`
	Quote           string    `json:"quote"`
	Qualifier       string    `json:"qualifier,omitempty"`
}

type Ability struct {
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	Levels    []LevelDefinition `json:"levels"`
	Evidences []Evidence        `json:"evidences"`
}

type Input struct {
	ID                uuid.UUID
	UserID            uuid.UUID
	TargetID          uuid.UUID
	JobDescriptionID  uuid.UUID
	LeaseToken        uuid.UUID
	SourceFingerprint string
	Attempts          int
	MaxAttempts       int
	Title             string
	Responsibilities  []string
	RawText           string
	Abilities         []Ability
}

type Assessment struct {
	OptionID        uuid.UUID `json:"option_id"`
	AbilityCode     string    `json:"ability_code"`
	Level           int       `json:"level"`
	Source          string    `json:"source"`
	RequirementKind string    `json:"requirement_kind"`
	EvidenceQuote   string    `json:"evidence_quote"`
	Reason          string    `json:"reason"`
	Confidence      float64   `json:"confidence"`
}

type Result struct {
	Assessments       []Assessment `json:"assessments"`
	Provider          string
	Model             string
	PromptVersion     string
	ProviderRequestID string
	InputTokens       int
	OutputTokens      int
}

type Grader interface {
	GradeJDAbilities(context.Context, string, string, Input) (Result, error)
}

type Repository interface {
	SetConfigurationBlocked(context.Context, bool) error
	RecoverExpired(context.Context) (int64, error)
	Claim(context.Context, time.Duration) (Input, error)
	Heartbeat(context.Context, Input, time.Duration) error
	Complete(context.Context, Input, Result) error
	Fail(context.Context, Input, string, bool) error
}

type Worker struct {
	repository Repository
	grader     Grader
	enabled    bool
	apiKey     string
	model      string
	pollEvery  time.Duration
	lease      time.Duration
	heartbeat  time.Duration
}

func NewWorker(repository Repository, grader Grader, enabled bool, apiKey, model string, pollEvery time.Duration) *Worker {
	return &Worker{
		repository: repository, grader: grader, enabled: enabled, apiKey: apiKey, model: model,
		pollEvery: pollEvery, lease: 5 * time.Minute, heartbeat: 30 * time.Second,
	}
}

func (w *Worker) Run(ctx context.Context, concurrency int) {
	if !w.enabled {
		slog.Info("JD ability grading worker disabled")
		return
	}
	if w.apiKey == "" {
		_ = w.repository.SetConfigurationBlocked(ctx, true)
		slog.Warn("JD ability grading worker blocked: platform key is not configured")
		return
	}
	workerpool.Run(ctx, workerpool.Options{
		Name: "ability_grading", Concurrency: concurrency, PollInterval: w.pollEvery,
		RecoveryInterval: time.Minute,
		Recover: func(ctx context.Context) error {
			if err := w.repository.SetConfigurationBlocked(ctx, false); err != nil {
				return err
			}
			_, err := w.repository.RecoverExpired(ctx)
			return err
		},
		Process: func(ctx context.Context) (bool, error) {
			err := w.runOne(ctx)
			if errors.Is(err, ErrNoJob) {
				return false, nil
			}
			return err == nil, err
		},
	})
}

func (w *Worker) runOne(ctx context.Context) error {
	job, err := w.repository.Claim(ctx, w.lease)
	if err != nil {
		return err
	}
	callContext := modelusage.For(modelusage.Resource(ctx, job.TargetID, job.JobDescriptionID), job.UserID, job.ID, "jd_grading", "platform")
	result, gradeErr, heartbeatErr := workerpool.RunLeased(callContext, w.heartbeat,
		func(heartbeatCtx context.Context) error { return w.repository.Heartbeat(heartbeatCtx, job, w.lease) },
		func(workCtx context.Context) (Result, error) {
			return w.grader.GradeJDAbilities(workCtx, w.apiKey, w.model, job)
		})
	if errors.Is(heartbeatErr, ErrLeaseLost) {
		return heartbeatErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if heartbeatErr != nil {
		gradeErr = heartbeatErr
	}
	if gradeErr != nil {
		return w.repository.Fail(ctx, job, gradeErr.Error(), job.Attempts < job.MaxAttempts)
	}
	return w.repository.Complete(ctx, job, result)
}
