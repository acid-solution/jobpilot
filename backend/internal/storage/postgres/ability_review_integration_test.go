package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/google/uuid"
)

func TestAbilityReviewApproveNewAndRejectsStaleLeaseIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewAbilityReviewRepository(db)
	catalog, err := repo.loadAbilityCatalog(context.Background())
	if err != nil || len(catalog) == 0 {
		t.Fatalf("catalog: %v", err)
	}
	name := "ReviewTest" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	normalized := NormalizeAbilityName(name)
	requestID, token := uuid.New(), uuid.New()
	_, err = db.Exec(`INSERT INTO ability_review_requests(id,candidate_key,normalized_name,proposed_name,proposed_category_code,initiated_by_user_id,status,attempts,lease_token,heartbeat_at,lease_expires_at) VALUES($1,$2,$3,$4,$5,$6,'running',1,$7,NOW(),NOW()+INTERVAL '5 minutes')`, requestID, catalog[0].CategoryCode+":"+normalized, normalized, name, catalog[0].CategoryCode, uuid.New(), token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM ability_review_requests WHERE id=$1`, requestID)
		_, _ = db.Exec(`DELETE FROM abilities WHERE normalized_name=$1`, normalized)
	})
	attachReviewTestSource(t, db, requestID, name)
	levels := []abilityreview.Level{{Level: 0, Description: "未学习"}, {Level: 1, Description: "了解概念"}, {Level: 2, Description: "能完成基础任务"}, {Level: 3, Description: "能独立开发"}, {Level: 4, Description: "能处理复杂场景"}, {Level: 5, Description: "能设计体系"}}
	result := abilityreview.Result{Decision: "approve_new", Reason: "独立且可评估的框架", NewAbility: abilityreview.NewAbility{Name: name, CategoryCode: catalog[0].CategoryCode, Definition: "集成测试能力", Levels: levels}, Provider: "test", Model: "test", PromptVersion: abilityreview.PromptVersion}
	input := abilityreview.Input{ID: requestID, LeaseToken: uuid.New(), Catalog: catalog}
	if err := repo.Complete(context.Background(), input, result, uuid.Nil); err != abilityreview.ErrLeaseLost {
		t.Fatalf("stale lease should fail, got %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM abilities WHERE normalized_name=$1`, normalized).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale worker wrote ability: count=%d err=%v", count, err)
	}
	input.LeaseToken = token
	if err := repo.Complete(context.Background(), input, result, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM ability_levels level JOIN abilities ability ON ability.id=level.ability_id WHERE ability.normalized_name=$1`, normalized).Scan(&count); err != nil || count != 6 {
		t.Fatalf("levels=%d err=%v", count, err)
	}
}

func TestAbilityReviewClaimHasSingleEffectiveOwnerIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewAbilityReviewRepository(db)
	requestID := uuid.New()
	name := "ClaimTest" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	_, err = db.Exec(`INSERT INTO ability_review_requests(id,candidate_key,normalized_name,proposed_name,initiated_by_user_id,status,next_attempt_at) VALUES($1,$2,$3,$4,$5,'queued',NOW())`, requestID, "unknown:"+NormalizeAbilityName(name), NormalizeAbilityName(name), name, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM ability_review_requests WHERE id=$1`, requestID) })
	attachReviewTestSource(t, db, requestID, name)
	type claimResult struct {
		input abilityreview.Input
		err   error
	}
	results := make(chan claimResult, 2)
	for range 2 {
		go func() {
			input, claimErr := repo.Claim(context.Background(), 5*time.Minute)
			results <- claimResult{input, claimErr}
		}()
	}
	claimed, noJob := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			if result.input.ID != requestID {
				t.Fatalf("claimed unexpected request %s", result.input.ID)
			}
			claimed++
		} else if errors.Is(result.err, abilityreview.ErrNoRequest) {
			noJob++
		} else {
			t.Fatal(result.err)
		}
	}
	if claimed != 1 || noJob != 1 {
		t.Fatalf("claimed=%d no_job=%d", claimed, noJob)
	}
}

func attachReviewTestSource(t *testing.T, db *sql.DB, requestID uuid.UUID, name string) {
	t.Helper()
	var user uuid.UUID
	if err := db.QueryRow(`SELECT initiated_by_user_id FROM ability_review_requests WHERE id=$1`, requestID).Scan(&user); err != nil {
		t.Fatal(err)
	}
	target, jd, requirement := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM job_descriptions WHERE id=$1`, jd)
		_, _ = db.Exec(`DELETE FROM job_targets WHERE id=$1`, target)
	})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO job_targets(id,user_id,title,employment_type)VALUES($1,$2,'审核集成测试','internship')`, []any{target, user}},
		{`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status)VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'included','valid')`, []any{jd, user, target, name}},
		{`INSERT INTO job_description_ability_requirements(id,job_description_id,operator,required_count,evidence,sort_order)VALUES($1,$2,'single',1,$3,1)`, []any{requirement, jd, name}},
		{`INSERT INTO job_description_ability_requirement_options(requirement_id,raw_label,evidence,sort_order,review_request_id,resolution_status)VALUES($1,$2::text,$2::text,1,$3,'pending_review')`, []any{requirement, name, requestID}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
}
