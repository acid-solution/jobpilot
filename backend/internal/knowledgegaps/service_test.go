package knowledgegaps

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

type sourceStub struct {
	target   target.Target
	market   market.Profile
	profile  profile.Overview
	settings profile.Settings
}

func (s *sourceStub) Current(context.Context, uuid.UUID) (target.Target, error) { return s.target, nil }
func (s *sourceStub) ProfileCurrent(context.Context, uuid.UUID) (market.Profile, error) {
	return s.market, nil
}
func (s *sourceStub) GetOverview(context.Context, uuid.UUID) (profile.Overview, error) {
	return s.profile, nil
}
func (s *sourceStub) GetSettings(context.Context, uuid.UUID) (profile.Settings, error) {
	return s.settings, nil
}

type reportStub struct {
	requirements       []Requirement
	stored             map[string]*Stored
	saves              int
	saveErr, commitErr error
}

func (r *reportStub) LoadRequirements(context.Context, uuid.UUID, uuid.UUID) ([]Requirement, error) {
	return r.requirements, nil
}
func (r *reportStub) Get(_ context.Context, _ uuid.UUID, goal string) (*Stored, error) {
	return r.stored[goal], nil
}
func (r *reportStub) Save(_ context.Context, _, _ uuid.UUID, goal, hash string, report Report) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saves++
	r.stored[goal] = &Stored{Report: report, SourceHash: hash}
	return nil
}
func (r *reportStub) WithinUserSnapshot(ctx context.Context, _ uuid.UUID, run func(context.Context) error) error {
	previous := make(map[string]*Stored, len(r.stored))
	for key, value := range r.stored {
		previous[key] = value
	}
	err := run(ctx)
	if err == nil {
		err = r.commitErr
	}
	if err != nil {
		r.stored = previous
	}
	return err
}
func readyService() (*Service, *sourceStub, *reportStub, uuid.UUID) {
	ability := uuid.New()
	weekly, weeks := 12, 8
	sources := &sourceStub{
		target:   target.Target{ID: uuid.New(), EmploymentType: "internship"},
		market:   market.Profile{IncludedJDCount: 10, Abilities: []market.AbilitySummary{{AbilityID: ability, Name: "Go", LevelSummary: market.LevelSummary{RecommendedLevel: 3}}}},
		profile:  profile.Overview{Capabilities: []profile.Capability{{AbilityID: ability, Name: "Go", CurrentLevel: 2, Assessed: true, LevelSource: "manual"}}},
		settings: profile.Settings{WeeklyHours: &weekly, ExpectedWeeks: &weeks, ExistingExperience: "Go 项目"},
	}
	repo := &reportStub{stored: map[string]*Stored{}, requirements: []Requirement{{ID: uuid.New(), JDID: uuid.New(), Operator: "single", RequiredCount: 1,
		Kind: "required", Options: []Option{{ID: uuid.New(), AbilityID: ability, Name: "Go", Level: 3, Graded: true, Resolution: "resolved", Evidence: "独立开发 Go 服务"}}}}}
	return NewService(repo, sources, sources, sources), sources, repo, uuid.New()
}
func initialReport(t *testing.T, s *Service, user uuid.UUID) View {
	t.Helper()
	v, err := s.Analyze(context.Background(), user)
	if err != nil || v.Report == nil {
		t.Fatalf("initial report: %+v %v", v, err)
	}
	return v
}

