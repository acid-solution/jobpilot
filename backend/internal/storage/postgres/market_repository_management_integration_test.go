package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/google/uuid"
)

func TestMarketRepositoryJDManagement(t *testing.T) {
	databaseURL := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	userID, targetID := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = database.ExecContext(context.Background(), `DELETE FROM job_descriptions WHERE target_id=$1`, targetID)
		_, _ = database.ExecContext(context.Background(), `DELETE FROM job_targets WHERE id=$1`, targetID)
	})
	if _, err = database.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status) VALUES($1,$2,'后端开发','internship','[]'::jsonb,'valid')`, targetID, userID); err != nil {
		t.Fatal(err)
	}

	repository := NewMarketRepository(database)
	original := "负责 Go 后端服务开发，完成数据库建模、接口实现、自动化测试和线上问题排查。"
	created, err := repository.CreateWithAnalysisJob(ctx, userID, targetID, original, testJDHash(original))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.CreateWithAnalysisJob(ctx, userID, targetID, original, testJDHash(original)); !errors.Is(err, market.ErrDuplicateJD) {
		t.Fatalf("expected duplicate error, got %v", err)
	}

	if _, err = database.ExecContext(ctx, `UPDATE job_descriptions SET title='Go 后端实习生',company='示例公司',primary_category='后端开发',status='included',validation_status='valid' WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(ctx, `UPDATE analysis_jobs SET status='succeeded' WHERE job_description_id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	filtered, err := repository.ListByTarget(ctx, userID, targetID, market.ListFilter{Statuses: []market.Status{market.StatusIncluded}, Query: "示例公司"})
	if err != nil || len(filtered) != 1 || filtered[0].ID != created.ID {
		t.Fatalf("unexpected filtered result: %#v err=%v", filtered, err)
	}

	edited := "负责 Go 服务端架构与接口开发，参与缓存设计、可观测性建设、自动化测试和故障排查。"
	updated, err := repository.UpdateRawText(ctx, userID, created.ID, edited, testJDHash(edited))
	if err != nil {
		t.Fatal(err)
	}
	if updated.RawText != edited || updated.JobStatus != "queued" || updated.Status != market.StatusProcessing || updated.ValidationStatus != "pending" {
		t.Fatalf("edited JD was not reset and queued: %#v", updated)
	}

	if err = repository.Delete(ctx, userID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.FindByID(ctx, userID, created.ID); !errors.Is(err, market.ErrNotFound) {
		t.Fatalf("deleted JD still exists: %v", err)
	}
}

func testJDHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
