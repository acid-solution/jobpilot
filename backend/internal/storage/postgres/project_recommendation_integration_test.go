package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/projectrecs"
	"github.com/LeoninCS/jobpilot-next/backend/internal/storage/postgres/migrations"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestProjectRecommendationLeaseAndAtomicReplacement(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrations.Files)
	if err = goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err = goose.Up(db, "sql"); err != nil {
		t.Fatal(err)
	}
	var alreadyActive int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_recommendation_jobs WHERE status IN ('queued','running')`).Scan(&alreadyActive); err != nil {
		t.Fatal(err)
	}
	if alreadyActive > 0 {
		t.Skip("integration database has an active recommendation job")
	}
	user, target := uuid.New(), uuid.New()
	_, err = db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type) VALUES($1,$2,'测试目标','internship')`, target, user)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, cleanupErr := db.Exec(`DELETE FROM job_targets WHERE id=$1`, target); cleanupErr != nil {
			t.Errorf("clean up recommendation test target: %v", cleanupErr)
		}
	}()
	r := NewProjectRecommendationRepository(db)
	ss := projectrecs.Snapshot{TargetID: target, GoalSignature: "test-goal-" + uuid.NewString(), SourceHash: "hash-1", Input: projectrecs.Input{Goal: "测试目标"}}
	if _, err = r.Enqueue(ctx, user, ss, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Enqueue(ctx, user, ss, ""); !errors.Is(err, projectrecs.ErrConflict) {
		t.Fatalf("duplicate job: %v", err)
	}
	otherTarget := uuid.New()
	if _, err = db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,is_current) VALUES($1,$2,'另一目标','internship',FALSE)`, otherTarget, user); err != nil {
		t.Fatal(err)
	}
	other := ss
	other.TargetID = otherTarget
	other.GoalSignature = "test-goal-" + uuid.NewString()
	if _, err = r.Enqueue(ctx, user, other, ""); err != nil {
		t.Fatalf("different target should have its own active job: %v", err)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM job_targets WHERE id=$1`, otherTarget); err != nil {
		t.Fatal(err)
	}
	first, err := r.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first.UserID != user || first.TargetID != target {
		t.Fatal("claimed an unrelated recommendation job")
	}
	if _, err = r.Claim(ctx, 5*time.Minute); !errors.Is(err, projectrecs.ErrNoJob) {
		t.Fatalf("second worker claimed active job: %v", err)
	}
	if err = r.Heartbeat(ctx, first, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	drafts := []projectrecs.Draft{{ID: "p1"}, {ID: "p2"}}
	if err = r.SaveDrafts(ctx, first, drafts); err != nil {
		t.Fatal(err)
	}
	if err = r.SaveResearchProgress(ctx, first, []projectrecs.Research{{DraftID: "p1"}}); err != nil {
		t.Fatal(err)
	}
	_, progress, err := r.Get(ctx, user, ss.GoalSignature)
	if err != nil || progress == nil || progress.Phase != "research" || progress.DraftCount != 2 || progress.ResearchedCount != 1 {
		t.Fatalf("partial research progress=%+v err=%v", progress, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE project_recommendation_jobs SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err = r.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := r.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.LeaseToken == first.LeaseToken {
		t.Fatal("job was not reclaimed with a new token")
	}
	if second.Phase != "research" || len(second.Drafts) != 2 || len(second.Research) != 1 || second.Research[0].DraftID != "p1" {
		t.Fatalf("partial research was not restored: %+v", second)
	}
	if err = r.SaveResearchProgress(ctx, first, []projectrecs.Research{{DraftID: "p2"}}); !errors.Is(err, projectrecs.ErrLeaseLost) {
		t.Fatalf("stale worker updated progress: %v", err)
	}
	if err = r.SaveResearchProgress(ctx, second, []projectrecs.Research{{DraftID: "p1"}, {DraftID: "p2"}}); err != nil {
		t.Fatal(err)
	}
	if err = r.SaveResearch(ctx, second, []projectrecs.Research{{DraftID: "p1"}, {DraftID: "p2"}}); err != nil {
		t.Fatal(err)
	}
	_, progress, err = r.Get(ctx, user, ss.GoalSignature)
	if err != nil || progress == nil || progress.Phase != "compare" || progress.ResearchedCount != 2 {
		t.Fatalf("completed research progress=%+v err=%v", progress, err)
	}
	report := projectrecs.Report{ID: uuid.New(), TargetTitle: "测试目标", Projects: []projectrecs.Project{{Draft: projectrecs.Draft{ID: "p1", Title: "项目一"}}}}
	if err = r.Complete(ctx, first, report); !errors.Is(err, projectrecs.ErrLeaseLost) {
		t.Fatalf("stale worker wrote result: %v", err)
	}
	if err = r.Complete(ctx, second, report); err != nil {
		t.Fatal(err)
	}
	if err = r.Select(ctx, user, ss.GoalSignature, report.ID, "p1"); err != nil {
		t.Fatal(err)
	}
	stored, _, err := r.Get(ctx, user, ss.GoalSignature)
	if err != nil || stored.SelectedProjectID != "p1" {
		t.Fatalf("selected=%+v err=%v", stored, err)
	}
	ss.SourceHash = "hash-2"
	if _, err = r.Enqueue(ctx, user, ss, ""); err != nil {
		t.Fatal(err)
	}
	third, err := r.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	newReport := projectrecs.Report{ID: uuid.New(), TargetTitle: "测试目标", Projects: []projectrecs.Project{}}
	if err = r.Complete(ctx, third, newReport); err != nil {
		t.Fatal(err)
	}
	stored, _, err = r.Get(ctx, user, ss.GoalSignature)
	if err != nil || stored.Report.ID != newReport.ID || stored.SelectedProjectID != "" {
		t.Fatalf("replacement=%+v err=%v", stored, err)
	}
}
