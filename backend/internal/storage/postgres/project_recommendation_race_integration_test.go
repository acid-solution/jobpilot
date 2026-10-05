package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

func recommendationRaceFixture(t *testing.T, db *sql.DB, ctx context.Context) (*projectrecs.Service, *ProjectRecommendationRepository, *profile.Service, uuid.UUID, uuid.UUID) {
	t.Helper()
	user, targetID := uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status) VALUES($1,$2,'后端开发','internship','[]'::jsonb,'valid')`, targetID, user)
	var goID uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT id FROM abilities WHERE name='Go' AND is_active LIMIT 1`).Scan(&goID); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		jdID := uuid.New()
		exec(`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status,title,responsibilities)
			VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'included','valid','Go 后端岗位','["独立开发 Go 服务"]'::jsonb)`, jdID, user, targetID, fmt.Sprintf("岗位 %d：要求独立开发 Go 服务", i))
		exec(`WITH requirement AS (
			INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,requirement_kind,evidence,sort_order)
			VALUES($1,'single',1,'required','独立开发 Go 服务',1) RETURNING id), option AS (
			INSERT INTO job_description_ability_requirement_options(requirement_id,ability_id,raw_label,evidence,sort_order)
			SELECT id,$2,'Go','独立开发 Go 服务',1 FROM requirement RETURNING id)
			INSERT INTO jd_ability_option_levels(option_id,job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version)
			SELECT id,$1,$2,3,'explicit','required','独立开发 Go 服务','对照标准可独立开发',.9,'test' FROM option`, jdID, goID)
		exec(`INSERT INTO jd_ability_level_assessments(job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version)
			VALUES($1,$2,3,'explicit','required','独立开发 Go 服务','对照标准可独立开发',.9,'test')`, jdID, goID)
		exec(`INSERT INTO jd_ability_level_jobs(user_id,target_id,job_description_id,status,input_fingerprint,result_fingerprint,result_snapshot,prompt_version)
			VALUES($1,$2,$3,'succeeded',jd_ability_grading_fingerprint($3,$4),jd_ability_grading_fingerprint($3,$4),jd_ability_grading_snapshot($3),$4)`, user, targetID, jdID, abilitygrading.PromptVersion)
	}
	profiles := profile.NewService(NewProfileRepository(db), nil)
	hours, weeks := 12, 8
	if _, err := profiles.SaveSettings(ctx, user, profile.Settings{WeeklyHours: &hours, ExpectedWeeks: &weeks, ExistingExperience: "Go 项目开发"}); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.SetCapabilityLevel(ctx, user, goID, 2); err != nil {
		t.Fatal(err)
	}
	targets := target.NewService(NewTargetRepository(db))
	markets := market.NewService(NewMarketRepository(db), targets)
	gaps := knowledgegaps.NewService(NewKnowledgeGapsRepository(db), targets, markets, profiles)
	repo := NewProjectRecommendationRepository(db)
	s := projectrecs.NewService(repo, targets, markets, profiles, gaps, nil)
	ss, err := s.Snapshot(ctx, user)
	if err != nil || ss.Readiness.Code != "ready" {
		t.Fatalf("fixture not ready: %+v %v", ss.Readiness, err)
	}
	return s, repo, profiles, user, goID
}

func recommendationValidator(s *projectrecs.Service, job projectrecs.Job) func(context.Context) error {
	return func(ctx context.Context) error {
		ss, err := s.Snapshot(ctx, job.UserID)
		if err != nil {
			return err
		}
		if ss.TargetID != job.TargetID || ss.GoalSignature != job.GoalSignature || ss.SourceHash != job.SourceHash || ss.Readiness.Code != "ready" {
			return projectrecs.ErrInputsChanged
		}
		return nil
	}
}

type emptyRecommendationModel struct{}

func (emptyRecommendationModel) GenerateJSON(_ context.Context, _, _, _, _ string, output any) error {
	return json.Unmarshal([]byte(`{"drafts":[],"empty_reason":"本次没有合格项目"}`), output)
}

type delayedRecommendationCompletion struct {
	*ProjectRecommendationRepository
	reached chan struct{}
	proceed chan struct{}
}

func (r *delayedRecommendationCompletion) Complete(ctx context.Context, job projectrecs.Job, report projectrecs.Report, validate func(context.Context) error) error {
	close(r.reached) // The Worker's lock-free fingerprint check already passed.
	select {
	case <-r.proceed:
	case <-ctx.Done():
		return ctx.Err()
	}
	return r.ProjectRecommendationRepository.Complete(ctx, job, report, validate)
}

