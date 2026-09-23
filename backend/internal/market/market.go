package market

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("job description not found")
	ErrValidation    = errors.New("invalid job description")
	ErrInvalidJDText = errors.New("invalid JD text")
	ErrDuplicateJD   = errors.New("duplicate job description")
)

const MaxBatchSize = 50

type AbilityReviewQuotaError struct{ NextAvailableAt time.Time }

func (e *AbilityReviewQuotaError) Error() string { return "ability review quota exceeded" }

type Status string

const (
	StatusProcessing Status = "processing"
	StatusIncluded   Status = "included"
	StatusReference  Status = "reference"
	StatusExcluded   Status = "excluded"
	StatusFailed     Status = "failed"
)

type JobDescription struct {
	ID                    uuid.UUID                   `json:"id"`
	TargetID              uuid.UUID                   `json:"target_id"`
	Title                 string                      `json:"title,omitempty"`
	Company               string                      `json:"company,omitempty"`
	Status                Status                      `json:"status"`
	PrimaryCategory       string                      `json:"primary_category,omitempty"`
	SecondaryCategory     string                      `json:"secondary_category,omitempty"`
	Reason                string                      `json:"reason,omitempty"`
	Conditions            string                      `json:"conditions,omitempty"`
	EmploymentType        string                      `json:"employment_type,omitempty"`
	Responsibilities      []string                    `json:"responsibilities"`
	AbilityMentions       []jdanalysis.AbilityMention `json:"ability_mentions"`
	AbilityLevels         []JDAbilityLevel            `json:"ability_levels"`
	AnalysisProvider      string                      `json:"analysis_provider,omitempty"`
	AnalysisModel         string                      `json:"analysis_model,omitempty"`
	AnalysisPromptVersion string                      `json:"analysis_prompt_version,omitempty"`
	DocumentType          jdanalysis.DocumentType     `json:"document_type,omitempty"`
	ValidationStatus      jdanalysis.ValidationStatus `json:"validation_status"`
	ValidationReason      string                      `json:"validation_reason,omitempty"`
	RawText               string                      `json:"raw_text"`
	JobStatus             string                      `json:"job_status"`
	JobErrorCode          string                      `json:"job_error_code,omitempty"`
	CreatedAt             time.Time                   `json:"created_at"`
	UpdatedAt             time.Time                   `json:"updated_at"`
	AbilityReview         AbilityReviewSummary        `json:"ability_review"`
	AbilityGrading        AbilityGradingSummary       `json:"ability_grading"`
}

type AbilityReviewSummary struct {
	PendingCount  int        `json:"pending_count"`
	FailedCount   int        `json:"failed_count"`
	NextAttemptAt *time.Time `json:"next_attempt_at"`
	Blocked       bool       `json:"blocked"`
}

type AbilityGradingSummary struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type AbilityEvidence struct {
	JobDescriptionID uuid.UUID `json:"job_description_id"`
	JobTitle         string    `json:"job_title"`
	Evidence         string    `json:"evidence"`
	Qualifier        string    `json:"qualifier,omitempty"`
}

type JDAbilityLevel struct {
	AbilityID       uuid.UUID `json:"ability_id"`
	Name            string    `json:"name"`
	Level           int       `json:"level"`
	Source          string    `json:"source"`
	RequirementKind string    `json:"requirement_kind"`
	Evidence        string    `json:"evidence"`
	Reason          string    `json:"reason"`
	Confidence      float64   `json:"confidence"`
}

type LevelDistribution struct {
	Level int `json:"level"`
	Count int `json:"count"`
}

type LevelEvidence struct {
	JobDescriptionID uuid.UUID `json:"job_description_id"`
	JobTitle         string    `json:"job_title"`
	Level            int       `json:"level"`
	Source           string    `json:"source"`
	RequirementKind  string    `json:"requirement_kind"`
	Evidence         string    `json:"evidence"`
	Reason           string    `json:"reason"`
	Confidence       float64   `json:"confidence"`
}

