package learningroutes

import (
	"context"
	"errors"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/google/uuid"
	"time"
)

var (
	ErrInvalid       = errors.New("invalid learning route")
	ErrConflict      = errors.New("learning route version conflict")
	ErrNotReady      = errors.New("learning route inputs not ready")
	ErrNotFound      = errors.New("learning route not found")
	ErrNoJob         = errors.New("no learning route job")
	ErrLeaseLost     = errors.New("learning route lease lost")
	ErrInputsChanged = errors.New("learning route inputs changed")
)

const PromptVersion = "learning-route-v2"

type PlanConfig struct {
	Mode          string `json:"mode"`
	WeeklyMinutes int    `json:"weekly_minutes"`
	StartDate     string `json:"start_date,omitempty"`
	LearningDays  []int  `json:"learning_days,omitempty"`
	DailyMinutes  int    `json:"daily_minutes,omitempty"`
}
type PathChoice struct {
	GapID      string      `json:"gap_id"`
	AbilityIDs []uuid.UUID `json:"ability_ids"`
}
type Config struct {
	Project   PlanConfig   `json:"project"`
	Knowledge PlanConfig   `json:"knowledge"`
	Paths     []PathChoice `json:"paths,omitempty"`
}
type Task struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	CompletionCriteria string   `json:"completion_criteria"`
	EstimateMinutes    int      `json:"estimate_minutes"`
	ScheduledDate      string   `json:"scheduled_date,omitempty"`
	DependsOn          []string `json:"depends_on,omitempty"`
}
type Stage struct {
	Title   string `json:"title"`
	Outcome string `json:"outcome"`
	Tasks   []Task `json:"tasks"`
}
type Plan struct {
	SelectedPaths []PathChoice `json:"selected_paths,omitempty"`
	Role          string       `json:"role"`
	Title         string       `json:"title"`
	Description   string       `json:"description"`
	Objective     string       `json:"objective"`
	Stages        []Stage      `json:"stages"`
	PathReasons   []string     `json:"path_reasons,omitempty"`
}
type Version struct {
	ContentHash   string    `json:"content_hash"`
	ID            uuid.UUID `json:"id"`
	RouteID       uuid.UUID `json:"route_id"`
	Number        int       `json:"version_no"`
	Revision      int64     `json:"revision"`
	SourceHash    string    `json:"-"`
	KnowledgeHash string    `json:"-"`
	Config        Config    `json:"config"`
	Plans         []Plan    `json:"plans"`
	GeneratedAt   time.Time `json:"generated_at"`
}
type JobView struct {
	ErrorMessage string    `json:"error_message,omitempty"`
	ID           uuid.UUID `json:"id"`
	Status       string    `json:"status"`
	Attempts     int       `json:"attempts"`
	ErrorCode    string    `json:"error_code,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type ImportRequest struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"-"`
	RouteID       uuid.UUID `json:"route_id"`
	VersionID     uuid.UUID `json:"version_id"`
	Revision      int64     `json:"revision"`
	Key           string    `json:"idempotency_key"`
	Digest        string    `json:"content_digest"`
	Payload       Payload   `json:"-"`
	Status        string    `json:"status"`
	Receipt       *Receipt  `json:"receipt,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
	DispatchToken uuid.UUID `json:"-"`
}
type View struct {
	ProjectTitle         string                  `json:"project_title,omitempty"`
	GoalTitle            string                  `json:"goal_title"`
	Readiness            knowledgegaps.Readiness `json:"readiness"`
	Knowledge            *knowledgegaps.Report   `json:"knowledge_summary,omitempty"`
	Version              *Version                `json:"version,omitempty"`
	Job                  *JobView                `json:"job,omitempty"`
	Import               *ImportRequest          `json:"import,omitempty"`
	KnowledgeNeedsUpdate bool                    `json:"knowledge_needs_update"`
	ProjectNeedsReview   bool                    `json:"project_needs_review"`
	Warnings             []string                `json:"warnings"`
	ImportEnabled        bool                    `json:"import_enabled"`
	StudyFlowURL         string                  `json:"studyflow_url,omitempty"`
}
type GenerateInput struct {
	Config        Config `json:"config"`
	KnowledgeOnly bool   `json:"knowledge_only"`
	Adjustment    string `json:"adjustment,omitempty"`
}
type EditInput struct {
	VersionID uuid.UUID `json:"version_id"`
	Revision  int64     `json:"revision"`
	Config    Config    `json:"config"`
	Plans     []Plan    `json:"plans"`
}
type ConfirmInput struct {
	VersionID   uuid.UUID `json:"version_id"`
	Revision    int64     `json:"revision"`
	ContentHash string    `json:"content_hash"`
}
type AbilityStandard struct {
	Name   string `json:"name"`
	Levels any    `json:"levels"`
}
type Input struct {
	Profile       projectrecs.Input    `json:"profile"`
	Project       projectrecs.Project  `json:"project"`
	Standards     []AbilityStandard    `json:"standards"`
	Knowledge     knowledgegaps.Report `json:"knowledge"`
	Config        Config               `json:"config"`
	Previous      []Plan               `json:"previous,omitempty"`
	KnowledgeOnly bool                 `json:"knowledge_only"`
	Adjustment    string               `json:"adjustment,omitempty"`
	KnowledgeHash string               `json:"knowledge_hash"`
	ProjectHash   string               `json:"project_hash"`
}
type Snapshot struct {
	TargetID      uuid.UUID
	GoalSignature string
	ReportID      uuid.UUID
	ProjectID     string
	SourceHash    string
	Input         Input
	Readiness     knowledgegaps.Readiness
}
type Job struct {
	ModelPlans                    []Plan
	ErrorDetail                   string
	ID, RouteID, UserID, TargetID uuid.UUID
	GoalSignature, SourceHash     string
	BaseVersion                   int
	Input                         Input
	Attempts                      int
	LeaseToken                    uuid.UUID
}
type Repository interface {
	WithinUserTransaction(context.Context, uuid.UUID, func(context.Context) error) error
	Get(context.Context, uuid.UUID, string, uuid.UUID, string) (*Version, *JobView, *ImportRequest, error)
	Enqueue(context.Context, uuid.UUID, Snapshot) (JobView, error)
	Edit(context.Context, uuid.UUID, EditInput) error
	Claim(context.Context, time.Duration) (Job, error)
	RecoverExpired(context.Context) error
	Heartbeat(context.Context, Job, time.Duration) error
	SaveModelPlans(context.Context, Job, []Plan) error
	Complete(context.Context, Job, []Plan, func(context.Context) error) error
	Fail(context.Context, Job, string, string, bool) error
	Freeze(context.Context, uuid.UUID, Version, Payload, string) (ImportRequest, error)
	FindImport(context.Context, uuid.UUID, uuid.UUID) (ImportRequest, error)
	ClaimImport(context.Context, uuid.UUID, uuid.UUID) (ImportRequest, error)
	FinishImport(context.Context, ImportRequest, *Receipt, string, string) error
}
type Remote interface {
	Send(context.Context, string, ImportRequest) (*Receipt, error)
	Enabled() bool
}
