// Package profile manages evidence-backed user capability assessment.
package profile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound         = errors.New("profile resource not found")
	ErrInvalidInput     = errors.New("invalid profile input")
	ErrConflict         = errors.New("profile state conflict")
	ErrPrecondition     = errors.New("profile precondition not satisfied")
	ErrModelUnavailable = errors.New("profile model unavailable")
)

type MaterialType string

const (
	MaterialResume     MaterialType = "resume"
	MaterialExperience MaterialType = "experience"
)

type MaterialStatus string

const (
	MaterialDraft      MaterialStatus = "draft"
	MaterialProcessing MaterialStatus = "processing"
	MaterialReady      MaterialStatus = "ready"
	MaterialFailed     MaterialStatus = "failed"
)

type PracticeMode string

const (
	ModeValidation PracticeMode = "validation"
	ModeReview     PracticeMode = "review"
	ModeInitial    PracticeMode = "initial"
)

type Material struct {
	ID            uuid.UUID      `json:"id"`
	Type          MaterialType   `json:"type"`
	Title         string         `json:"title"`
	Text          string         `json:"text"`
	Status        MaterialStatus `json:"status"`
	FailureReason string         `json:"failure_reason,omitempty"`
	EvidenceCount int            `json:"evidence_count"`
	ConfirmedAt   *time.Time     `json:"confirmed_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}
type MaterialInput struct {
	Type  MaterialType `json:"type"`
	Title string       `json:"title"`
	Text  string       `json:"text"`
}
type LevelDefinition struct {
	Level       int    `json:"level"`
	Description string `json:"description"`
}
type Evidence struct {
	ID          uuid.UUID `json:"id"`
	MaterialID  uuid.UUID `json:"material_id"`
	AbilityID   uuid.UUID `json:"concept_id"`
	AbilityName string    `json:"concept_name"`
	Level       int       `json:"level"`
	Quote       string    `json:"quote"`
	Reason      string    `json:"reason"`
	Confidence  float64   `json:"confidence"`
	CreatedAt   time.Time `json:"created_at"`
}
type Capability struct {
	AbilityID        uuid.UUID         `json:"concept_id"`
	Name             string            `json:"name"`
	Category         string            `json:"category,omitempty"`
	MarketLevel      int               `json:"market_level"`
	MarketLevelReady bool              `json:"market_level_ready"`
	CurrentLevel     int               `json:"current_level"`
	Assessed         bool              `json:"assessed"`
	LevelSource      string            `json:"level_source"`
	ManualUpdatedAt  *time.Time        `json:"manual_updated_at,omitempty"`
	EvidenceLevel    int               `json:"evidence_level"`
	VerifiedLevel    int               `json:"verified_level"`
	Status           string            `json:"status"`
	NeedsValidation  bool              `json:"needs_validation"`
	Levels           []LevelDefinition `json:"levels"`
	Evidence         []Evidence        `json:"evidence"`
	UpdatedAt        *time.Time        `json:"updated_at,omitempty"`
}
type Overview struct {
	MarketProfileReady bool         `json:"market_profile_ready"`
	MaterialCount      int          `json:"material_count"`
	ReadyMaterialCount int          `json:"ready_material_count"`
	AssessedCount      int          `json:"assessed_count"`
	PendingCount       int          `json:"pending_count"`
	BlockingCount      int          `json:"blocking_count"`
	Complete           bool         `json:"complete"`
	Capabilities       []Capability `json:"capabilities"`
}
type Settings struct {
	WeeklyHours        *int       `json:"weekly_hours,omitempty"`
	ExpectedWeeks      *int       `json:"expected_weeks,omitempty"`
	ExistingExperience string     `json:"existing_experience"`
	UpdatedAt          *time.Time `json:"updated_at,omitempty"`
}
type CapabilityInput struct {
	AbilityID   uuid.UUID         `json:"concept_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Levels      []LevelDefinition `json:"levels"`
}
type EvidenceDraft struct {
	AbilityID     uuid.UUID `json:"concept_id"`
	Level         int       `json:"level"`
	Quote         string    `json:"quote"`
	Reason        string    `json:"reason"`
	Confidence    float64   `json:"confidence"`
	RawLabel      string    `json:"raw_label,omitempty"`
	MappingReason string    `json:"mapping_reason,omitempty"`
}
type Question struct {
	ID          string `json:"id"`
	Prompt      string `json:"prompt"`
	Dimension   string `json:"dimension"`
	Position    int    `json:"position"`
	Hint        string `json:"hint,omitempty"`
	Reference   string `json:"reference,omitempty"`
	Explanation string `json:"explanation,omitempty"`
}
type AnswerInput struct {
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
}
type QuestionResult struct {
	QuestionID string `json:"question_id"`
	Passed     bool   `json:"passed"`
	Feedback   string `json:"feedback"`
}
type Evaluation struct {
	Passed          bool             `json:"passed"`
	Score           float64          `json:"score"`
	Verdict         string           `json:"verdict"`
	SuggestedLevel  *int             `json:"suggested_level,omitempty"`
	Summary         string           `json:"summary"`
	QuestionResults []QuestionResult `json:"question_results"`
	Followups       []Question       `json:"followup_questions,omitempty"`
}
type Session struct {
	ID                 uuid.UUID     `json:"id"`
	AbilityID          uuid.UUID     `json:"concept_id"`
	AbilityName        string        `json:"concept_name"`
	Mode               PracticeMode  `json:"mode"`
	BaseLevel          int           `json:"base_level"`
	TargetLevel        int           `json:"target_level"`
	Status             string        `json:"status"`
	Questions          []Question    `json:"questions"`
	Answers            []AnswerInput `json:"answers,omitempty"`
	Evaluation         *Evaluation   `json:"evaluation,omitempty"`
	LevelUpdated       bool          `json:"level_updated"`
	ClarificationCount int           `json:"clarification_count"`
	CreatedAt          time.Time     `json:"created_at"`
	CompletedAt        *time.Time    `json:"completed_at,omitempty"`
}
type StartSessionInput struct {
	AbilityID uuid.UUID    `json:"concept_id"`
	Mode      PracticeMode `json:"mode"`
}