type LevelSummary struct {
	CommonLevels           []int               `json:"common_levels"`
	RecommendedLevel       int                 `json:"recommended_level"`
	SampleCount            int                 `json:"sample_count"`
	ExplicitCount          int                 `json:"explicit_count"`
	InferredCount          int                 `json:"inferred_count"`
	Distribution           []LevelDistribution `json:"distribution"`
	HigherRequirementCount int                 `json:"higher_requirement_count"`
	Status                 string              `json:"status"`
	Evidences              []LevelEvidence     `json:"evidences"`
}

type AbilitySummary struct {
	AbilityID             uuid.UUID         `json:"ability_id"`
	Name                  string            `json:"name"`
	Category              string            `json:"category"`
	CoveredJDCount        int               `json:"covered_jd_count"`
	Evidences             []AbilityEvidence `json:"evidences"`
	TargetLevel           int               `json:"target_level"`
	LevelSummary          LevelSummary      `json:"level_summary"`
	PreferredLevelSummary LevelSummary      `json:"preferred_level_summary"`
}

type Profile struct {
	IncludedJDCount            int              `json:"included_jd_count"`
	RequiredJDCount            int              `json:"required_jd_count"`
	Complete                   bool             `json:"complete"`
	AbilityGradingPendingCount int              `json:"ability_grading_pending_count"`
	AbilityGradingFailedCount  int              `json:"ability_grading_failed_count"`
	Abilities                  []AbilitySummary `json:"abilities"`
}

type ListFilter struct {
	Statuses []Status
	Query    string
	Company  string
	Ability  string
	Category string
}

