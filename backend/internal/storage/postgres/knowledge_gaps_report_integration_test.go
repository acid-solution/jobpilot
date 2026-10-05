package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/LeoninCS/jobpilot-next/backend/internal/knowledgegaps"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
)

func TestKnowledgeGapReportTenJDsAndStalenessIntegration(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	userID, targetID := uuid.New(), uuid.New()
	var goID, pythonID uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT id FROM abilities WHERE name='Go' AND is_active LIMIT 1`).Scan(&goID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM abilities WHERE name='Python' AND is_active LIMIT 1`).Scan(&pythonID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM user_capability_level_events WHERE user_id=$1`, userID)
		_, _ = db.Exec(`DELETE FROM user_capability_profiles WHERE user_id=$1`, userID)
		_, _ = db.Exec(`DELETE FROM user_profile_settings WHERE user_id=$1`, userID)
		if _, err := db.Exec(`DELETE FROM job_descriptions WHERE target_id=$1`, targetID); err != nil {
			t.Error(err)
		}
		if _, err := db.Exec(`DELETE FROM job_targets WHERE id=$1`, targetID); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.ExecContext(ctx, `INSERT INTO job_targets(id,user_id,title,employment_type,directions,catalog_status) VALUES($1,$2,'后端开发','internship','[]'::jsonb,'valid')`, targetID, userID); err != nil {
		t.Fatal(err)
	}
	var firstJDID uuid.UUID
	for i := 0; i < 10; i++ {
		jdID := uuid.New()
		if i == 0 {
			firstJDID = jdID
		}
		raw := fmt.Sprintf("Go 后端岗位 %d：要求独立使用 Go 开发服务。熟悉 Go 或 Python 任一语言。", i)
		if _, err := db.ExecContext(ctx, `INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status,title,responsibilities) VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'included','valid','Go 后端岗位','["负责服务开发"]'::jsonb)`, jdID, userID, targetID, raw); err != nil {
			t.Fatal(err)
		}
		insertOption := func(operator string, count int, kind, evidence string, order int, abilityID uuid.UUID, abilityName string, level int) {
			var reqID, optionID uuid.UUID
			if err := db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,requirement_kind,evidence,sort_order) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, jdID, operator, count, kind, evidence, order).Scan(&reqID); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirement_options(requirement_id,ability_id,raw_label,evidence,sort_order) VALUES($1,$2,$3,$4,1) RETURNING id`, reqID, abilityID, abilityName, evidence).Scan(&optionID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO jd_ability_option_levels(option_id,job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version) VALUES($1,$2,$3,$4,'explicit',$5,$6,'原文要求独立完成常见服务开发',.9,'test')`, optionID, jdID, abilityID, level, kind, evidence); err != nil {
				t.Fatal(err)
			}
		}
		insertOption("single", 1, "required", "要求独立使用 Go 开发服务", 1, goID, "Go", 3)
		if _, err := db.ExecContext(ctx, `INSERT INTO jd_ability_level_assessments(job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version) VALUES($1,$2,3,'explicit','required','要求独立使用 Go 开发服务','岗位要求独立开发',.9,'test')`, jdID, goID); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			var reqID uuid.UUID
			quote := "熟悉 Go 或 Python 任一语言"
			if err := db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirements(job_description_id,operator,required_count,requirement_kind,evidence,sort_order) VALUES($1,'any_of',1,'required',$2,2) RETURNING id`, jdID, quote).Scan(&reqID); err != nil {
				t.Fatal(err)
			}
			for order, item := range []struct {
				id   uuid.UUID
				name string
			}{{goID, "Go"}, {pythonID, "Python"}} {
				var optionID uuid.UUID
				if err := db.QueryRowContext(ctx, `INSERT INTO job_description_ability_requirement_options(requirement_id,ability_id,raw_label,evidence,sort_order) VALUES($1,$2,$3,$4,$5) RETURNING id`, reqID, item.id, item.name, quote, order+1).Scan(&optionID); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO jd_ability_option_levels(option_id,job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version) VALUES($1,$2,$3,3,'explicit','required',$4,'任选一种语言即可',.9,'test')`, optionID, jdID, item.id, quote); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO jd_ability_level_assessments(job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version) VALUES($1,$2,3,'explicit','required',$3,'任选一种语言即可',.9,'test')`, jdID, pythonID, quote); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO jd_ability_level_jobs(user_id,target_id,job_description_id,status,
			input_fingerprint,result_fingerprint,result_snapshot,prompt_version)
			VALUES($1,$2,$3,'succeeded',jd_ability_grading_fingerprint($3,$4),jd_ability_grading_fingerprint($3,$4),
			jd_ability_grading_snapshot($3),$4)`, userID, targetID, jdID, abilitygrading.PromptVersion); err != nil {
			t.Fatal(err)
		}
	}
	profiles := profile.NewService(NewProfileRepository(db), nil)
	if _, err := profiles.SaveSettings(ctx, userID, profile.Settings{WeeklyHours: intPtr(12), ExpectedWeeks: intPtr(8), ExistingExperience: "暂无"}); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.SetCapabilityLevel(ctx, userID, goID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.SetCapabilityLevel(ctx, userID, pythonID, 3); err != nil {
		t.Fatal(err)
	}
	targets := target.NewService(NewTargetRepository(db))
	markets := market.NewService(NewMarketRepository(db, AbilityReviewQuotaLimits{}), targets)
	s := knowledgegaps.NewService(NewKnowledgeGapsRepository(db), targets, markets, profiles)
	first, err := s.Analyze(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Report == nil || len(first.Report.Gaps) != 1 || len(first.Report.Met) != 1 {
		t.Fatalf("wrong report counts gaps=%d met=%d", len(first.Report.Gaps), len(first.Report.Met))
	}
	if first.Report.Gaps[0].SampleCount != 10 || first.Report.Gaps[0].TargetLevel != 3 {
		t.Fatalf("Go modal grade or JD dedup wrong: %+v", first.Report.Gaps[0])
	}
	if _, err := profiles.SetCapabilityLevel(ctx, userID, goID, 3); err != nil {
		t.Fatal(err)
	}
	updated, err := s.Get(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Stale || updated.Report == nil || len(updated.Report.Gaps) != 0 || len(updated.Report.Met) != 2 {
		t.Fatalf("manual correction did not update report: %+v", updated.Report)
	}

	// Update market grades through the same completion transaction used by the
	// Worker, without running basic JD parsing or invoking a paid model.
	if _, err := db.ExecContext(ctx, `UPDATE jd_ability_level_jobs SET status='queued',attempts=0,next_attempt_at=NOW() WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	grading := NewAbilityGradingRepository(db)
	for range 10 {
		input, err := grading.Claim(ctx, 5*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		result := abilitygrading.Result{Provider: "test", Model: "test", PromptVersion: abilitygrading.PromptVersion}
		for _, ability := range input.Abilities {
			for _, evidence := range ability.Evidences {
				level := 3
				if evidence.Quote == "要求独立使用 Go 开发服务" {
					level = 4
				}
				result.Assessments = append(result.Assessments, abilitygrading.Assessment{OptionID: evidence.OptionID, AbilityCode: ability.Code,
					Level: level, Source: "explicit", RequirementKind: evidence.RequirementKind, EvidenceQuote: evidence.Quote, Reason: "对照等级标准重新判断", Confidence: .9})
			}
		}
		if err := grading.Complete(ctx, input, result); err != nil {
			t.Fatal(err)
		}
	}
	// Concurrent reads must all observe the same published report, rather than
	// overwrite it repeatedly from overlapping snapshots.
	type readResult struct {
		view knowledgegaps.View
		err  error
	}
	reads := make(chan readResult, 4)
	for range 4 {
		go func() { view, err := s.Get(ctx, userID); reads <- readResult{view, err} }()
	}
	var generated time.Time
	for range 4 {
		value := <-reads
		if value.err != nil || value.view.Stale || value.view.Report == nil || len(value.view.Report.Gaps) != 1 || value.view.Report.Gaps[0].TargetLevel != 4 {
			t.Fatalf("market grade was not automatically refreshed: %+v %v", value.view, value.err)
		}
		if generated.IsZero() {
			generated = value.view.Report.GeneratedAt
		} else if !generated.Equal(value.view.Report.GeneratedAt) {
			t.Fatalf("concurrent readers republished identical input: first=%s next=%s fingerprint=%s", generated, value.view.Report.GeneratedAt, value.view.SourceFingerprint)
		}
	}
	// A material/reassessment update changes the persisted current level directly;
	// refresh must not depend on the manual-level endpoint being called.
	if _, err := db.ExecContext(ctx, `UPDATE user_capability_profiles SET current_level=4,level_source='material',updated_at=NOW() WHERE user_id=$1 AND ability_id=$2`, userID, goID); err != nil {
		t.Fatal(err)
	}
	updated, err = s.Get(ctx, userID)
	if err != nil || updated.Stale || len(updated.Report.Gaps) != 0 || len(updated.Report.Met) != 2 {
		t.Fatalf("material grade not refreshed: %+v %v", updated, err)
	}
	// Saving must be part of the transaction; a real database rejection leaves
	// the previous report readable, even inside an Agent action transaction.
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_gap_refresh_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.user_id='`+userID.String()+`'::uuid THEN RAISE EXCEPTION 'test report save failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_gap_refresh_test BEFORE UPDATE ON knowledge_gap_reports FOR EACH ROW EXECUTE FUNCTION fail_gap_refresh_test()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS fail_gap_refresh_test ON knowledge_gap_reports`)
		_, _ = db.Exec(`DROP FUNCTION IF EXISTS fail_gap_refresh_test()`)
	})
	if _, err := profiles.SetCapabilityLevel(ctx, userID, goID, 3); err != nil {
		t.Fatal(err)
	}
	if err := NewAgentRepository(db).WithinUserTransaction(ctx, userID, func(txCtx context.Context) error {
		preserved, err := s.Get(txCtx, userID)
		if err != nil {
			return err
		}
		if !preserved.Stale || preserved.RefreshError == "" || len(preserved.Report.Gaps) != 0 {
			return errors.New("failed refresh did not return previous report")
		}
		var usable int
		return NewKnowledgeGapsRepository(db).database.QueryRowContext(txCtx, `SELECT 1`).Scan(&usable)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_gap_refresh_test ON knowledge_gap_reports`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP FUNCTION fail_gap_refresh_test()`); err != nil {
		t.Fatal(err)
	}
	updated, err = s.Get(ctx, userID)
	if err != nil || updated.Stale || len(updated.Report.Gaps) != 1 {
		t.Fatalf("retry did not recover: %+v %v", updated, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE jd_ability_level_jobs SET status='failed',attempts=3 WHERE job_description_id=$1`, firstJDID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewMarketRepository(db).RetryAbilityGrading(ctx, userID, firstJDID); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT status,attempts FROM jd_ability_level_jobs WHERE job_description_id=$1`, firstJDID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || attempts != 0 {
		t.Fatalf("grading retry was not queued cleanly: %s, %d attempts", status, attempts)
	}
	var retained int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jd_ability_option_levels WHERE job_description_id=$1`, firstJDID).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != 3 {
		t.Fatalf("grading retry should retain old grades until success: %d", retained)
	}
	waiting, err := s.Get(ctx, userID)
	if err != nil || !waiting.Stale || waiting.Readiness.Code != "grading_pending" || !waiting.Report.GeneratedAt.Equal(updated.Report.GeneratedAt) {
		t.Fatalf("incomplete grading did not preserve report: %+v %v", waiting, err)
	}
}
func intPtr(v int) *int { return &v }
