package postgres

import (
	"testing"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/google/uuid"
)

func TestProfileAndPlanningShareReadinessRulesIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := t.Context()
	projects, _, profiles, user, goID := recommendationRaceFixture(t, db, ctx)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(wantComplete bool, wantCode string) {
		t.Helper()
		overview, err := profiles.GetOverview(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		ss, err := projects.Snapshot(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if overview.Complete != wantComplete || ss.Readiness.ProfileComplete != wantComplete || ss.Readiness.Code != wantCode || overview.BlockingCount != ss.Readiness.MissingAbilityCount {
			t.Fatalf("profile=%+v, planning=%+v; want complete=%v code=%s", overview, ss.Readiness, wantComplete, wantCode)
		}
	}
	setLevel := func(id uuid.UUID, value int) {
		t.Helper()
		if _, err := profiles.SetCapabilityLevel(ctx, user, id, value); err != nil {
			t.Fatal(err)
		}
	}
	check(true, "ready")
	setLevel(goID, 0)
	check(true, "ready") // Explicit L0 is assessed, even though it reveals a gap.

	var requirement, jd, pythonID uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT requirement.id,jd.id FROM job_description_ability_requirements requirement JOIN job_descriptions jd ON jd.id=requirement.job_description_id WHERE jd.user_id=$1 LIMIT 1`, user).Scan(&requirement, &jd); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM abilities WHERE name='Python' AND is_active LIMIT 1`).Scan(&pythonID); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE job_description_ability_requirements SET operator='any_of' WHERE id=$1`, requirement)
	exec(`WITH option AS (
	 INSERT INTO job_description_ability_requirement_options(requirement_id,ability_id,raw_label,evidence,sort_order)
	 VALUES($1,$2,'Python','Go 或 Python',2) RETURNING id)
	 INSERT INTO jd_ability_option_levels(option_id,job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version)
	 SELECT id,$3,$2,3,'explicit','required','Go 或 Python','任选一门',.9,'test' FROM option`, requirement, pythonID, jd)
	refreshGrade := func() {
		exec(`UPDATE jd_ability_level_jobs SET result_fingerprint=jd_ability_grading_fingerprint($1,$2),input_fingerprint=jd_ability_grading_fingerprint($1,$2),result_snapshot=jd_ability_grading_snapshot($1) WHERE job_description_id=$1`, jd, abilitygrading.PromptVersion)
	}
	refreshGrade()
	check(false, "profile_incomplete") // Go L0 cannot decide the unknown Python path.
	setLevel(goID, 3)
	check(true, "ready") // A satisfied path leaves the alternative unassessed.
	exec(`UPDATE job_description_ability_requirements SET operator='at_least_n',required_count=2 WHERE id=$1`, requirement)
	refreshGrade()
	check(false, "profile_incomplete")
	setLevel(pythonID, 0)
	check(true, "ready") // Both paths are assessed; a gap need not block analysis.

	if _, err := profiles.SaveSettings(ctx, user, profile.Settings{WeeklyHours: intPtr(12), ExpectedWeeks: intPtr(8)}); err != nil {
		t.Fatal(err)
	}
	check(false, "profile_incomplete")
	if _, err := profiles.SaveSettings(ctx, user, profile.Settings{WeeklyHours: intPtr(12), ExpectedWeeks: intPtr(8), ExistingExperience: "测试经历"}); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE job_descriptions SET validation_status='invalid' WHERE id=$1`, jd)
	check(false, "market_incomplete") // Only nine valid, included JDs remain.
}