type Repository interface {
	CreateMaterial(context.Context, uuid.UUID, MaterialInput) (Material, error)
	UpdateMaterial(context.Context, uuid.UUID, uuid.UUID, MaterialInput) (Material, error)
	DeleteMaterial(context.Context, uuid.UUID, uuid.UUID) error
	ListMaterials(context.Context, uuid.UUID) ([]Material, error)
	ListCapabilityInputs(context.Context, uuid.UUID) ([]CapabilityInput, error)
	BeginMaterialAnalysis(context.Context, uuid.UUID, uuid.UUID) (Material, error)
	CompleteMaterialAnalysis(context.Context, uuid.UUID, uuid.UUID, []EvidenceDraft) (Material, error)
	FailMaterialAnalysis(context.Context, uuid.UUID, uuid.UUID, string) error
	GetOverview(context.Context, uuid.UUID) (Overview, error)
	GetSettings(context.Context, uuid.UUID) (Settings, error)
	SaveSettings(context.Context, uuid.UUID, Settings) (Settings, error)
	GetCapability(context.Context, uuid.UUID, uuid.UUID) (Capability, error)
	SetCapabilityLevel(context.Context, uuid.UUID, uuid.UUID, int) (Capability, error)
	CreateSession(context.Context, uuid.UUID, Session) (Session, error)
	GetSession(context.Context, uuid.UUID, uuid.UUID) (Session, error)
	ListSessions(context.Context, uuid.UUID) ([]Session, error)
	SaveAnswer(context.Context, uuid.UUID, uuid.UUID, AnswerInput) (Session, error)
	AddClarification(context.Context, uuid.UUID, uuid.UUID, []AnswerInput, []Question) (Session, error)
	CompleteSession(context.Context, uuid.UUID, uuid.UUID, []AnswerInput, Evaluation) (Session, error)
	ConfirmSession(context.Context, uuid.UUID, uuid.UUID) (Session, error)
}
type Assessor interface {
	ExtractEvidence(context.Context, uuid.UUID, Material, []CapabilityInput) ([]EvidenceDraft, error)
	GenerateQuestions(context.Context, uuid.UUID, Capability, PracticeMode, int, int) ([]Question, error)
	EvaluateAnswers(context.Context, uuid.UUID, Session, []AnswerInput, []LevelDefinition) (Evaluation, error)
}
type Service struct {
	repository    Repository
	assessor      Assessor
	questionCount int
}

