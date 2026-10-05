package projectrecs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

type workerSources struct {
	target     target.Target
	level      int
	marketHash string
	ready      string
}

func (s *workerSources) Current(context.Context, uuid.UUID) (target.Target, error) {
	return s.target, nil
}
func (s *workerSources) ProfileCurrent(context.Context, uuid.UUID) (market.Profile, error) {
	return market.Profile{IncludedJDCount: 10, Complete: true}, nil
}
func (s *workerSources) GetOverview(context.Context, uuid.UUID) (profile.Overview, error) {
	return profile.Overview{Complete: true, Capabilities: []profile.Capability{{Name: "Go", Assessed: true, CurrentLevel: s.level}}}, nil
}
func (s *workerSources) GetSettings(context.Context, uuid.UUID) (profile.Settings, error) {
	hours, weeks := 12, 8
	return profile.Settings{WeeklyHours: &hours, ExpectedWeeks: &weeks, ExistingExperience: "Go 服务开发"}, nil
}
func (s *workerSources) Get(context.Context, uuid.UUID) (knowledgegaps.View, error) {
	return knowledgegaps.View{Readiness: knowledgegaps.Readiness{Code: s.ready}, SourceFingerprint: s.marketHash}, nil
}

type completionRepository struct {
	Repository  // Unused pipeline methods must not be called in the empty-result path.
	job         Job
	beforeSave  func()
	completeErr error
	published   bool
	checked     bool
	failure     string
	retry       bool
}

func (r *completionRepository) Claim(context.Context, time.Duration) (Job, error)   { return r.job, nil }
func (r *completionRepository) Heartbeat(context.Context, Job, time.Duration) error { return nil }
func (r *completionRepository) Complete(ctx context.Context, _ Job, _ Report, validate func(context.Context) error) error {
	if r.beforeSave != nil {
		r.beforeSave()
	}
	if r.completeErr != nil {
		return r.completeErr
	}
	r.checked = true
	if err := validate(ctx); err != nil {
		return err
	}
	r.published = true
	return nil
}
func (r *completionRepository) Fail(_ context.Context, _ Job, code string, retry bool) error {
	r.failure, r.retry = code, retry
	return nil
}

type workerCredentials struct{}

func (workerCredentials) Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error) {
	return modelconfig.Credentials{APIKey: "test", Model: "test"}, nil
}

func TestWorkerRevalidatesInputsDuringCompletion(t *testing.T) {
	for _, change := range []string{"none", "user_level", "market_level", "target", "readiness", "database_failure", "lease_lost"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			sources := &workerSources{target: target.Target{ID: uuid.New(), EmploymentType: "internship"}, level: 2, marketHash: "market-1", ready: "ready"}
			r := &completionRepository{}
			s := NewService(r, sources, sources, sources, sources, nil)
			ss, err := s.Snapshot(ctx, uuid.New())
			if err != nil {
				t.Fatal(err)
			}
			r.job = Job{ID: uuid.New(), UserID: uuid.New(), TargetID: ss.TargetID, GoalSignature: ss.GoalSignature,
				SourceHash: ss.SourceHash, Input: ss.Input, Phase: "draft", Attempts: 1, MaxAttempts: 3, LeaseToken: uuid.New()}
			// The pipeline's early check has already passed when Complete starts.
			r.beforeSave = func() {
				switch change {
				case "user_level":
					sources.level++
				case "market_level":
					sources.marketHash = "market-2"
				case "target":
					sources.target.EmploymentType = "campus"
				case "readiness":
					sources.ready = "grading_pending"
				case "database_failure":
					r.completeErr = errors.New("database unavailable")
				case "lease_lost":
					r.completeErr = ErrLeaseLost
				}
			}
			model := &fakeGenerator{outputs: []any{draftOutput{EmptyReason: "没有合格选题"}}}
			w := NewWorker(r, s, workerCredentials{}, model, nil, time.Second)
			worked, err := w.ProcessOnce(ctx)
			if err != nil || !worked {
				t.Fatalf("process: worked=%v err=%v", worked, err)
			}
			switch change {
			case "none":
				if !r.published || !r.checked || r.failure != "" {
					t.Fatalf("valid completion: %+v", r)
				}
			case "lease_lost":
				if r.published || r.failure != "" {
					t.Fatal("stale Worker altered task or report")
				}
			case "database_failure":
				if r.published || r.failure != "recommendation_failed" || !r.retry {
					t.Fatalf("completion error was not retryable: %+v", r)
				}
			default:
				if r.published || !r.checked || r.failure != "inputs_changed" || r.retry {
					t.Fatalf("stale completion was accepted: %+v", r)
				}
			}
		})
	}
}
