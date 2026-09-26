package projectrecs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

var (
	ErrNotReady  = errors.New("recommendation profiles are not ready")
	ErrConflict  = errors.New("recommendation job already active or report changed")
	ErrNotFound  = errors.New("recommendation project not found")
	ErrInvalid   = errors.New("invalid recommendation request")
	ErrNoJob     = errors.New("no recommendation job")
	ErrLeaseLost = errors.New("recommendation lease lost")
)

const PromptVersion = "project-recommendation-v1"

type Fact struct {
	Text      string `json:"text"`
	Quote     string `json:"quote"`
	SourceURL string `json:"source_url"`
}
type Comparison struct {
	Similarities         []string `json:"similarities"`
	Differences          []string `json:"differences"`
	ProjectAdvantages    []string `json:"project_advantages"`
	ProjectLimits        []string `json:"project_limits"`
	RepositoryAdvantages []string `json:"repository_advantages"`
	RepositoryLimits     []string `json:"repository_limits"`
}
type Reference struct {
	FullName    string     `json:"full_name"`
	URL         string     `json:"url"`
	Description string     `json:"description"`
	Language    string     `json:"language"`
	Archived    bool       `json:"archived"`
	CheckedAt   time.Time  `json:"checked_at"`
	Facts       []Fact     `json:"facts"`
	Comparison  Comparison `json:"comparison"`
}
type Draft struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	Audience      string   `json:"audience"`
	Problem       string   `json:"problem"`
	Shape         string   `json:"shape"`
	Scope         []string `json:"scope"`
	FitReasons    []string `json:"fit_reasons"`
	Abilities     []string `json:"abilities"`
	Duration      string   `json:"duration"`
	KnownFacts    []string `json:"known_facts"`
	Assumptions   []string `json:"assumptions"`
	SearchQueries []string `json:"search_queries"`
}
type Project struct {
	Draft
	Rank               int         `json:"rank"`
	References         []Reference `json:"references"`
	NoReferenceReason  string      `json:"no_reference_reason,omitempty"`
	SearchedDirections []string    `json:"searched_directions"`
}
type Report struct {
	ID            uuid.UUID `json:"id"`
	TargetTitle   string    `json:"target_title"`
	Projects      []Project `json:"projects"`
	EmptyReason   string    `json:"empty_reason,omitempty"`
	GeneratedAt   time.Time `json:"generated_at"`
	PromptVersion string    `json:"prompt_version"`
}
type JobView struct {
	ID              uuid.UUID  `json:"id"`
	Status          string     `json:"status"`
	Phase           string     `json:"phase"`
	DraftCount      int        `json:"draft_count"`
	ResearchedCount int        `json:"researched_count"`
	Attempts        int        `json:"attempts"`
	MaxAttempts     int        `json:"max_attempts"`
	NextAttemptAt   *time.Time `json:"next_attempt_at,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
type View struct {
	Readiness         knowledgegaps.Readiness `json:"readiness"`
	Report            *Report                 `json:"report,omitempty"`
	SelectedProjectID string                  `json:"selected_project_id,omitempty"`
	Stale             bool                    `json:"stale"`
	Job               *JobView                `json:"job,omitempty"`
}
type Stored struct {
	Report            Report
	SourceHash        string
	SelectedProjectID string
}
type MarketAbility struct {
	Name           string   `json:"name"`
	CoveredJDCount int      `json:"covered_jd_count"`
	Level          int      `json:"common_level"`
	Evidence       []string `json:"evidence"`
}
type UserAbility struct {
	Name   string `json:"name"`
	Level  int    `json:"level"`
	Source string `json:"source"`
}
type Input struct {
	Goal               string          `json:"goal"`
	EmploymentType     string          `json:"employment_type"`
	GraduationYear     *int            `json:"graduation_year,omitempty"`
	Directions         []string        `json:"directions"`
	IncludedJDCount    int             `json:"included_jd_count"`
	MarketAbilities    []MarketAbility `json:"market_abilities"`
	UserAbilities      []UserAbility   `json:"user_abilities"`
	WeeklyHours        int             `json:"weekly_hours"`
	ExpectedWeeks      int             `json:"expected_weeks"`
	ExistingExperience string          `json:"existing_experience"`
}
type Snapshot struct {
	TargetID      uuid.UUID
	GoalSignature string
	SourceHash    string
	Input         Input
	Readiness     knowledgegaps.Readiness
}
type Job struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	TargetID      uuid.UUID
	GoalSignature string
	SourceHash    string
	Input         Input
	Adjustment    string
	Phase         string
	Drafts        []Draft
	Research      []Research
	Attempts      int
	MaxAttempts   int
	LeaseToken    uuid.UUID
}
type Repository interface {
	Get(context.Context, uuid.UUID, string) (*Stored, *JobView, error)
	Enqueue(context.Context, uuid.UUID, Snapshot, string) (JobView, error)
	Select(context.Context, uuid.UUID, string, uuid.UUID, string) error
	RecoverExpired(context.Context) error
	Claim(context.Context, time.Duration) (Job, error)
	Heartbeat(context.Context, Job, time.Duration) error
	SaveDrafts(context.Context, Job, []Draft) error
	SaveResearchProgress(context.Context, Job, []Research) error
	SaveResearch(context.Context, Job, []Research) error
	Complete(context.Context, Job, Report) error
	Fail(context.Context, Job, string, bool) error
}
type TargetReader interface {
	Current(context.Context, uuid.UUID) (target.Target, error)
}
type MarketReader interface {
	ProfileCurrent(context.Context, uuid.UUID) (market.Profile, error)
}
type ProfileReader interface {
	GetOverview(context.Context, uuid.UUID) (profile.Overview, error)
	GetSettings(context.Context, uuid.UUID) (profile.Settings, error)
}
type GapReader interface {
	Get(context.Context, uuid.UUID) (knowledgegaps.View, error)
}
type ModelConfigReader interface {
	Get(context.Context, uuid.UUID) (modelconfig.Metadata, error)
}

type Service struct {
	repo     Repository
	targets  TargetReader
	market   MarketReader
	profiles ProfileReader
	gaps     GapReader
	models   ModelConfigReader
}

func NewService(repo Repository, targets TargetReader, market MarketReader, profiles ProfileReader, gaps GapReader, models ModelConfigReader) *Service {
	return &Service{repo, targets, market, profiles, gaps, models}
}
func (s *Service) Snapshot(ctx context.Context, userID uuid.UUID) (Snapshot, error) {
	t, err := s.targets.Current(ctx, userID)
	if errors.Is(err, target.ErrNotFound) {
		return Snapshot{Readiness: knowledgegaps.Readiness{Code: "target_missing", Message: "请先设置求职目标"}}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	g, err := s.gaps.Get(ctx, userID)
	if err != nil {
		return Snapshot{}, err
	}
	m, err := s.market.ProfileCurrent(ctx, userID)
	if err != nil {
		return Snapshot{}, err
	}
	p, err := s.profiles.GetOverview(ctx, userID)
	if err != nil {
		return Snapshot{}, err
	}
	settings, err := s.profiles.GetSettings(ctx, userID)
	if err != nil {
		return Snapshot{}, err
	}
	r := g.Readiness
	if r.Code == "ready" && !p.Complete {
		r.Code = "profile_incomplete"
		r.Message = "请先完成用户画像"
	}
	if r.Code == "ready" && !m.Complete {
		r.Code = "market_incomplete"
		r.Message = "请先完成市场画像"
	}
	input := Input{Goal: t.Title, EmploymentType: t.EmploymentType, GraduationYear: t.GraduationYear,
		IncludedJDCount: m.IncludedJDCount, ExistingExperience: strings.TrimSpace(settings.ExistingExperience)}
	if runes := []rune(input.ExistingExperience); len(runes) > 2500 {
		input.ExistingExperience = string(runes[:2500])
	}
	if settings.WeeklyHours != nil {
		input.WeeklyHours = *settings.WeeklyHours
	}
	if settings.ExpectedWeeks != nil {
		input.ExpectedWeeks = *settings.ExpectedWeeks
	}
	for _, d := range t.Directions {
		name := d.Category
		if d.Specialty != "" {
			name += " / " + d.Specialty
		}
		input.Directions = append(input.Directions, name)
	}
	sort.Slice(m.Abilities, func(i, j int) bool { return m.Abilities[i].CoveredJDCount > m.Abilities[j].CoveredJDCount })
	for index, a := range m.Abilities {
		if index >= 40 {
			break
		}
		item := MarketAbility{Name: a.Name, CoveredJDCount: a.CoveredJDCount, Level: a.LevelSummary.RecommendedLevel}
		for _, e := range a.Evidences {
			if len(item.Evidence) == 1 {
				break
			}
			if strings.TrimSpace(e.Evidence) != "" {
				quote := []rune(e.Evidence)
				if len(quote) > 180 {
					quote = quote[:180]
				}
				item.Evidence = append(item.Evidence, string(quote))
			}
		}
		input.MarketAbilities = append(input.MarketAbilities, item)
	}
	for _, a := range p.Capabilities {
		if a.Assessed {
			input.UserAbilities = append(input.UserAbilities, UserAbility{Name: a.Name, Level: a.CurrentLevel, Source: a.LevelSource})
		}
	}
	sort.Slice(input.MarketAbilities, func(i, j int) bool { return input.MarketAbilities[i].Name < input.MarketAbilities[j].Name })
	sort.Slice(input.UserAbilities, func(i, j int) bool { return input.UserAbilities[i].Name < input.UserAbilities[j].Name })
	raw, _ := json.Marshal(struct {
		ProfileInput       Input
		MarketRequirements string
	}{input, g.SourceFingerprint})
	hash := sha256.Sum256(raw)
	return Snapshot{TargetID: t.ID, GoalSignature: knowledgegaps.GoalSignature(t), SourceHash: hex.EncodeToString(hash[:]), Input: input, Readiness: r}, nil
}
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (View, error) {
	ss, err := s.Snapshot(ctx, userID)
	if err != nil {
		return View{}, err
	}
	v := View{Readiness: ss.Readiness}
	if ss.GoalSignature == "" {
		return v, nil
	}
	stored, job, err := s.repo.Get(ctx, userID, ss.GoalSignature)
	if err != nil {
		return View{}, err
	}
	v.Job = job
	if stored != nil {
		v.Report = &stored.Report
		v.SelectedProjectID = stored.SelectedProjectID
		v.Stale = stored.SourceHash != ss.SourceHash
	}
	return v, nil
}
func (s *Service) Generate(ctx context.Context, userID uuid.UUID, adjustment string) (View, error) {
	adjustment = strings.TrimSpace(adjustment)
	if len([]rune(adjustment)) > 500 {
		return View{}, ErrInvalid
	}
	ss, err := s.Snapshot(ctx, userID)
	if err != nil {
		return View{}, err
	}
	if ss.Readiness.Code != "ready" {
		return View{Readiness: ss.Readiness}, ErrNotReady
	}
	config, err := s.models.Get(ctx, userID)
	if err != nil {
		return View{}, err
	}
	if !config.Configured {
		return View{}, modelconfig.ErrNotConfigured
	}
	job, err := s.repo.Enqueue(ctx, userID, ss, adjustment)
	if err != nil {
		return View{}, err
	}
	v, err := s.Get(ctx, userID)
	if err != nil {
		return View{}, err
	}
	v.Job = &job
	return v, nil
}
func (s *Service) Select(ctx context.Context, userID, reportID uuid.UUID, projectID string) (View, error) {
	ss, err := s.Snapshot(ctx, userID)
	if err != nil {
		return View{}, err
	}
	if ss.GoalSignature == "" {
		return View{}, ErrNotFound
	}
	if err = s.repo.Select(ctx, userID, ss.GoalSignature, reportID, projectID); err != nil {
		return View{}, err
	}
	return s.Get(ctx, userID)
}
func ValidateDraft(d Draft) error {
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Title) == "" || strings.TrimSpace(d.Summary) == "" || strings.TrimSpace(d.Audience) == "" || strings.TrimSpace(d.Problem) == "" || strings.TrimSpace(d.Shape) == "" || strings.TrimSpace(d.Duration) == "" || len(d.Scope) == 0 || len(d.FitReasons) == 0 || len(d.Abilities) == 0 || len(d.SearchQueries) == 0 || len(d.SearchQueries) > 2 {
		return fmt.Errorf("incomplete project draft")
	}
	if len([]rune(d.ID)) > 80 || len([]rune(d.Title)) > 120 {
		return fmt.Errorf("project draft too long")
	}
	return nil
}
