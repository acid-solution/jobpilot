package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/LeoninCS/jobpilot-next/backend/internal/agent"
	"github.com/LeoninCS/jobpilot-next/backend/internal/learningroutes"
	"github.com/LeoninCS/jobpilot-next/backend/internal/modelconfig"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

type routeTestCredentials struct{}

func (routeTestCredentials) Credentials(context.Context, uuid.UUID) (modelconfig.Credentials, error) {
	return modelconfig.Credentials{APIKey: "test-only", Model: "mock"}, nil
}

type routeModel struct{ calls int }

func (m *routeModel) GenerateJSONWithLimit(_ context.Context, _, _, _, input string, _ int, output any) error {
	m.calls++
	var in learningroutes.Input
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return err
	}
	roles := []string{"project", "knowledge"}
	if in.KnowledgeOnly {
		roles = []string{"knowledge"}
	}
	plans := []learningroutes.Plan{}
	for _, role := range roles {
		plans = append(plans, learningroutes.Plan{Role: role, Title: role, Objective: "可检查成果", Stages: []learningroutes.Stage{{Title: "阶段", Outcome: "结果", Tasks: []learningroutes.Task{{ID: role + "-task", Title: "完成任务", Description: "执行说明", CompletionCriteria: "检查结果", EstimateMinutes: 30}}}}})
	}
	raw, _ := json.Marshal(map[string]any{"plans": plans})
	return json.Unmarshal(raw, output)
}

type routeRemote struct {
	calls    int
	requests []learningroutes.ImportRequest
	before   func()
	lose     bool
}