func TestAnySourceLevelChangeAutomaticallyRefreshesExistingReport(t *testing.T) {
	for _, kind := range []string{"manual", "questionnaire", "material", "material_reassessment", "market", "jd_option_grade"} {
		t.Run(kind, func(t *testing.T) {
			s, sources, repo, user := readyService()
			initialReport(t, s, user)
			if kind == "market" {
				sources.market.Abilities[0].LevelSummary.RecommendedLevel = 4
			} else if kind == "jd_option_grade" {
				repo.requirements[0].Options[0].Level = 4
			} else {
				sources.profile.Capabilities[0].CurrentLevel = 3
				sources.profile.Capabilities[0].LevelSource = kind
			}
			v, err := s.Get(context.Background(), user)
			if err != nil || v.Stale || v.Report == nil || repo.saves != 2 {
				t.Fatalf("refresh: %+v saves=%d err=%v", v, repo.saves, err)
			}
			if kind != "market" && kind != "jd_option_grade" && len(v.Report.Met) != 1 {
				t.Fatalf("user update did not change conclusion: %+v", v.Report)
			}
			if kind == "market" && v.Report.Gaps[0].TargetLevel != 4 {
				t.Fatal("market grade not refreshed")
			}
			if kind == "jd_option_grade" && v.Report.Gaps[0].Evidences[0].Level != 4 {
				t.Fatal("option grade evidence not refreshed")
			}
		})
	}
}

func TestFreshReportAndUnrelatedSettingsDoNotResave(t *testing.T) {
	s, sources, repo, user := readyService()
	first := initialReport(t, s, user)
	hours := 20
	sources.settings.WeeklyHours = &hours
	for range 2 {
		v, err := s.Get(context.Background(), user)
		if err != nil || v.Stale || repo.saves != 1 || !v.Report.GeneratedAt.Equal(first.Report.GeneratedAt) {
			t.Fatalf("unnecessary refresh: %+v %v saves=%d", v, err, repo.saves)
		}
	}
}

func TestSourceOrderDoesNotRefreshIdenticalReport(t *testing.T) {
	s, sources, repo, user := readyService()
	goID := sources.profile.Capabilities[0].AbilityID
	pythonID := uuid.New()
	sources.profile.Capabilities = append(sources.profile.Capabilities, profile.Capability{AbilityID: pythonID, Name: "Python", CurrentLevel: 2, Assessed: true, LevelSource: "manual"})
	sources.market.Abilities = append(sources.market.Abilities, market.AbilitySummary{AbilityID: pythonID, Name: "Python", LevelSummary: market.LevelSummary{RecommendedLevel: 3}})
	repo.requirements = append(repo.requirements, Requirement{ID: uuid.New(), JDID: uuid.New(), Operator: "any_of", RequiredCount: 1, Kind: "required", Options: []Option{
		{ID: uuid.New(), AbilityID: goID, Name: "Go", Level: 3, Graded: true, Resolution: "resolved"},
		{ID: uuid.New(), AbilityID: pythonID, Name: "Python", Level: 3, Graded: true, Resolution: "resolved"},
	}})
	for i := range sources.profile.Capabilities {
		sources.profile.Capabilities[i].Evidence = []profile.Evidence{{ID: uuid.New(), Quote: "材料一"}, {ID: uuid.New(), Quote: "材料二"}}
	}
	for i := range sources.market.Abilities {
		a := &sources.market.Abilities[i]
		jd1, jd2 := uuid.New(), uuid.New()
		a.Evidences = []market.AbilityEvidence{{JobDescriptionID: jd1, JobTitle: "同名岗位", Evidence: "证据一"}, {JobDescriptionID: jd2, JobTitle: "同名岗位", Evidence: "证据二"}}
		a.LevelSummary.Evidences = []market.LevelEvidence{{JobDescriptionID: jd1, Level: 3}, {JobDescriptionID: jd2, Level: 3}}
		a.PreferredLevelSummary.Evidences = []market.LevelEvidence{{JobDescriptionID: jd1, Level: 4}, {JobDescriptionID: jd2, Level: 4}}
	}
	first := initialReport(t, s, user)
	for range 2 {
		slices.Reverse(sources.profile.Capabilities)
		for i := range sources.profile.Capabilities {
			slices.Reverse(sources.profile.Capabilities[i].Evidence)
		}
		slices.Reverse(sources.market.Abilities)
		for i := range sources.market.Abilities {
			a := &sources.market.Abilities[i]
			slices.Reverse(a.Evidences)
			slices.Reverse(a.LevelSummary.Evidences)
			slices.Reverse(a.PreferredLevelSummary.Evidences)
		}
		slices.Reverse(repo.requirements)
		for i := range repo.requirements {
			slices.Reverse(repo.requirements[i].Options)
		}
		v, err := s.Get(context.Background(), user)
		if err != nil || v.Stale || v.SourceFingerprint != first.SourceFingerprint || repo.saves != 1 {
			t.Fatalf("ordering changed fingerprint: %+v err=%v saves=%d", v, err, repo.saves)
		}
	}
}