func NewService(repository Repository, assessor Assessor) *Service {
	return &Service{repository: repository, assessor: assessor, questionCount: 3}
}

func (s *Service) CreateMaterial(ctx context.Context, userID uuid.UUID, input MaterialInput) (Material, error) {
	value, err := normalizeMaterialInput(input)
	if err != nil {
		return Material{}, err
	}
	return s.repository.CreateMaterial(ctx, userID, value)
}
func (s *Service) UpdateMaterial(ctx context.Context, userID, id uuid.UUID, input MaterialInput) (Material, error) {
	value, err := normalizeMaterialInput(input)
	if err != nil {
		return Material{}, err
	}
	return s.repository.UpdateMaterial(ctx, userID, id, value)
}
func (s *Service) DeleteMaterial(ctx context.Context, userID, id uuid.UUID) error {
	return s.repository.DeleteMaterial(ctx, userID, id)
}
func (s *Service) ListMaterials(ctx context.Context, userID uuid.UUID) ([]Material, error) {
	return s.repository.ListMaterials(ctx, userID)
}
func (s *Service) GetOverview(ctx context.Context, userID uuid.UUID) (Overview, error) {
	return s.repository.GetOverview(ctx, userID)
}
func (s *Service) SetCapabilityLevel(ctx context.Context, userID, abilityID uuid.UUID, level int) (Capability, error) {
	if abilityID == uuid.Nil || level < 0 || level > 5 {
		return Capability{}, ErrInvalidInput
	}
	if _, err := s.repository.GetCapability(ctx, userID, abilityID); err != nil {
		return Capability{}, err
	}
	return s.repository.SetCapabilityLevel(ctx, userID, abilityID, level)
}
func (s *Service) GetSettings(ctx context.Context, userID uuid.UUID) (Settings, error) {
	return s.repository.GetSettings(ctx, userID)
}
func (s *Service) SaveSettings(ctx context.Context, userID uuid.UUID, input Settings) (Settings, error) {
	input.ExistingExperience = strings.TrimSpace(input.ExistingExperience)
	if input.WeeklyHours == nil || *input.WeeklyHours < 1 || *input.WeeklyHours > 80 || input.ExpectedWeeks == nil || *input.ExpectedWeeks < 1 || *input.ExpectedWeeks > 52 || len([]rune(input.ExistingExperience)) > 4000 {
		return Settings{}, fmt.Errorf("%w: weekly_hours、expected_weeks 或 existing_experience 不符合要求", ErrInvalidInput)
	}
	return s.repository.SaveSettings(ctx, userID, input)
}

func (s *Service) ConfirmMaterial(ctx context.Context, userID, id uuid.UUID) (material Material, err error) {
	inputs, err := s.repository.ListCapabilityInputs(ctx, userID)
	if err != nil {
		return Material{}, err
	}
	if len(inputs) == 0 {
		return Material{}, fmt.Errorf("%w: 请先导入当前目标岗位的有效 JD，生成市场能力清单", ErrPrecondition)
	}
	material, err = s.repository.BeginMaterialAnalysis(ctx, userID, id)
	if err != nil {
		return Material{}, err
	}
	defer func() {
		if err != nil {
			_ = s.repository.FailMaterialAnalysis(context.WithoutCancel(ctx), userID, material.ID, "材料分析未完成，请检查模型配置或稍后重试")
		}
	}()
	drafts, err := s.assessor.ExtractEvidence(ctx, userID, material, inputs)
	if err != nil {
		return Material{}, err
	}
	if err = validateEvidenceDrafts(material.Text, inputs, drafts); err != nil {
		return Material{}, fmt.Errorf("model_invalid_response: %w", err)
	}
	return s.repository.CompleteMaterialAnalysis(ctx, userID, material.ID, drafts)
}