func (r *routeRemote) Enabled() bool { return true }
func (r *routeRemote) Send(_ context.Context, _ string, in learningroutes.ImportRequest) (*learningroutes.Receipt, error) {
	r.calls++
	r.requests = append(r.requests, in)
	if r.before != nil {
		r.before()
	}
	if r.lose {
		return nil, errors.New("response lost")
	}
	return &learningroutes.Receipt{ImportID: uuid.NewString(), GoalID: uuid.NewString(), SourceRouteID: in.Payload.SourceRouteID, SourceVersion: in.Payload.SourceVersion, ContentDigest: in.Digest, Plans: []learningroutes.PlanRef{{Role: "project", PlanID: uuid.NewString(), PlanVersionID: uuid.NewString(), VersionNo: 1}, {Role: "knowledge", PlanID: uuid.NewString(), PlanVersionID: uuid.NewString(), VersionNo: 1}}}, nil
}
func TestLearningRouteGenerationVersionAndFrozenImportIntegration(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	projects, projectRepo, profiles, user, goID := recommendationRaceFixture(t, db, ctx)
	ss, err := projects.Snapshot(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = projectRepo.Enqueue(ctx, user, ss, ""); err != nil {
		t.Fatal(err)
	}
	job, err := projectRepo.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	report := projectrecs.Report{ID: uuid.New(), TargetTitle: job.Input.Goal, Projects: []projectrecs.Project{{Draft: projectrecs.Draft{ID: "p1", Title: "项目", Summary: "真实主体范围", Scope: []string{"实现服务"}}}}, GeneratedAt: time.Now()}
	if err = projectRepo.Complete(ctx, job, report, recommendationValidator(projects, job)); err != nil {
		t.Fatal(err)
	}
	if _, err = projects.Select(ctx, user, report.ID, "p1"); err != nil {
		t.Fatal(err)
	}
	repo := NewLearningRouteRepository(db)
	remote := &routeRemote{}
	creds := routeTestCredentials{}
	service := learningroutes.NewService(repo, projects, creds, remote, "http://127.0.0.1:5174")
	model := &routeModel{}
	worker := learningroutes.NewWorker(repo, service, creds, model)
	cfg := learningroutes.Config{Project: learningroutes.PlanConfig{Mode: "sequence", WeeklyMinutes: 360}, Knowledge: learningroutes.PlanConfig{Mode: "sequence", WeeklyMinutes: 240}}
	// No saved gaps report prerequisite.
	v, err := service.Generate(ctx, user, learningroutes.GenerateInput{Config: cfg})
	if err != nil || v.Job == nil {
		t.Fatal("generation failed", err)
	}
	if _, err = worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	v, err = service.Get(ctx, user)
	if err != nil || v.Version == nil || len(v.Version.Plans) != 2 {
		t.Fatal("missing route", err)
	}
	first := *v.Version
	// Grade-only changes retain report and selection but refresh knowledge.
	if _, err = profiles.SetCapabilityLevel(ctx, user, goID, 3); err != nil {
		t.Fatal(err)
	}
	p, err := projects.Get(ctx, user)
	if err != nil || p.Stale || p.SelectedProjectID != "p1" {
		t.Fatal("grade changed project recommendation", err)
	}
	v, err = service.Get(ctx, user)
	if err != nil || !v.KnowledgeNeedsUpdate || v.ProjectNeedsReview {
		t.Fatal("wrong dependency boundary", err)
	}
	confirmation := learningroutes.ConfirmInput{VersionID: first.ID, Revision: first.Revision, ContentHash: first.ContentHash}
	if _, err = service.AdmitImport(ctx, user, confirmation); !errors.Is(err, learningroutes.ErrInputsChanged) {
		t.Fatal("old knowledge confirmation accepted", err)
	}
	if _, err = service.Generate(ctx, user, learningroutes.GenerateInput{Config: cfg, KnowledgeOnly: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	v, err = service.Get(ctx, user)
	if err != nil || v.Version.Number != 2 || learningroutes.Hash(v.Version.Plans[0]) != learningroutes.Hash(first.Plans[0]) {
		t.Fatal("project changed on knowledge update", err)
	}
	confirmation = learningroutes.ConfirmInput{VersionID: v.Version.ID, Revision: v.Version.Revision, ContentHash: v.Version.ContentHash}
	frozen, err := service.AdmitImport(ctx, user, confirmation)
	if err != nil {
		t.Fatal(err)
	}
	// A profile edit during the external call proves no user lock spans network I/O.
	remote.before = func() {
		lockCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if e := NewAgentRepository(db).WithinUserTransaction(lockCtx, user, func(txCtx context.Context) error { _, e := profiles.SetCapabilityLevel(txCtx, user, goID, 4); return e }); e != nil {
			t.Fatal("external call held account lock", e)
		}
	}
	remote.lose = true
	if err = service.Dispatch(ctx, user, frozen.ID, "source-access-test"); err != nil {
		t.Fatal(err)
	}
	saved, err := service.GetImport(ctx, user, frozen.ID)
	if err != nil || saved.Status != "unknown" {
		t.Fatal("lost response marked success", err)
	}
	remote.before = nil
	remote.lose = false
	if err = service.Dispatch(ctx, user, frozen.ID, "source-access-test"); err != nil {
		t.Fatal(err)
	}
	saved, err = service.GetImport(ctx, user, frozen.ID)
	if err != nil || saved.Status != "succeeded" || remote.requests[0].Digest != remote.requests[1].Digest || remote.requests[0].Key != remote.requests[1].Key {
		t.Fatal("retry changed frozen payload", err)
	}
	if err = service.Dispatch(ctx, user, frozen.ID, "source-access-test"); err != nil || remote.calls != 2 {
		t.Fatal("duplicate local dispatch")
	}
	if _, err = service.GetImport(ctx, uuid.New(), frozen.ID); !errors.Is(err, learningroutes.ErrNotFound) {
		t.Fatal("cross-account import leak")
	}
	if model.calls != 2 {
		t.Fatal("unexpected automatic model call")
	}
	t.Run("expired generation and transactional validation", func(t *testing.T) {
		if _, err := service.Generate(ctx, user, learningroutes.GenerateInput{Config: cfg}); err != nil {
			t.Fatal(err)
		}
		old, err := repo.Claim(ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `UPDATE learning_route_jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
			t.Fatal(err)
		}
		if err = repo.RecoverExpired(ctx); err != nil {
			t.Fatal(err)
		}
		current, err := repo.Claim(ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if current.ID != old.ID || current.LeaseToken == old.LeaseToken {
			t.Fatal("task not reclaimed")
		}
		for _, err = range []error{repo.Heartbeat(ctx, old, time.Minute), repo.SaveModelPlans(ctx, old, first.Plans), repo.Complete(ctx, old, first.Plans, func(context.Context) error { return nil })} {
			if !errors.Is(err, learningroutes.ErrLeaseLost) {
				t.Fatal("expired worker wrote result", err)
			}
		}
		before, _, _, err := repo.Get(ctx, user, current.GoalSignature, report.ID, "p1")
		if err != nil {
			t.Fatal(err)
		}
		if err = repo.Complete(ctx, current, first.Plans, func(context.Context) error { return learningroutes.ErrInputsChanged }); !errors.Is(err, learningroutes.ErrInputsChanged) {
			t.Fatal(err)
		}
		after, _, _, err := repo.Get(ctx, user, current.GoalSignature, report.ID, "p1")
		if err != nil || after.ID != before.ID {
			t.Fatal("failed final validation replaced route", err)
		}
		if err = repo.Fail(ctx, current, "inputs_changed", "", false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("external action survives restart with import identity", func(t *testing.T) {
		actions := NewAgentRepository(db)
		conv, err := actions.Create(ctx, user, ss.GoalSignature, "路线导入")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = actions.BeginRun(ctx, user, ss.GoalSignature, conv.ID); err != nil {
			t.Fatal(err)
		}
		versions, err := actions.ReadResourceVersions(ctx, user, []string{"learning_routes"})
		if err != nil {
			t.Fatal(err)
		}
		action, err := actions.CreateAction(ctx, conv.ID, user, ss.GoalSignature, "route_import", json.RawMessage(`{}`), "导入学习路线", agent.Snapshot{Hash: frozen.Digest, Versions: versions})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := actions.ClaimAction(ctx, action.ID, conv.ID)
		if err != nil || !claimed {
			t.Fatal(err)
		}
		if err = actions.WithinUserTransaction(ctx, user, func(txCtx context.Context) error { return actions.BindExternalImport(txCtx, action.ID, frozen.ID) }); err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `UPDATE agent_conversations SET run_expires_at=now()-interval '1 second' WHERE id=$1`, conv.ID); err != nil {
			t.Fatal(err)
		}
		if err = actions.RecoverRuns(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := actions.GetAction(ctx, user, ss.GoalSignature, action.ID)
		if err != nil || got.Status != "failed" || !strings.Contains(string(got.Result), frozen.ID.String()) {
			t.Fatal("restart lost immutable import ID", err)
		}
		conversation, err := actions.Get(ctx, user, ss.GoalSignature, conv.ID)
		if err != nil || len(conversation.Messages) == 0 || !strings.Contains(conversation.Messages[len(conversation.Messages)-1].Content, frozen.ID.String()) {
			t.Fatal("recovery failed to expose queryable receipt", err)
		}
	})
}
