package knowledgegaps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

var ErrNotReady = errors.New("knowledge gap sources are not ready")

type Option struct {
	ID         uuid.UUID `json:"id"`
	AbilityID  uuid.UUID `json:"ability_id"`
	Name       string    `json:"name"`
	Level      int       `json:"level"`
	Source     string    `json:"source"`
	Evidence   string    `json:"evidence"`
	Reason     string    `json:"reason"`
	Graded     bool      `json:"graded"`
	Resolution string    `json:"resolution"`
}
type Requirement struct {
	ID            uuid.UUID `json:"id"`
	JDID          uuid.UUID `json:"jd_id"`
	JDTitle       string    `json:"jd_title"`
	Operator      string    `json:"operator"`
	RequiredCount int       `json:"required_count"`
	Kind          string    `json:"kind"`
	Evidence      string    `json:"evidence"`
	Options       []Option  `json:"options"`
}
type Item struct {
	ID             string             `json:"id"`
	Kind           string             `json:"kind"`
	Name           string             `json:"name"`
	AbilityID      *uuid.UUID         `json:"ability_id,omitempty"`
	CurrentLevel   int                `json:"current_level,omitempty"`
	TargetLevel    int                `json:"target_level,omitempty"`
	UserSource     string             `json:"user_source,omitempty"`
	UserEvidence   []profile.Evidence `json:"user_evidence,omitempty"`
	SampleCount    int                `json:"sample_count"`
	RequiredCount  int                `json:"required_count,omitempty"`
	SatisfiedCount int                `json:"satisfied_count,omitempty"`
	Options        []OptionState      `json:"options,omitempty"`
	Evidences      []Evidence         `json:"evidences"`
}
type OptionState struct {
	Name          string    `json:"name"`
	AbilityID     uuid.UUID `json:"ability_id"`
	CurrentLevel  int       `json:"current_level"`
	RequiredLevel int       `json:"required_level"`
	Assessed      bool      `json:"assessed"`
	Satisfied     bool      `json:"satisfied"`
}
type Evidence struct {
	JDID    uuid.UUID `json:"jd_id"`
	JDTitle string    `json:"jd_title"`
	Quote   string    `json:"quote"`
	Level   int       `json:"level"`
	Source  string    `json:"source"`
	Reason  string    `json:"reason"`
}
type Report struct {
	TargetID    uuid.UUID `json:"target_id"`
	Gaps        []Item    `json:"gaps"`
	Met         []Item    `json:"met"`
	Preferred   []Item    `json:"preferred"`
	GeneratedAt time.Time `json:"generated_at"`
}
type Readiness struct {
	Code                string `json:"code"`
	Message             string `json:"message"`
	IncludedJDCount     int    `json:"included_jd_count"`
	PendingReviewCount  int    `json:"pending_review_count"`
	FailedReviewCount   int    `json:"failed_review_count"`
	PendingGradingCount int    `json:"pending_grading_count"`
	FailedGradingCount  int    `json:"failed_grading_count"`
	MissingAbilityCount int    `json:"missing_ability_count"`
}
type View struct {
	Report            *Report   `json:"report,omitempty"`
	Stale             bool      `json:"stale"`
	Readiness         Readiness `json:"readiness"`
	SourceFingerprint string    `json:"-"`
	RefreshError      string    `json:"refresh_error,omitempty"`
}
type Stored struct {
	Report     Report
	SourceHash string
}
type Repository interface {
	// Snapshot serializes report publication and reads the contributing sources
	// consistently. It also joins an existing approved business transaction.
	WithinUserSnapshot(context.Context, uuid.UUID, func(context.Context) error) error
	LoadRequirements(context.Context, uuid.UUID, uuid.UUID) ([]Requirement, error)
	Get(context.Context, uuid.UUID, string) (*Stored, error)
	Save(context.Context, uuid.UUID, uuid.UUID, string, string, Report) error
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
type Service struct {
	repo     Repository
	targets  TargetReader
	market   MarketReader
	profiles ProfileReader
}

func NewService(repo Repository, targets TargetReader, market MarketReader, profiles ProfileReader) *Service {
	return &Service{repo, targets, market, profiles}
}

type snapshot struct {
	target        target.Target
	market        market.Profile
	profile       profile.Overview
	settings      profile.Settings
	requirements  []Requirement
	readiness     Readiness
	hash          string
	goalSignature string
}

func (s *Service) load(ctx context.Context, userID uuid.UUID) (snapshot, error) {
	t, err := s.targets.Current(ctx, userID)
	if err != nil {
		return snapshot{}, err
	}
	m, err := s.market.ProfileCurrent(ctx, userID)
	if err != nil {
		return snapshot{}, err
	}
	p, err := s.profiles.GetOverview(ctx, userID)
	if err != nil {
		return snapshot{}, err
	}
	settings, err := s.profiles.GetSettings(ctx, userID)
	if err != nil {
		return snapshot{}, err
	}
	reqs, err := s.repo.LoadRequirements(ctx, userID, t.ID)
	if err != nil {
		return snapshot{}, err
	}
	r := Readiness{Code: "ready", IncludedJDCount: m.IncludedJDCount, PendingGradingCount: m.AbilityGradingPendingCount, FailedGradingCount: m.AbilityGradingFailedCount}
	if m.IncludedJDCount < 10 {
		r.Code = "market_incomplete"
		r.Message = "请先收集至少 10 份计入市场画像的 JD"
	}
	missingGradeJDs := map[uuid.UUID]bool{}
	for _, q := range reqs {
		for _, o := range q.Options {
			switch o.Resolution {
			case "pending_review":
				r.PendingReviewCount++
			case "review_failed":
				r.FailedReviewCount++
			}
			if o.Resolution == "resolved" && !o.Graded {
				missingGradeJDs[q.JDID] = true
			}
		}
	}
	if len(missingGradeJDs) > r.PendingGradingCount {
		r.PendingGradingCount = len(missingGradeJDs)
	}
	if r.Code == "ready" && (r.PendingReviewCount > 0 || r.FailedReviewCount > 0) {
		r.Code = "ability_review_pending"
		r.Message = "相关能力目录审核尚未完成，请在 JD 详情中重试失败项"
	}
	if r.Code == "ready" && (r.PendingGradingCount > 0 || r.FailedGradingCount > 0) {
		r.Code = "grading_pending"
		r.Message = "JD 能力判级尚未完成，请在市场画像中查看或重试"
	}
	if r.Code == "ready" && (settings.WeeklyHours == nil || settings.ExpectedWeeks == nil || settings.ExistingExperience == "") {
		r.Code = "profile_incomplete"
		r.Message = "请先补齐用户画像的必要信息"
	}
	ability := map[uuid.UUID]profile.Capability{}
	for _, a := range p.Capabilities {
		ability[a.AbilityID] = a
	}
	for _, q := range reqs {
		if q.Kind == "preferred" {
			continue
		}
		if !groupDecidable(q, ability) {
			r.MissingAbilityCount++
		}
	}
	if r.Code == "ready" && r.MissingAbilityCount > 0 {
		r.Code = "profile_incomplete"
		r.Message = "请先评估当前岗位要求所需的能力"
	}
	// Only sources that can change a gap conclusion enter this fingerprint.
	type level struct {
		ID       uuid.UUID
		Value    int
		Assessed bool
		Source   string
		Evidence []profile.Evidence
	}
	levels := make([]level, 0, len(p.Capabilities))
	for _, a := range p.Capabilities {
		evidence := sortedSourceValues(a.Evidence, func(e profile.Evidence) string { return e.ID.String() })
		levels = append(levels, level{a.AbilityID, a.CurrentLevel, a.Assessed, a.LevelSource, evidence})
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].ID.String() < levels[j].ID.String() })
	// Database/map iteration order is not a source change. Canonicalize copies
	// so repeated reads do not keep replacing an otherwise identical report.
	marketAbilities := sortedSourceValues(m.Abilities, func(a market.AbilitySummary) string { return a.AbilityID.String() })
	for i := range marketAbilities {
		a := &marketAbilities[i]
		a.Evidences = sortedSourceValues(a.Evidences, func(e market.AbilityEvidence) string {
			return e.JobDescriptionID.String() + "\x00" + e.Evidence + "\x00" + e.Qualifier
		})
		for _, summary := range []*market.LevelSummary{&a.LevelSummary, &a.PreferredLevelSummary} {
			summary.Evidences = sortedSourceValues(summary.Evidences, func(e market.LevelEvidence) string { return e.JobDescriptionID.String() })
		}
	}
	requirements := sortedSourceValues(reqs, func(q Requirement) string { return q.ID.String() })
	for i := range requirements {
		requirements[i].Options = sortedSourceValues(requirements[i].Options, func(o Option) string { return o.ID.String() })
	}
	goalSignature := signature(t)
	value, _ := json.Marshal(struct {
		Target       uuid.UUID
		Market       []market.AbilitySummary
		Requirements []Requirement
		Levels       []level
	}{t.ID, marketAbilities, requirements, levels})
	h := sha256.Sum256(value)
	return snapshot{t, m, p, settings, reqs, r, hex.EncodeToString(h[:]), goalSignature}, nil
}

