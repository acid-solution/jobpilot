package abilitygrading

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const PromptVersion = "jd-ability-level-v3"

var (
	ErrNoJob     = errors.New("no claimable JD ability grading job")
	ErrLeaseLost = errors.New("JD ability grading lease lost")
)

type LevelDefinition struct {
	Level       int    `json:"level"`
	Description string `json:"description"`
}

type Evidence struct {
	RequirementKind string `json:"requirement_kind"`
	Operator        string `json:"operator"`
	RequiredCount   int    `json:"required_count"`
	Quote           string `json:"quote"`
	Qualifier       string `json:"qualifier,omitempty"`
}

type Ability struct {
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	Levels    []LevelDefinition `json:"levels"`
	Evidences []Evidence        `json:"evidences"`
}

type Input struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	TargetID         uuid.UUID
	JobDescriptionID uuid.UUID
	LeaseToken       uuid.UUID
	Attempts         int
	MaxAttempts      int
	Title            string
	Responsibilities []string
	RawText          string
	Abilities        []Ability
}

type Assessment struct {
	AbilityCode     string  `json:"ability_code"`
	Level           int     `json:"level"`
	Source          string  `json:"source"`
	RequirementKind string  `json:"requirement_kind"`
	EvidenceQuote   string  `json:"evidence_quote"`
	Reason          string  `json:"reason"`
	Confidence      float64 `json:"confidence"`
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

func (w *Worker) Run(ctx context.Context) {
	if !w.enabled {
		slog.Info("JD ability grading worker disabled")
		return
	}
	if w.apiKey == "" {
		_ = w.repository.SetConfigurationBlocked(ctx, true)
		slog.Warn("JD ability grading worker blocked: platform key is not configured")
		return
	}
	_ = w.repository.SetConfigurationBlocked(ctx, false)
	go w.recoveryLoop(ctx)
	ticker := time.NewTicker(w.pollEvery)
	defer ticker.Stop()
	for {
		if err := w.runOne(ctx); err != nil && !errors.Is(err, ErrNoJob) && !errors.Is(err, context.Canceled) {
			slog.Error("JD ability grading failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) recoveryLoop(ctx context.Context) {
	_, _ = w.repository.RecoverExpired(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = w.repository.RecoverExpired(ctx)
		}
	}
}

func (w *Worker) runOne(ctx context.Context) error {
	job, err := w.repository.Claim(ctx, w.lease)
	if err != nil {
		return err
	}
	callContext, cancel := context.WithCancel(ctx)
	defer cancel()
	lost := make(chan struct{}, 1)
	go func() {
		ticker := time.NewTicker(w.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-callContext.Done():
				return
			case <-ticker.C:
				if err := w.repository.Heartbeat(callContext, job, w.lease); err != nil {
					select {
					case lost <- struct{}{}:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	result, gradeErr := w.grader.GradeJDAbilities(callContext, w.apiKey, w.model, job)
	select {
	case <-lost:
		return ErrLeaseLost
	default:
	}
	if gradeErr != nil {
		return w.repository.Fail(ctx, job, gradeErr.Error(), job.Attempts < job.MaxAttempts)
	}
	return w.repository.Complete(ctx, job, result)
}
