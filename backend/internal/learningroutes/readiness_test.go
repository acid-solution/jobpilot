package learningroutes

import (
	"context"
	"errors"
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/LeoninCS/jobpilot-next/backend/internal/readiness"
	"github.com/google/uuid"
)

type readinessProjects struct {
	snapshot projectrecs.Snapshot
	view     projectrecs.View
	calls    int
}

func (p *readinessProjects) PlanningSnapshot(context.Context, uuid.UUID) (projectrecs.Snapshot, projectrecs.View, error) {
	p.calls++
	return p.snapshot, p.view, nil
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
		name, code string
		change     func(*readinessProjects)
	}{
		{"no target", "target_missing", func(p *readinessProjects) {
			p.snapshot.GoalSignature = ""
			p.snapshot.Readiness = knowledgegaps.Readiness{Code: "target_missing"}
		}},
		{"only nine JDs", "market_incomplete", func(p *readinessProjects) {
			p.snapshot.Readiness = readiness.Evaluate(readiness.State{IncludedJDCount: 9}, true)
		}},
		{"necessary information missing", "profile_incomplete", func(p *readinessProjects) {
			p.snapshot.Readiness = readiness.Evaluate(readiness.State{IncludedJDCount: 10}, false)
		}},
		{"unassessed requirements", "profile_incomplete", func(p *readinessProjects) {
			p.snapshot.Readiness = readiness.Evaluate(readiness.State{IncludedJDCount: 10, MissingAbilityCount: 1}, true)
		}},
		{"review unfinished", "ability_review_pending", func(p *readinessProjects) {
			p.snapshot.Readiness = readiness.Evaluate(readiness.State{IncludedJDCount: 10, PendingReviewCount: 1}, true)
		}},
		{"review failed", "ability_review_pending", func(p *readinessProjects) {
			p.snapshot.Readiness = readiness.Evaluate(readiness.State{IncludedJDCount: 10, FailedReviewCount: 1}, true)
		}},
		{"grading failed", "grading_pending", func(p *readinessProjects) {
			p.snapshot.Readiness = readiness.Evaluate(readiness.State{IncludedJDCount: 10, FailedGradingCount: 1}, true)
		}},
		{"no recommendation", "project_missing", func(p *readinessProjects) { p.view = projectrecs.View{} }},
		{"not selected", "project_missing", func(p *readinessProjects) { p.view.SelectedProjectID = "" }},
		{"project stale", "project_stale", func(p *readinessProjects) { p.view.SelectionNeedsReview = true }},
		{"ready", "ready", func(*readinessProjects) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projects := &readinessProjects{snapshot: projectrecs.Snapshot{TargetID: uuid.New(), GoalSignature: "goal", Readiness: readiness.Evaluate(readiness.State{IncludedJDCount: 10}, true), Input: projectrecs.Input{IncludedJDCount: 10, WeeklyHours: 3, ExpectedWeeks: 2, ExistingExperience: "测试经历"}}, view: projectrecs.View{SelectedProjectID: "project", Report: &projectrecs.Report{ID: uuid.New(), Projects: []projectrecs.Project{{Draft: projectrecs.Draft{ID: "project", Title: "测试项目"}}}}}}
			tc.change(projects)
			repo := &readinessRepo{}
			service := NewService(repo, projects, credsFixture{}, nil, "")
			user := uuid.New()
			view, err := service.Get(t.Context(), user)
			if err != nil || view.Readiness.Code != tc.code {
				t.Fatalf("got %q want %q: %v", view.Readiness.Code, tc.code, err)
			}
			if projects.calls != 1 {
				t.Fatalf("one route read loaded sources %d times", projects.calls)
			}
			if view.Preparation.AnalysisReady != projects.snapshot.Readiness.ReviewsAndGradingComplete {
				t.Fatal("preparation review/grading step was conflated with user-profile completion")
			}
			_, err = service.Generate(t.Context(), user, GenerateInput{Config: sampleConfig()})
			if tc.code != "ready" {
				if !errors.Is(err, ErrNotReady) || repo.enqueued != 0 {
					t.Fatalf("blocked request created a task: %v %d", err, repo.enqueued)
				}
			} else if err != nil || repo.enqueued != 1 || !view.Preparation.MarketComplete || !view.Preparation.ProfileComplete || !view.Preparation.AnalysisReady || !view.Preparation.ProjectSelected {
				t.Fatalf("ready request rejected: %+v %v", view, err)
			}
		})
	}
}
