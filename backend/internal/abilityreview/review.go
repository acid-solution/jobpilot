package abilityreview

import (
	"context"
	"errors"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"log/slog"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/workerpool"
	"github.com/google/uuid"
)

const PromptVersion = "ability-review-v2"
const AliasPromptVersion = "ability-alias-review-v1"

var (
	ErrNoRequest = errors.New("no claimable ability review")
	ErrLeaseLost = errors.New("ability review lease lost")
	ErrQuota     = errors.New("ability review quota exceeded")
)

type CatalogAbility struct {
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	CategoryCode string   `json:"category_code"`
	CategoryName string   `json:"category_name"`
	Aliases      []string `json:"aliases"`
	Definition   string   `json:"definition,omitempty"`
	Level2       string   `json:"level_2,omitempty"`
	Level3       string   `json:"level_3,omitempty"`
}

type Input struct {
	ID                    uuid.UUID
	UserID                uuid.UUID
	LeaseToken            uuid.UUID
	Attempts              int
	MaxAttempts           int
	CandidateKey          string
	NormalizedName        string
	Name                  string
	CategoryCode          string
	Aliases               []string
	Definition            string
	ApplicationReason     string
	NearestCandidateCodes []string
	Evidence              []string
	Catalog               []CatalogAbility
	ReviewType            string
	TargetAbilityCode     string
}

type Level struct {
	Level       int    `json:"level"`
	Description string `json:"description"`
}

type NewAbility struct {
	Name         string   `json:"name"`
	CategoryCode string   `json:"category_code"`
	Aliases      []string `json:"aliases"`
	Definition   string   `json:"definition"`
	Levels       []Level  `json:"levels"`
}

type Result struct {
	Decision            string     `json:"decision"`
	Reason              string     `json:"reason"`
	ExistingAbilityCode string     `json:"existing_ability_code"`
	NewAbility          NewAbility `json:"new_ability"`
	Provider            string
	Model               string
	PromptVersion       string
	ProviderRequestID   string
	InputTokens         int
	OutputTokens        int
	ContextIndependent  bool `json:"context_independent"`
}

type Reviewer interface {
	ReviewAbility(context.Context, string, string, Input) (Result, error)
}

type Repository interface {
	SetConfigurationBlocked(context.Context, bool) error
	RecoverExpired(context.Context) (int64, error)
	Claim(context.Context, time.Duration) (Input, error)
	Heartbeat(context.Context, Input, time.Duration) error
	ResolveWithoutModel(context.Context, Input) (bool, error)
	ReserveUsage(context.Context, Input, string) (uuid.UUID, time.Time, error)
	Complete(context.Context, Input, Result, uuid.UUID) error
	Fail(context.Context, Input, uuid.UUID, string, bool) error
}

type Worker struct {
	repository Repository
	reviewer   Reviewer
	enabled    bool
	apiKey     string
	model      string
	pollEvery  time.Duration
	lease      time.Duration
	heartbeat  time.Duration
}

func NewWorker(repository Repository, reviewer Reviewer, enabled bool, apiKey, model string, pollEvery time.Duration) *Worker {
	return &Worker{repository: repository, reviewer: reviewer, enabled: enabled, apiKey: apiKey, model: model,
		pollEvery: pollEvery, lease: 5 * time.Minute, heartbeat: 30 * time.Second}
}

func (w *Worker) Run(ctx context.Context, concurrency int) {
	if !w.enabled {
		slog.Info("ability review worker disabled")
		return
	}
	if w.apiKey == "" {
		_ = w.repository.SetConfigurationBlocked(ctx, true)
		slog.Warn("ability review worker blocked: platform key is not configured")
		return
	}
	workerpool.Run(ctx, workerpool.Options{
		Name: "ability_review", Concurrency: concurrency, PollInterval: w.pollEvery,
		RecoveryInterval: time.Minute,
		Recover: func(ctx context.Context) error {
			// Retry startup unblocking after a transient database failure.
			if err := w.repository.SetConfigurationBlocked(ctx, false); err != nil {
				return err
			}
			_, err := w.repository.RecoverExpired(ctx)
			return err
		},
		Process: func(ctx context.Context) (bool, error) {
			err := w.runOne(ctx)
			if errors.Is(err, ErrNoRequest) || errors.Is(err, ErrQuota) {
				return false, nil
			}
			return err == nil, err
		},
	})
}

func (w *Worker) runOne(ctx context.Context) error {
	request, err := w.repository.Claim(ctx, w.lease)
	if err != nil {
		return err
	}
	if resolved, err := w.repository.ResolveWithoutModel(ctx, request); err != nil || resolved {
		return err
	}
	usageID, _, err := w.repository.ReserveUsage(ctx, request, w.model)
	if err != nil {
		return err
	}

	callCtx := ctx
	purpose := "ability_review"
	if request.ReviewType == "alias" {
		purpose = "ability_alias_review"
	}
	callCtx = modelusage.For(callCtx, request.UserID, request.ID, purpose, "platform")
	scope := modelusage.From(callCtx)
	scope.LegacyUsageID = usageID
	callCtx = modelusage.WithScope(callCtx, scope)
	result, reviewErr, heartbeatErr := workerpool.RunLeased(callCtx, w.heartbeat,
		func(heartbeatCtx context.Context) error {
			return w.repository.Heartbeat(heartbeatCtx, request, w.lease)
		},
		func(workCtx context.Context) (Result, error) {
			return w.reviewer.ReviewAbility(workCtx, w.apiKey, w.model, request)
		})
	if errors.Is(heartbeatErr, ErrLeaseLost) {
		return heartbeatErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if heartbeatErr != nil {
		reviewErr = heartbeatErr
	}
	if reviewErr != nil {
		return w.repository.Fail(ctx, request, usageID, errorCode(reviewErr), request.Attempts < request.MaxAttempts)
	}
	return w.repository.Complete(ctx, request, result, usageID)
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, modelusage.ErrUnavailable) {
		return "usage_record_unavailable"
	}
	return err.Error()
}
