package jdanalysis

import (
	"context"
	"errors"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelusage"
	"log/slog"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/workerpool"
	"github.com/google/uuid"
)

const PromptVersion = "jd-source-stages-v18"
const NormalizationPromptVersion = "jd-vector-candidates-v14"
const ClassificationReviewPromptVersion = "job-classification-review-v1"

var (
	ErrNoJob     = errors.New("no claimable analysis job")
	ErrLeaseLost = errors.New("analysis job lease lost")
)

type DocumentType string

const (
	DocumentJobDescription        DocumentType = "job_description"
	DocumentPartialJobDescription DocumentType = "partial_job_description"
	DocumentNonJobDescription     DocumentType = "non_job_description"
	DocumentUnreadable            DocumentType = "unreadable"
)

type ValidationStatus string

const (
	ValidationPending    ValidationStatus = "pending"
	ValidationValid      ValidationStatus = "valid"
	ValidationIncomplete ValidationStatus = "incomplete"
	ValidationInvalid    ValidationStatus = "invalid"
)

type AbilityMention struct {
	Name          string `json:"name"`
	CatalogCode   string `json:"catalog_code,omitempty"`
	Qualifier     string `json:"qualifier,omitempty"`
	Evidence      string `json:"evidence"`
	RequiredLevel *int   `json:"required_level,omitempty"`
}

type AbilityRequirementOperator string

const (
	RequirementSingle   AbilityRequirementOperator = "single"
	RequirementAnyOf    AbilityRequirementOperator = "any_of"
	RequirementAtLeastN AbilityRequirementOperator = "at_least_n"
)

type AbilityRequirementOption struct {
	RawLabel              string
	AbilityName           string
	CatalogCode           string
	Qualifier             string
	Evidence              string
	RequiredLevel         *int
	Candidate             *AbilityCandidate
	NormalizationReason   string
	NormalizationRelation string
	ScopeDetails          []AbilityScopeDetail
}

type AbilityScopeDetail struct {
	Label     string `json:"label"`
	Qualifier string `json:"qualifier"`
	Evidence  string `json:"evidence"`
}

type AbilityCandidate struct {
	CategoryCode          string   `json:"category_code"`
	Aliases               []string `json:"aliases"`
	Definition            string   `json:"definition"`
	Reason                string   `json:"reason"`
	NearestCandidateCodes []string `json:"nearest_candidate_codes"`
}

type AbilityRequirement struct {
	Operator        AbilityRequirementOperator
	RequiredCount   int
	RequirementKind string
	Evidence        string
	Options         []AbilityRequirementOption
}

type SpecialtyOption struct {
	Code           string               `json:"code"`
	Name           string               `json:"name"`
	Definition     string               `json:"definition"`
	IncludeSignals []string             `json:"include_signals"`
	ExcludeSignals []string             `json:"exclude_signals"`
	ConfusedWith   []SpecialtyConfusion `json:"confused_with"`
}

type SpecialtyConfusion struct {
	SpecialtyCode string `json:"specialty_code"`
	Distinction   string `json:"distinction"`
}

type JobCategoryOption struct {
	Code        string            `json:"code"`
	Name        string            `json:"name"`
	Definition  string            `json:"definition,omitempty"`
	Specialties []SpecialtyOption `json:"specialties"`
}

type AbilityOption struct {
	Code           string             `json:"code"`
	Name           string             `json:"name"`
	CategoryCode   string             `json:"category_code,omitempty"`
	CategoryName   string             `json:"category_name,omitempty"`
	Aliases        []string           `json:"aliases"`
	Definition     string             `json:"definition,omitempty"`
	IncludeSignals []string           `json:"include_signals,omitempty"`
	ExcludeSignals []string           `json:"exclude_signals,omitempty"`
	ConfusedWith   []AbilityConfusion `json:"confused_with,omitempty"`
}

type AbilityConfusion struct {
	AbilityCode string `json:"ability_code"`
	Distinction string `json:"distinction"`
}

type AbilityMatch struct {
	Code           string             `json:"code"`
	Name           string             `json:"name"`
	CategoryCode   string             `json:"category_code"`
	Aliases        []string           `json:"aliases"`
	Definition     string             `json:"definition"`
	IncludeSignals []string           `json:"include_signals,omitempty"`
	ExcludeSignals []string           `json:"exclude_signals,omitempty"`
	ConfusedWith   []AbilityConfusion `json:"confused_with,omitempty"`
}

