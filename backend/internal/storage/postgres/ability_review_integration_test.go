package postgres

import (
	"context"
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
	levels := []abilityreview.Level{{0, "未学习"}, {1, "了解概念"}, {2, "能完成基础任务"}, {3, "能独立开发"}, {4, "能处理复杂场景"}, {5, "能设计体系"}}
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