func sortedSourceValues[T any](values []T, key func(T) string) []T {
	result := append([]T{}, values...)
	sort.Slice(result, func(i, j int) bool { return key(result[i]) < key(result[j]) })
	return result
}

func signature(t target.Target) string {
	type direction struct {
		Category  uuid.UUID
		Specialty *uuid.UUID
	}
	values := make([]direction, 0, len(t.Directions))
	for _, d := range t.Directions {
		values = append(values, direction{d.CategoryID, d.SpecialtyID})
	}
	sort.Slice(values, func(i, j int) bool {
		a, b := values[i], values[j]
		if a.Category != b.Category {
			return a.Category.String() < b.Category.String()
		}
		if a.Specialty == nil && b.Specialty == nil {
			return false
		}
		if a.Specialty == nil {
			return true
		}
		if b.Specialty == nil {
			return false
		}
		return a.Specialty.String() < b.Specialty.String()
	})
	raw, _ := json.Marshal(struct {
		Type       string
		Year       *int
		Directions []direction
	}{t.EmploymentType, t.GraduationYear, values})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// GoalSignature is shared by reports that belong to the semantic current goal.
// A target row is updated in place, so its UUID alone is not a stable goal key.
func GoalSignature(t target.Target) string { return signature(t) }
func groupDecidable(q Requirement, abilities map[uuid.UUID]profile.Capability) bool {
	needed := q.RequiredCount
	if needed < 1 {
		needed = 1
	}
	satisfied := map[uuid.UUID]bool{}
	unknown := false
	for _, o := range q.Options {
		if o.Resolution != "resolved" {
			continue
		}
		a, ok := abilities[o.AbilityID]
		if !ok || !a.Assessed {
			unknown = true
			continue
		}
		if o.Graded && a.CurrentLevel >= o.Level {
			satisfied[o.AbilityID] = true
		}
	}
	return len(satisfied) >= needed || !unknown
}
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (View, error) {
	var view, previous View
	err := s.repo.WithinUserSnapshot(ctx, userID, func(snapshotCtx context.Context) error {
		ss, err := s.load(snapshotCtx, userID)
		if err != nil {
			if errors.Is(err, target.ErrNotFound) {
				view.Readiness = Readiness{Code: "target_missing", Message: "请先设置求职目标"}
				return nil
			}
			return err
		}
		stored, err := s.repo.Get(snapshotCtx, userID, ss.goalSignature)
		if err != nil {
			return err
		}
		view = View{Readiness: ss.readiness, SourceFingerprint: ss.hash}
		if stored == nil {
			return nil
		}
		view.Report = &stored.Report
		view.Stale = stored.SourceHash != ss.hash || ss.readiness.Code != "ready"
		if !view.Stale || ss.readiness.Code != "ready" {
			return nil
		}
		previous = view
		view, err = s.buildAndSave(snapshotCtx, userID, ss)
		return err
	})
	if err != nil && previous.Report != nil && ctx.Err() == nil {
		// Publication is transactional: a save or commit failure cannot replace
		// the last good report. Never return an uncommitted report as current.
		previous.RefreshError = "短板报告自动更新暂时失败，已保留上次结果，请重试"
		slog.Warn("refresh knowledge gap report", "user_id", userID, "error", err)
		return previous, nil
	}
	return view, err
}

func (s *Service) Analyze(ctx context.Context, userID uuid.UUID) (View, error) {
	var view View
	err := s.repo.WithinUserSnapshot(ctx, userID, func(snapshotCtx context.Context) error {
		ss, err := s.load(snapshotCtx, userID)
		if err != nil {
			if errors.Is(err, target.ErrNotFound) {
				view.Readiness = Readiness{Code: "target_missing", Message: "请先设置求职目标"}
				return ErrNotReady
			}
			return err
		}
		if ss.readiness.Code != "ready" {
			view.Readiness = ss.readiness
			return ErrNotReady
		}
		view, err = s.buildAndSave(snapshotCtx, userID, ss)
		return err
	})
	return view, err
}

func (s *Service) buildAndSave(ctx context.Context, userID uuid.UUID, ss snapshot) (View, error) {
	report := Build(ss.target.ID, ss.requirements, ss.market.Abilities, ss.profile.Capabilities)
	report.GeneratedAt = time.Now().UTC()
	if err := s.repo.Save(ctx, userID, ss.target.ID, ss.goalSignature, ss.hash, report); err != nil {
		return View{}, err
	}
	return View{Report: &report, Readiness: ss.readiness, SourceFingerprint: ss.hash}, nil
}

func Build(targetID uuid.UUID, reqs []Requirement, marketAbilities []market.AbilitySummary, capabilities []profile.Capability) Report {
	r := Report{TargetID: targetID, Gaps: []Item{}, Met: []Item{}, Preferred: []Item{}}
	users := map[uuid.UUID]profile.Capability{}
	for _, a := range capabilities {
		users[a.AbilityID] = a
	}
	markets := map[uuid.UUID]market.AbilitySummary{}
	for _, a := range marketAbilities {
		markets[a.AbilityID] = a
	}
	// Single ability conclusions use the market modal grade across all relevant JDs.
	singles := map[uuid.UUID]*Item{}
	seenJD := map[uuid.UUID]map[uuid.UUID]bool{}
	groups := map[string]*Item{}
	groupSeenJD := map[string]map[uuid.UUID]bool{}
	for _, q := range reqs {
		if q.Operator == "single" && len(q.Options) > 0 && q.Options[0].Resolution == "resolved" {
			o := q.Options[0]
			a := users[o.AbilityID]
			m := markets[o.AbilityID]
			level := m.LevelSummary.RecommendedLevel
			if q.Kind == "preferred" {
				level = m.PreferredLevelSummary.RecommendedLevel
			}
			if level == 0 {
				level = o.Level
			}
			key := o.AbilityID
			if q.Kind == "preferred" {
				key = uuid.NewSHA1(o.AbilityID, []byte("preferred"))
			}
			item := singles[key]
			if item == nil {
				item = &Item{ID: key.String(), Kind: "ability", Name: o.Name, AbilityID: &o.AbilityID, CurrentLevel: a.CurrentLevel, TargetLevel: level, UserSource: a.LevelSource, UserEvidence: a.Evidence, Evidences: []Evidence{}}
				singles[key] = item
				seenJD[key] = map[uuid.UUID]bool{}
			}
			if !seenJD[key][q.JDID] {
				item.SampleCount++
				seenJD[key][q.JDID] = true
			}
			item.Evidences = append(item.Evidences, Evidence{q.JDID, q.JDTitle, o.Evidence, o.Level, o.Source, o.Reason})
			continue
		}
		needed := q.RequiredCount
		if needed < 1 {
			needed = 1
		}
		options := map[uuid.UUID]OptionState{}
		evidences := []Evidence{}
		for _, o := range q.Options {
			if o.Resolution != "resolved" {
				continue
			}
			a := users[o.AbilityID]
			previous, exists := options[o.AbilityID]
			if !exists || o.Level > previous.RequiredLevel {
				options[o.AbilityID] = OptionState{o.Name, o.AbilityID, a.CurrentLevel, o.Level, a.Assessed, a.Assessed && a.CurrentLevel >= o.Level}
			}
			evidences = append(evidences, Evidence{q.JDID, q.JDTitle, o.Evidence, o.Level, o.Source, o.Reason})
		}
		optionIDs := make([]uuid.UUID, 0, len(options))
		for id := range options {
			optionIDs = append(optionIDs, id)
		}
		sort.Slice(optionIDs, func(i, j int) bool { return optionIDs[i].String() < optionIDs[j].String() })
		parts := []string{q.Kind, q.Operator, fmt.Sprint(needed)}
		states := []OptionState{}
		satisfied := 0
		for _, id := range optionIDs {
			state := options[id]
			parts = append(parts, id.String()+":"+fmt.Sprint(state.RequiredLevel))
			states = append(states, state)
			if state.Satisfied {
				satisfied++
			}
		}
		key := strings.Join(parts, "|")
		item := groups[key]
		if item == nil {
			item = &Item{ID: uuid.NewSHA1(uuid.Nil, []byte(key)).String(), Kind: "requirement", Name: "组合能力要求", RequiredCount: needed, SatisfiedCount: satisfied, Options: states, Evidences: []Evidence{}}
			groups[key] = item
			groupSeenJD[key] = map[uuid.UUID]bool{}
		}
		if !groupSeenJD[key][q.JDID] {
			item.SampleCount++
			groupSeenJD[key][q.JDID] = true
		}
		item.Evidences = append(item.Evidences, evidences...)
	}
	for key, item := range groups {
		if strings.HasPrefix(key, "preferred|") {
			r.Preferred = append(r.Preferred, *item)
		} else if item.SatisfiedCount >= item.RequiredCount {
			r.Met = append(r.Met, *item)
		} else {
			r.Gaps = append(r.Gaps, *item)
		}
	}
	for _, item := range singles {
		if item.ID != item.AbilityID.String() {
			r.Preferred = append(r.Preferred, *item)
		} else if item.CurrentLevel < item.TargetLevel {
			r.Gaps = append(r.Gaps, *item)
		} else {
			r.Met = append(r.Met, *item)
		}
	}
	sort.Slice(r.Gaps, func(i, j int) bool { return r.Gaps[i].Name < r.Gaps[j].Name })
	return r
}