func waitForRecommendationLockWait(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var waiting bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("concurrent operation never waited on the account lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestProjectRecommendationChecksInputsAfterUserLockIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, repo, profiles, user, goID := recommendationRaceFixture(t, db, ctx)
	ss, err := s.Snapshot(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(ctx, user, ss, ""); err != nil {
		t.Fatal(err)
	}
	initial, err := repo.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	old := projectrecs.Report{ID: uuid.New(), Projects: []projectrecs.Project{{Draft: projectrecs.Draft{ID: "chosen", Title: "已选项目"}}}}
	if err = repo.Complete(ctx, initial, old, recommendationValidator(s, initial)); err != nil {
		t.Fatal(err)
	}
	if err = repo.Select(ctx, user, ss.GoalSignature, old.ID, "chosen"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(ctx, user, ss, ""); err != nil {
		t.Fatal(err)
	}
	paused := &delayedRecommendationCompletion{repo, make(chan struct{}), make(chan struct{})}
	w := projectrecs.NewWorker(paused, s, transactionTestCredentials{}, emptyRecommendationModel{}, nil, time.Second)
	done := make(chan error, 1)
	go func() { _, err := w.ProcessOnce(ctx); done <- err }()
	select {
	case <-paused.reached:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// User wins the lock after the early check, and edits while Complete waits.
	edit, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer edit.Rollback()
	if err = lockUserMutationTx(ctx, edit, user); err != nil {
		t.Fatal(err)
	}
	close(paused.proceed)
	waitForRecommendationLockWait(t, ctx, db)
	editCtx := context.WithValue(ctx, transactionKey{}, transactionBinding{pool: db, tx: edit})
	if _, err = profiles.SetCapabilityLevel(editCtx, user, goID, 4); err != nil {
		t.Fatal(err)
	}
	if err = edit.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	stored, job, err := repo.Get(ctx, user, ss.GoalSignature)
	if err != nil || stored == nil || stored.Report.ID != old.ID || stored.SelectedProjectID != "chosen" || job.Status != "failed" || job.ErrorCode != "inputs_changed" {
		t.Fatalf("stale completion changed the report/selection or succeeded: stored=%+v job=%+v err=%v", stored, job, err)
	}
	// New inputs can create a fresh job. Only successful publication clears the selection.
	next, err := s.Snapshot(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(ctx, user, next, ""); err != nil {
		t.Fatal(err)
	}
	w = projectrecs.NewWorker(repo, s, transactionTestCredentials{}, emptyRecommendationModel{}, nil, time.Second)
	if _, err = w.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stored, job, err = repo.Get(ctx, user, next.GoalSignature)
	if err != nil || stored == nil || stored.Report.ID == old.ID || stored.SourceHash != next.SourceHash || stored.SelectedProjectID != "" || job.Status != "succeeded" {
		t.Fatalf("fresh completion failed: stored=%+v job=%+v err=%v", stored, job, err)
	}
}

func TestProjectRecommendationKeepsUserLockUntilCommitIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, repo, profiles, user, goID := recommendationRaceFixture(t, db, ctx)
	ss, err := s.Snapshot(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(ctx, user, ss, ""); err != nil {
		t.Fatal(err)
	}
	job, err := repo.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	validated, save := make(chan struct{}), make(chan struct{})
	completed := make(chan error, 1)
	go func() {
		completed <- repo.Complete(ctx, job, projectrecs.Report{ID: uuid.New()}, func(commitCtx context.Context) error {
			if err := recommendationValidator(s, job)(commitCtx); err != nil {
				return err
			}
			close(validated)
			select {
			case <-save:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-validated:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	edited := make(chan error, 1)
	go func() {
		err := NewAgentRepository(db).WithinUserTransaction(ctx, user, func(editCtx context.Context) error {
			_, err := profiles.SetCapabilityLevel(editCtx, user, goID, 4)
			return err
		})
		edited <- err
	}()
	// The Agent lock helper retries rather than blocking a pool connection.
	waitCtx, cancelWait := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelWait()
	select {
	case err := <-edited:
		t.Fatalf("edit crossed final validation/publication: %v", err)
	case <-waitCtx.Done():
	}
	close(save)
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if err := <-edited; err != nil {
		t.Fatal(err)
	}
	view, err := s.Get(ctx, user)
	if err != nil || view.Report == nil || !view.Stale {
		t.Fatalf("later profile edit did not mark report stale: %+v %v", view, err)
	}
	if !errors.Is(recommendationValidator(s, job)(ctx), projectrecs.ErrInputsChanged) {
		t.Fatal("later edit not detected")
	}
}