type BatchItem struct {
	Index        int             `json:"index"`
	Status       string          `json:"status"`
	JD           *JobDescription `json:"jd,omitempty"`
	ExistingID   *uuid.UUID      `json:"existing_jd_id,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
}

type BatchResult struct {
	CreatedCount   int         `json:"created_count"`
	DuplicateCount int         `json:"duplicate_count"`
	InvalidCount   int         `json:"invalid_count"`
	Items          []BatchItem `json:"items"`
}

type DuplicateError struct{ ExistingID uuid.UUID }

func (e *DuplicateError) Error() string { return ErrDuplicateJD.Error() }
func (e *DuplicateError) Unwrap() error { return ErrDuplicateJD }

type Repository interface {
	CreateWithAnalysisJob(context.Context, uuid.UUID, uuid.UUID, string, string) (JobDescription, error)
	ListByTarget(context.Context, uuid.UUID, uuid.UUID, ListFilter) ([]JobDescription, error)
	FindByID(context.Context, uuid.UUID, uuid.UUID) (JobDescription, error)
	UpdateRawText(context.Context, uuid.UUID, uuid.UUID, string, string) (JobDescription, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
	RetryAnalysis(context.Context, uuid.UUID, uuid.UUID) (JobDescription, error)
	RetryAbilityReviews(context.Context, uuid.UUID, uuid.UUID) (JobDescription, error)
	ProfileByTarget(context.Context, uuid.UUID, uuid.UUID) (Profile, error)
}

type CurrentTargetReader interface {
	Current(context.Context, uuid.UUID) (target.Target, error)
}

type Service struct {
	repository    Repository
	targetService CurrentTargetReader
}

func NewService(repository Repository, targetService CurrentTargetReader) *Service {
	return &Service{repository: repository, targetService: targetService}
}

func (s *Service) Submit(ctx context.Context, userID uuid.UUID, rawText string) (JobDescription, error) {
	rawText = canonicalRawText(rawText)
	if err := ValidateJDText(rawText); err != nil {
		return JobDescription{}, err
	}
	currentTarget, err := s.targetService.Current(ctx, userID)
	if err != nil {
		return JobDescription{}, err
	}
	if currentTarget.CatalogStatus != target.CatalogStatusValid {
		return JobDescription{}, target.ErrNeedsReselection
	}
	return s.repository.CreateWithAnalysisJob(ctx, userID, currentTarget.ID, rawText, hashRawText(rawText))
}

func (s *Service) SubmitBatch(ctx context.Context, userID uuid.UUID, rawTexts []string) (BatchResult, error) {
	if len(rawTexts) == 0 || len(rawTexts) > MaxBatchSize {
		return BatchResult{}, ErrValidation
	}
	currentTarget, err := s.targetService.Current(ctx, userID)
	if err != nil {
		return BatchResult{}, err
	}
	if currentTarget.CatalogStatus != target.CatalogStatusValid {
		return BatchResult{}, target.ErrNeedsReselection
	}
	result := BatchResult{Items: make([]BatchItem, 0, len(rawTexts))}
	seen := make(map[string]uuid.UUID)
	for index, input := range rawTexts {
		rawText := canonicalRawText(input)
		if err := ValidateJDText(rawText); err != nil {
			result.InvalidCount++
			result.Items = append(result.Items, BatchItem{Index: index, Status: "invalid", ErrorCode: "invalid_jd_text", ErrorMessage: "请粘贴完整的岗位说明"})
			continue
		}
		hash := hashRawText(rawText)
		if existingID, duplicate := seen[hash]; duplicate {
			result.DuplicateCount++
			result.Items = append(result.Items, BatchItem{Index: index, Status: "duplicate", ExistingID: &existingID})
			continue
		}
		created, err := s.repository.CreateWithAnalysisJob(ctx, userID, currentTarget.ID, rawText, hash)
		if err != nil {
			var duplicate *DuplicateError
			if errors.As(err, &duplicate) {
				result.DuplicateCount++
				result.Items = append(result.Items, BatchItem{Index: index, Status: "duplicate", ExistingID: &duplicate.ExistingID})
				seen[hash] = duplicate.ExistingID
				continue
			}
			return BatchResult{}, err
		}
		result.CreatedCount++
		seen[hash] = created.ID
		createdCopy := created
		result.Items = append(result.Items, BatchItem{Index: index, Status: "created", JD: &createdCopy})
	}
	return result, nil
}

func (s *Service) ListCurrent(ctx context.Context, userID uuid.UUID, filter ListFilter) ([]JobDescription, error) {
	currentTarget, err := s.targetService.Current(ctx, userID)
	if err != nil {
		return nil, err
	}
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Company = strings.TrimSpace(filter.Company)
	filter.Ability = strings.TrimSpace(filter.Ability)
	filter.Category = strings.TrimSpace(filter.Category)
	for _, status := range filter.Statuses {
		if status != StatusProcessing && status != StatusIncluded && status != StatusReference && status != StatusExcluded && status != StatusFailed {
			return nil, ErrValidation
		}
	}
	return s.repository.ListByTarget(ctx, userID, currentTarget.ID, filter)
}

func (s *Service) Detail(ctx context.Context, userID, jdID uuid.UUID) (JobDescription, error) {
	return s.repository.FindByID(ctx, userID, jdID)
}

func (s *Service) Retry(ctx context.Context, userID, jdID uuid.UUID) (JobDescription, error) {
	return s.repository.RetryAnalysis(ctx, userID, jdID)
}

func (s *Service) Update(ctx context.Context, userID, jdID uuid.UUID, rawText string) (JobDescription, error) {
	rawText = canonicalRawText(rawText)
	if err := ValidateJDText(rawText); err != nil {
		return JobDescription{}, err
	}
	return s.repository.UpdateRawText(ctx, userID, jdID, rawText, hashRawText(rawText))
}

func (s *Service) Delete(ctx context.Context, userID, jdID uuid.UUID) error {
	return s.repository.Delete(ctx, userID, jdID)
}

func (s *Service) RetryAbilityReviews(ctx context.Context, userID, jdID uuid.UUID) (JobDescription, error) {
	return s.repository.RetryAbilityReviews(ctx, userID, jdID)
}

func canonicalRawText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func hashRawText(value string) string {
	digest := sha256.Sum256([]byte(canonicalRawText(value)))
	return hex.EncodeToString(digest[:])
}

func (s *Service) ProfileCurrent(ctx context.Context, userID uuid.UUID) (Profile, error) {
	currentTarget, err := s.targetService.Current(ctx, userID)
	if err != nil {
		return Profile{}, err
	}
	return s.repository.ProfileByTarget(ctx, userID, currentTarget.ID)
}