type Catalog struct {
	JobCategories []JobCategoryOption
	Abilities     []AbilityOption
}

type JobClassification struct {
	CategoryCode  string                      `json:"category_code"`
	SpecialtyCode string                      `json:"specialty_code"`
	Relation      string                      `json:"relation"`
	Evidence      string                      `json:"evidence"`
	Reason        string                      `json:"reason"`
	Candidate     *JobClassificationCandidate `json:"candidate,omitempty"`
}

type JobClassificationCandidate struct {
	Scope              string               `json:"scope"`
	CategoryName       string               `json:"category_name,omitempty"`
	CategoryDefinition string               `json:"category_definition,omitempty"`
	SpecialtyName      string               `json:"specialty_name"`
	Definition         string               `json:"definition"`
	IncludeSignals     []string             `json:"include_signals"`
	ExcludeSignals     []string             `json:"exclude_signals"`
	ConfusedWith       []SpecialtyConfusion `json:"confused_with"`
	Reason             string               `json:"reason"`
}

type ClassificationReviewInput struct {
	Title            string              `json:"title"`
	Responsibilities []string            `json:"responsibilities"`
	Classifications  []JobClassification `json:"first_stage_classifications"`
	Catalog          []JobCategoryOption `json:"catalog"`
}

type ClassificationReviewResult struct {
	Decision          string              `json:"decision"`
	Reason            string              `json:"reason"`
	Classifications   []JobClassification `json:"classifications"`
	Provider          string              `json:"-"`
	Model             string              `json:"-"`
	PromptVersion     string              `json:"-"`
	ProviderRequestID string              `json:"-"`
	InputTokens       int                 `json:"-"`
	OutputTokens      int                 `json:"-"`
}

type Result struct {
	Warnings             []string               `json:"warnings,omitempty"`
	VectorNormalized     bool                   `json:"-"`
	AliasProposals       []AbilityAliasProposal `json:"-"`
	DocumentType         DocumentType
	ValidationStatus     ValidationStatus
	ValidationReason     string
	Classifications      []JobClassification
	Title                string
	Company              string
	EmploymentType       string
	Responsibilities     []string
	AbilityMentions      []AbilityMention
	AbilityRequirements  []AbilityRequirement
	Conditions           []string
	Provider             string
	Model                string
	PromptVersion        string
	ClassificationReview *ClassificationReviewResult
}

type AbilityAliasProposal struct {
	CatalogCode, Label, Evidence, Reason string
}

type Job struct {
	ID                     uuid.UUID
	UserID                 uuid.UUID
	TargetID               uuid.UUID
	JobDescriptionID       uuid.UUID
	RawText                string
	Attempts               int
	MaxAttempts            int
	LeaseToken             uuid.UUID
	PreservePreviousResult bool
}

type Repository interface {
	StageRepository
	Catalog(context.Context) (Catalog, error)
	RecoverExpired(context.Context) (int64, error)
	Claim(context.Context, time.Duration) (Job, error)
	Heartbeat(context.Context, Job, time.Duration) error
	Complete(context.Context, Job, Result) error
	Fail(context.Context, Job, string, bool) error
}

type CredentialProvider interface {
	Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error)
}

type Analyzer interface {
	ExtractJD(context.Context, string, string, string) (Result, error)
	ClassifyJD(context.Context, string, string, string, Result, Catalog) ([]JobClassification, error)
}

type ClassificationReviewer interface {
	ReviewJobClassifications(context.Context, string, string, string, ClassificationReviewInput) (ClassificationReviewResult, error)
}

type ClassificationReviewConfig struct {
	Enabled  bool
	Reviewer ClassificationReviewer
	APIKey   string
	Model    string
}

type ClassifiedError struct {
	FailureCode string
	CanRetry    bool
	Cause       error
}

func (e *ClassifiedError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.FailureCode
}

func (e *ClassifiedError) Unwrap() error { return e.Cause }

func NewError(code string, retryable bool, cause error) error {
	return &ClassifiedError{FailureCode: code, CanRetry: retryable, Cause: cause}
}

type Worker struct {
	repository           Repository
	credentials          CredentialProvider
	analyzer             Analyzer
	pollEvery            time.Duration
	heartbeatEvery       time.Duration
	leaseDuration        time.Duration
	recoveryEvery        time.Duration
	classificationReview ClassificationReviewConfig
	normalizer           AbilityNormalizer
}

