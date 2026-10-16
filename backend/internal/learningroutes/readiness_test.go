package learningroutes

import (
	"context"
	"errors"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/google/uuid"
)

type readinessProjects struct {
	snapshot projectrecs.Snapshot
	view     projectrecs.View
}

func (p *readinessProjects) Snapshot(context.Context, uuid.UUID) (projectrecs.Snapshot, error) {
	return p.snapshot, nil
}
func (p *readinessProjects) Get(context.Context, uuid.UUID) (projectrecs.View, error) {
	return p.view, nil
}

type readinessProfiles struct{ overview profile.Overview }

func (p *readinessProfiles) GetOverview(context.Context, uuid.UUID) (profile.Overview, error) {
	return p.overview, nil
}

type readinessGaps struct{ view knowledgegaps.View }

func (g *readinessGaps) CurrentComparison(context.Context, uuid.UUID) (knowledgegaps.View, error) {
	return g.view, nil
}

type readinessRepo struct {
	Repository
	enqueued int
}

func (r *readinessRepo) WithinUserTransaction(ctx context.Context, _ uuid.UUID, fn func(context.Context) error) error {
	return fn(ctx)
}
func (r *readinessRepo) Get(context.Context, uuid.UUID, string, uuid.UUID, string) (*Version, *JobView, *ImportRequest, error) {
	return nil, nil, nil, nil
}
func (r *readinessRepo) Enqueue(context.Context, uuid.UUID, Snapshot) (JobView, error) {
	r.enqueued++
	return JobView{}, nil
}

func TestRouteRequiresCompleteProfilesAndCurrentProject(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   string
		change func(*readinessProjects, *readinessProfiles, *readinessGaps)
	}{
		{"no target", "target_missing", func(p *readinessProjects, _ *readinessProfiles, _ *readinessGaps) {
			p.snapshot.GoalSignature = ""
			p.snapshot.Readiness.Code = "target_missing"
		}},
		{"no profiles or recommendation", "market_incomplete", func(p *readinessProjects, _ *readinessProfiles, _ *readinessGaps) {
			p.snapshot.Input.IncludedJDCount = 0
			p.snapshot.Readiness.Code = "market_incomplete"
			p.view = projectrecs.View{}
		}},
		{"selected historical project but only nine JDs", "market_incomplete", func(p *readinessProjects, _ *readinessProfiles, _ *readinessGaps) {
			p.snapshot.Input.IncludedJDCount = 9
		}},
		{"profile incomplete", "profile_incomplete", func(_ *readinessProjects, p *readinessProfiles, _ *readinessGaps) { p.overview.Complete = false }},
		{"necessary information missing", "profile_incomplete", func(p *readinessProjects, _ *readinessProfiles, _ *readinessGaps) { p.snapshot.Input.WeeklyHours = 0 }},
		{"ability review unfinished", "ability_review_pending", func(p *readinessProjects, _ *readinessProfiles, _ *readinessGaps) {
			p.snapshot.Readiness.PendingReviewCount = 1
		}},
		{"grading failed", "grading_pending", func(p *readinessProjects, _ *readinessProfiles, _ *readinessGaps) {
			p.snapshot.Readiness.FailedGradingCount = 1
		}},
		{"no recommendation", "project_missing", func(p *readinessProjects, _ *readinessProfiles, _ *readinessGaps) { p.view = projectrecs.View{} }},
		{"recommendation not selected", "project_missing", func(p *readinessProjects, _ *readinessProfiles, _ *readinessGaps) { p.view.SelectedProjectID = "" }},
		{"selected project needs review", "project_stale", func(p *readinessProjects, _ *readinessProfiles, _ *readinessGaps) { p.view.SelectionNeedsReview = true }},
		{"comparison not ready", "grading_pending", func(_ *readinessProjects, _ *readinessProfiles, g *readinessGaps) {
			g.view.Readiness.Code = "grading_pending"
		}},
		{"comparison still has failed reviews", "ability_review_pending", func(_ *readinessProjects, _ *readinessProfiles, g *readinessGaps) {
			g.view.Readiness.FailedReviewCount = 1
		}},
		{"all prerequisites ready", "ready", func(_ *readinessProjects, _ *readinessProfiles, _ *readinessGaps) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projects := &readinessProjects{
				snapshot: projectrecs.Snapshot{TargetID: uuid.New(), GoalSignature: "goal", Readiness: knowledgegaps.Readiness{Code: "ready"}, Input: projectrecs.Input{IncludedJDCount: 10, WeeklyHours: 3, ExpectedWeeks: 2, ExistingExperience: "测试经历"}},
				view:     projectrecs.View{SelectedProjectID: "project", Report: &projectrecs.Report{ID: uuid.New(), Projects: []projectrecs.Project{{Draft: projectrecs.Draft{ID: "project", Title: "测试项目"}}}}},
			}
			profiles := &readinessProfiles{profile.Overview{Complete: true, MarketProfileReady: true}}
			gaps := &readinessGaps{knowledgegaps.View{Readiness: knowledgegaps.Readiness{Code: "ready"}}}
			tc.change(projects, profiles, gaps)
			repo := &readinessRepo{}
			service := NewService(repo, projects, gaps, profiles, credsFixture{}, nil, "")
			user := uuid.New()
			view, err := service.Get(t.Context(), user)
			if err != nil || view.Readiness.Code != tc.code {
				t.Fatalf("readiness: got %q, want %q, error %v", view.Readiness.Code, tc.code, err)
			}
			_, err = service.Generate(t.Context(), user, GenerateInput{Config: sampleConfig()})
			if tc.code != "ready" {
				if !errors.Is(err, ErrNotReady) || repo.enqueued != 0 {
					t.Fatalf("blocked request created a model task: %v, %d", err, repo.enqueued)
				}
			} else if err != nil || repo.enqueued != 1 || !view.Preparation.MarketComplete || !view.Preparation.ProfileComplete || !view.Preparation.AnalysisReady || !view.Preparation.ProjectSelected || view.WeeklyHours != 3 {
				t.Fatalf("ready request not admitted: %+v, %v", view, err)
			}
		})
	}
}
