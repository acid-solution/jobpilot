package abilityreview

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const PromptVersion = "ability-review-v2"

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

func (w *Worker) Run(ctx context.Context) {
	if !w.enabled {
		slog.Info("ability review worker disabled")
		return
	}
	if w.apiKey == "" {
		_ = w.repository.SetConfigurationBlocked(ctx, true)
		slog.Warn("ability review worker blocked: platform key is not configured")
		return
	}
	_ = w.repository.SetConfigurationBlocked(ctx, false)
	go w.recoveryLoop(ctx)
	ticker := time.NewTicker(w.pollEvery)
	defer ticker.Stop()
	for {
		if err := w.runOne(ctx); err != nil && !errors.Is(err, ErrNoRequest) && !errors.Is(err, context.Canceled) {
			slog.Error("ability review failed", "error", err)
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

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	lost := make(chan struct{}, 1)
	go func() {
		ticker := time.NewTicker(w.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-callCtx.Done():
				return
			case <-ticker.C:
				if err := w.repository.Heartbeat(callCtx, request, w.lease); err != nil {
					lost <- struct{}{}
					cancel()
					return
				}
			}
		}
	}()
	result, reviewErr := w.reviewer.ReviewAbility(callCtx, w.apiKey, w.model, request)
	select {
	case <-lost:
		return ErrLeaseLost
	default:
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
	return err.Error()
}