type AbilityNormalizer interface {
	Normalize(context.Context, uuid.UUID, string, string, Catalog, Result) (Result, error)
}

func (w *Worker) SetNormalizer(value AbilityNormalizer) { w.normalizer = value }

func NewWorker(repository Repository, credentials CredentialProvider, analyzer Analyzer, pollEvery time.Duration, review ...ClassificationReviewConfig) *Worker {
	worker := &Worker{
		repository: repository, credentials: credentials, analyzer: analyzer, pollEvery: pollEvery,
		heartbeatEvery: 30 * time.Second, leaseDuration: 5 * time.Minute, recoveryEvery: time.Minute,
	}
	if len(review) > 0 {
		worker.classificationReview = review[0]
	}
	return worker
}

func (w *Worker) Run(ctx context.Context, concurrency int) {
	workerpool.Run(ctx, workerpool.Options{
		Name: "jd_analysis", Concurrency: concurrency, PollInterval: w.pollEvery,
		RecoveryInterval: w.recoveryEvery, Process: w.ProcessOnce,
		Recover: w.recoverExpired,
	})
}

func (w *Worker) ProcessOnce(ctx context.Context) (bool, error) {
	job, err := w.repository.Claim(ctx, w.leaseDuration)
	if errors.Is(err, ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	taskCtx := modelusage.For(modelusage.Resource(ctx, job.TargetID, job.JobDescriptionID), job.UserID, job.ID, "jd_analysis", "user")
	var credentials modelconfig.Credentials
	result, err, leaseErr := workerpool.RunLeased(taskCtx, w.heartbeatEvery,
		func(heartbeatCtx context.Context) error {
			return w.repository.Heartbeat(heartbeatCtx, job, w.leaseDuration)
		},
		func(workCtx context.Context) (Result, error) {
			var err error
			credentials, err = w.credentials.Credentials(workCtx, job.UserID)
			if err != nil {
				code := "model_configuration_unavailable"
				if errors.Is(err, modelconfig.ErrNotConfigured) {
					code = "model_not_configured"
				}
				return Result{}, NewError(code, false, err)
			}
			catalog, err := w.repository.Catalog(workCtx)
			if err != nil {
				return Result{}, NewError("catalog_unavailable", true, err)
			}
			return w.analyze(workCtx, job, credentials, catalog)
		})
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if errors.Is(leaseErr, ErrLeaseLost) {
		slog.Warn("discard JD analysis result after lease loss", "job_id", job.ID)
		return true, nil
	}
	if leaseErr != nil {
		if failErr := w.repository.Fail(ctx, job, "heartbeat_failed", true); failErr != nil {
			if errors.Is(failErr, ErrLeaseLost) {
				slog.Warn("discard JD analysis after lease loss", "job_id", job.ID)
				return true, nil
			}
			return true, errors.Join(leaseErr, failErr)
		}
		return true, leaseErr
	}
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return true, err
		}
		code, retryable := classify(err)
		slog.Warn("JD analysis failed", "job_id", job.ID, "code", code, "retryable", retryable, "error", err)
		if failErr := w.repository.Fail(ctx, job, code, retryable); failErr != nil {
			if errors.Is(failErr, ErrLeaseLost) {
				slog.Warn("discard JD analysis failure after lease loss", "job_id", job.ID)
				return true, nil
			}
			return true, errors.Join(err, failErr)
		}
		return true, nil
	}
	result.Provider = credentials.Provider
	result.Model = credentials.Model
	if result.PromptVersion == "" {
		result.PromptVersion = PromptVersion
	}
	if err := w.repository.Complete(ctx, job, result); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			slog.Warn("discard JD analysis result after lease loss", "job_id", job.ID)
			return true, nil
		}
		return true, err
	}
	return true, nil
}

func (w *Worker) recoverExpired(ctx context.Context) error {
	count, err := w.repository.RecoverExpired(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		slog.Warn("recovered expired JD analysis jobs", "count", count)
	}
	return nil
}

func classify(err error) (string, bool) {
	var classified *ClassifiedError
	if errors.As(err, &classified) {
		return strings.TrimSpace(classified.FailureCode), classified.CanRetry
	}
	return "model_call_failed", true
}