func (s *Service) StartSession(ctx context.Context, userID uuid.UUID, input StartSessionInput) (Session, error) {
	if input.AbilityID == uuid.Nil || (input.Mode != ModeValidation && input.Mode != ModeReview && input.Mode != ModeInitial) {
		return Session{}, fmt.Errorf("%w: concept_id and valid mode are required", ErrInvalidInput)
	}
	capability, err := s.repository.GetCapability(ctx, userID, input.AbilityID)
	if err != nil {
		return Session{}, err
	}
	if len(capability.Levels) != 6 {
		return Session{}, fmt.Errorf("%w: 能力等级标准尚未准备完成", ErrPrecondition)
	}
	base, target := capability.CurrentLevel, capability.CurrentLevel
	if input.Mode == ModeValidation {
		if !capability.Assessed {
			return Session{}, fmt.Errorf("%w: 未评估能力请先完成初始评估或自行设置等级", ErrPrecondition)
		}
		target = base + 1
		if target > 5 {
			return Session{}, fmt.Errorf("%w: capability already reached L5", ErrPrecondition)
		}
	} else if input.Mode == ModeInitial {
		if capability.Assessed {
			return Session{}, fmt.Errorf("%w: 已有等级，无需初始评估", ErrPrecondition)
		}
		target = capability.MarketLevel
		if target == 0 {
			target = 1
		}
	} else if target == 0 {
		target = 1
	}
	count := 6
	if input.Mode == ModeInitial {
		count = s.questionCount
	}
	questions, err := s.assessor.GenerateQuestions(ctx, userID, capability, input.Mode, target, count)
	if err != nil {
		return Session{}, err
	}
	if err := validateQuestions(questions, count); err != nil {
		return Session{}, fmt.Errorf("model_invalid_response: %w", err)
	}
	for i := range questions {
		if input.Mode == ModeReview {
			if strings.TrimSpace(questions[i].Hint) == "" || strings.TrimSpace(questions[i].Reference) == "" || strings.TrimSpace(questions[i].Explanation) == "" {
				return Session{}, fmt.Errorf("model_invalid_response: review question has no study aids")
			}
		} else {
			questions[i].Hint = ""
			questions[i].Reference = ""
			questions[i].Explanation = ""
		}
	}
	return s.repository.CreateSession(ctx, userID, Session{AbilityID: input.AbilityID, AbilityName: capability.Name, Mode: input.Mode, BaseLevel: base, TargetLevel: target, Status: "ready", Questions: questions})
}
func (s *Service) GetSession(ctx context.Context, userID, id uuid.UUID) (Session, error) {
	return s.repository.GetSession(ctx, userID, id)
}
func (s *Service) ListSessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	return s.repository.ListSessions(ctx, userID)
}
func (s *Service) SaveAnswer(ctx context.Context, userID, id uuid.UUID, answer AnswerInput) (Session, error) {
	session, err := s.repository.GetSession(ctx, userID, id)
	if err != nil {
		return Session{}, err
	}
	if session.Status != "ready" && session.Status != "clarifying" {
		return Session{}, ErrConflict
	}
	answer.QuestionID = strings.TrimSpace(answer.QuestionID)
	answer.Answer = strings.TrimSpace(answer.Answer)
	if answer.Answer == "" || len([]rune(answer.Answer)) > 8000 {
		return Session{}, ErrInvalidInput
	}
	valid := false
	for _, q := range session.Questions {
		if q.ID == answer.QuestionID {
			valid = true
			break
		}
	}
	if !valid {
		return Session{}, ErrInvalidInput
	}
	return s.repository.SaveAnswer(ctx, userID, id, answer)
}
func (s *Service) ConfirmSession(ctx context.Context, userID, id uuid.UUID) (Session, error) {
	return s.repository.ConfirmSession(ctx, userID, id)
}
func (s *Service) SubmitSession(ctx context.Context, userID, id uuid.UUID, answers []AnswerInput) (Session, error) {
	session, err := s.repository.GetSession(ctx, userID, id)
	if err != nil {
		return Session{}, err
	}
	if session.Status != "ready" && session.Status != "clarifying" {
		return Session{}, ErrConflict
	}
	normalized, err := normalizeAnswers(session.Questions, answers)
	if err != nil {
		return Session{}, err
	}
	capability, err := s.repository.GetCapability(ctx, userID, session.AbilityID)
	if err != nil {
		return Session{}, err
	}
	evaluation, err := s.assessor.EvaluateAnswers(ctx, userID, session, normalized, capability.Levels)
	if err != nil {
		return Session{}, err
	}
	if err := validateEvaluation(session, evaluation); err != nil {
		return Session{}, fmt.Errorf("model_invalid_response: %w", err)
	}
	if session.Mode == ModeValidation && session.Status == "ready" && evaluation.Verdict == "uncertain" && len(evaluation.Followups) > 0 {
		if len(evaluation.Followups) > 3 {
			return Session{}, fmt.Errorf("model_invalid_response: too many followups")
		}
		seen := map[string]bool{}
		for _, q := range session.Questions {
			seen[q.ID] = true
		}
		for _, q := range evaluation.Followups {
			if seen[q.ID] || strings.TrimSpace(q.Prompt) == "" {
				return Session{}, fmt.Errorf("model_invalid_response: invalid followup")
			}
			seen[q.ID] = true
		}
		for i := range evaluation.Followups {
			evaluation.Followups[i].Hint = ""
			evaluation.Followups[i].Reference = ""
			evaluation.Followups[i].Explanation = ""
		}
		return s.repository.AddClarification(ctx, userID, id, normalized, evaluation.Followups)
	}
	if session.Mode == ModeReview {
		evaluation.Passed = false
		evaluation.Verdict = "review"
	}
	if session.Mode == ModeValidation {
		evaluation.Passed = evaluation.Verdict == "pass"
	}
	if session.Mode == ModeInitial {
		evaluation.Passed = false
	}
	evaluation.Followups = nil
	return s.repository.CompleteSession(ctx, userID, session.ID, normalized, evaluation)
}