func TestIncompleteSourcesKeepOldReportAndRefreshWhenReady(t *testing.T) {
	for _, kind := range []string{"grading_pending", "grading_failed", "review_pending", "review_failed", "unassessed", "market_incomplete"} {
		t.Run(kind, func(t *testing.T) {
			s, sources, repo, user := readyService()
			first := initialReport(t, s, user)
			sources.profile.Capabilities[0].CurrentLevel = 3
			switch kind {
			case "grading_pending":
				sources.market.AbilityGradingPendingCount = 1
			case "grading_failed":
				sources.market.AbilityGradingFailedCount = 1
			case "review_pending":
				repo.requirements[0].Options[0].Resolution = "pending_review"
			case "review_failed":
				repo.requirements[0].Options[0].Resolution = "review_failed"
			case "unassessed":
				sources.profile.Capabilities[0].Assessed = false
			case "market_incomplete":
				sources.market.IncludedJDCount = 9
			}
			v, err := s.Get(context.Background(), user)
			if err != nil || !v.Stale || v.Readiness.Code == "ready" || repo.saves != 1 || !v.Report.GeneratedAt.Equal(first.Report.GeneratedAt) {
				t.Fatalf("old report not retained: %+v %v", v, err)
			}
			sources.market.AbilityGradingPendingCount = 0
			sources.market.AbilityGradingFailedCount = 0
			sources.market.IncludedJDCount = 10
			repo.requirements[0].Options[0].Resolution = "resolved"
			sources.profile.Capabilities[0].Assessed = true
			v, err = s.Get(context.Background(), user)
			if err != nil || v.Stale || len(v.Report.Met) != 1 || repo.saves != 2 {
				t.Fatalf("ready report not refreshed: %+v %v", v, err)
			}
		})
	}
}

func TestAutomaticRefreshFailuresReturnLastPersistedReport(t *testing.T) {
	for _, kind := range []string{"save", "commit"} {
		t.Run(kind, func(t *testing.T) {
			s, sources, repo, user := readyService()
			first := initialReport(t, s, user)
			sources.profile.Capabilities[0].CurrentLevel = 3
			if kind == "save" {
				repo.saveErr = errors.New("save failed")
			} else {
				repo.commitErr = errors.New("commit failed")
			}
			v, err := s.Get(context.Background(), user)
			if err != nil || !v.Stale || v.RefreshError == "" || !v.Report.GeneratedAt.Equal(first.Report.GeneratedAt) || len(v.Report.Gaps) != 1 {
				t.Fatalf("uncommitted report exposed: %+v %v", v, err)
			}
			repo.saveErr = nil
			repo.commitErr = nil
			v, err = s.Get(context.Background(), user)
			if err != nil || v.Stale || v.RefreshError != "" || len(v.Report.Met) != 1 {
				t.Fatalf("refresh did not recover: %+v %v", v, err)
			}
		})
	}
}

func TestNoReportStillRequiresFirstAnalysisAndGoalsAreIsolated(t *testing.T) {
	s, sources, repo, user := readyService()
	v, err := s.Get(context.Background(), user)
	if err != nil || v.Report != nil || repo.saves != 0 {
		t.Fatal("first report generated implicitly")
	}
	first := initialReport(t, s, user)
	sources.target.EmploymentType = "campus"
	v, err = s.Get(context.Background(), user)
	if err != nil || v.Report != nil || repo.saves != 1 {
		t.Fatal("different goal reused old report")
	}
	sources.target.EmploymentType = "internship"
	v, err = s.Get(context.Background(), user)
	if err != nil || v.Stale || !v.Report.GeneratedAt.Equal(first.Report.GeneratedAt) {
		t.Fatal("original goal report not restored")
	}
}
