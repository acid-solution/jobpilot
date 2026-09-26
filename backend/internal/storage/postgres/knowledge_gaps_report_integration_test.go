package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"

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
	defer db.Close()
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
		_, _ = db.Exec(`DELETE FROM job_targets WHERE id=$1`, targetID)
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
	stale, err := s.Get(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Stale || stale.Report == nil || len(stale.Report.Gaps) != 1 {
		t.Fatal("old report should remain visible and stale")
	}
	updated, err := s.Analyze(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Report.Gaps) != 0 || len(updated.Report.Met) != 2 {
		t.Fatalf("manual correction did not update report: %+v", updated.Report)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO jd_ability_level_jobs(user_id,target_id,job_description_id,status,attempts) VALUES($1,$2,$3,'failed',3)`, userID, targetID, firstJDID); err != nil {
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
}
func intPtr(v int) *int { return &v }