func normalizeMaterialInput(input MaterialInput) (MaterialInput, error) {
	input.Type = MaterialType(strings.ToLower(strings.TrimSpace(string(input.Type))))
	input.Title = strings.TrimSpace(input.Title)
	input.Text = strings.TrimSpace(input.Text)
	if input.Type != MaterialResume && input.Type != MaterialExperience {
		return MaterialInput{}, fmt.Errorf("%w: type must be resume or experience", ErrInvalidInput)
	}
	if input.Title == "" || len([]rune(input.Title)) > 120 {
		return MaterialInput{}, fmt.Errorf("%w: title is required and must not exceed 120 characters", ErrInvalidInput)
	}
	if len([]rune(input.Text)) < 20 || len([]rune(input.Text)) > 100000 {
		return MaterialInput{}, fmt.Errorf("%w: text must contain 20 to 100000 characters", ErrInvalidInput)
	}
	return input, nil
}
func validateEvidenceDrafts(source string, inputs []CapabilityInput, drafts []EvidenceDraft) error {
	known := map[uuid.UUID]bool{}
	for _, input := range inputs {
		known[input.AbilityID] = true
	}
	seen := map[uuid.UUID]bool{}
	for index, draft := range drafts {
		draft.Quote, draft.Reason = strings.TrimSpace(draft.Quote), strings.TrimSpace(draft.Reason)
		if !known[draft.AbilityID] || seen[draft.AbilityID] {
			return fmt.Errorf("evidence %d has unknown or duplicate ability", index+1)
		}
		if draft.Level < 1 || draft.Level > 5 {
			return fmt.Errorf("evidence %d level must be L1-L5", index+1)
		}
		if draft.Quote == "" || !strings.Contains(source, draft.Quote) {
			return fmt.Errorf("evidence %d quote is not an exact source substring", index+1)
		}
		if draft.Reason == "" || draft.Confidence < 0 || draft.Confidence > 1 {
			return fmt.Errorf("evidence %d reason or confidence is invalid", index+1)
		}
		if draft.RawLabel != "" && (len([]rune(draft.RawLabel)) > 150 || !strings.Contains(draft.Quote, draft.RawLabel) || strings.TrimSpace(draft.MappingReason) == "") {
			return fmt.Errorf("evidence %d has an invalid original ability label or mapping reason", index+1)
		}
		seen[draft.AbilityID] = true
	}
	return nil
}
func validateQuestions(questions []Question, count int) error {
	if len(questions) != count {
		return fmt.Errorf("expected %d questions, got %d", count, len(questions))
	}
	seen := map[string]bool{}
	for index := range questions {
		questions[index].ID = strings.TrimSpace(questions[index].ID)
		questions[index].Prompt = strings.TrimSpace(questions[index].Prompt)
		questions[index].Dimension = strings.TrimSpace(questions[index].Dimension)
		if questions[index].ID == "" || questions[index].Prompt == "" || questions[index].Dimension == "" || questions[index].Position != index+1 || seen[questions[index].ID] {
			return fmt.Errorf("question %d is invalid", index+1)
		}
		seen[questions[index].ID] = true
	}
	return nil
}
func normalizeAnswers(questions []Question, answers []AnswerInput) ([]AnswerInput, error) {
	if len(answers) != len(questions) {
		return nil, fmt.Errorf("%w: every question requires one answer", ErrInvalidInput)
	}
	known, seen := map[string]bool{}, map[string]bool{}
	for _, question := range questions {
		known[question.ID] = true
	}
	for index := range answers {
		answers[index].QuestionID = strings.TrimSpace(answers[index].QuestionID)
		answers[index].Answer = strings.TrimSpace(answers[index].Answer)
		if !known[answers[index].QuestionID] || seen[answers[index].QuestionID] || answers[index].Answer == "" || len([]rune(answers[index].Answer)) > 8000 {
			return nil, fmt.Errorf("%w: answer %d is invalid", ErrInvalidInput, index+1)
		}
		seen[answers[index].QuestionID] = true
	}
	return answers, nil
}
func validateEvaluation(session Session, value Evaluation) error {
	if value.Score < 0 || value.Score > 1 || strings.TrimSpace(value.Summary) == "" || len(value.QuestionResults) != len(session.Questions) {
		return errors.New("evaluation summary, score, or result count is invalid")
	}
	if value.Verdict != "pass" && value.Verdict != "not_yet" && value.Verdict != "uncertain" && value.Verdict != "review" {
		return errors.New("invalid verdict")
	}
	if session.Mode == ModeInitial && value.SuggestedLevel != nil && (*value.SuggestedLevel < 0 || *value.SuggestedLevel > 5) {
		return errors.New("invalid initial level")
	}
	known, seen := map[string]bool{}, map[string]bool{}
	for _, question := range session.Questions {
		known[question.ID] = true
	}
	for _, result := range value.QuestionResults {
		if !known[result.QuestionID] || seen[result.QuestionID] || strings.TrimSpace(result.Feedback) == "" {
			return errors.New("evaluation contains invalid question result")
		}
		seen[result.QuestionID] = true
	}
	return nil
}
