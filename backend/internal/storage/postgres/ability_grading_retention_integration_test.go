package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/target"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestJDGradingRetentionAcrossTargetAndReanalysisIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	userID, jdID, analysisID := uuid.New(), uuid.New(), uuid.New()
	targets := NewTargetRepository(db)
	setTarget := func(code string) uuid.UUID {
		t.Helper()
		var direction target.DirectionInput
		var specialtyID uuid.UUID
		if err := db.QueryRowContext(ctx, `SELECT category_id,id FROM job_specialties WHERE code=$1`, code).Scan(&direction.CategoryID, &specialtyID); err != nil {
			t.Fatal(err)
		}
		direction.SpecialtyID = &specialtyID
		value, err := targets.UpsertCurrent(ctx, userID, target.UpsertInput{EmploymentType: "internship", Directions: []target.DirectionInput{direction}})
		if err != nil {
			t.Fatal(err)
		}
		return value.ID
	}
	targetID := setTarget("backend-business")
	raw := "Go 后端实习生，负责服务端开发和中间件开发，要求独立使用 Go 完成服务端功能。"
	exec(`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status)
		VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'processing')`, jdID, userID, targetID, raw)
	exec(`INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status)
		VALUES($1,$2,$3,$4,'jd_analysis','queued')`, analysisID, userID, targetID, jdID)
	var goCode string
	if err := db.QueryRowContext(ctx, `SELECT code FROM abilities WHERE name='Go'`).Scan(&goCode); err != nil {
		t.Fatal(err)
	}
	parsed := jdanalysis.Result{
		DocumentType: jdanalysis.DocumentJobDescription, ValidationStatus: jdanalysis.ValidationValid,
		Title: "Go 后端实习生", EmploymentType: "internship", Responsibilities: []string{"负责服务端开发和中间件开发"},
		Classifications: []jdanalysis.JobClassification{
			{CategoryCode: "backend", SpecialtyCode: "backend-business", Relation: "primary", Evidence: "负责服务端开发和中间件开发", Reason: "主体为服务端业务开发"},
			{CategoryCode: "backend", SpecialtyCode: "backend-framework-middleware", Relation: "secondary", Evidence: "负责服务端开发和中间件开发", Reason: "兼有中间件开发职责"},
		},
		AbilityMentions: []jdanalysis.AbilityMention{{Name: "Go", CatalogCode: goCode, Evidence: "独立使用 Go 完成服务端功能"}},
		AbilityRequirements: []jdanalysis.AbilityRequirement{{
			Operator: "single", RequiredCount: 1, RequirementKind: "required", Evidence: "独立使用 Go 完成服务端功能",
			Options: []jdanalysis.AbilityRequirementOption{{RawLabel: "Go", AbilityName: "Go", CatalogCode: goCode, Evidence: "独立使用 Go 完成服务端功能"}},
		}}, PromptVersion: jdanalysis.PromptVersion,
	}
	analysis := NewAnalysisRepository(db)
	analyze := func() {
		t.Helper()
		exec(`UPDATE analysis_jobs SET status='queued',attempts=0,next_attempt_at=NOW(),preserve_previous_result=TRUE WHERE id=$1`, analysisID)
		job, err := analysis.Claim(ctx, 5*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := analysis.Complete(ctx, job, parsed); err != nil {
			t.Fatal(err)
		}
	}
	analyze()
	grading := NewAbilityGradingRepository(db)
	markets := NewMarketRepository(db)
	claim := func() abilitygrading.Input {
		t.Helper()
		job, err := grading.Claim(ctx, time.Minute*5)
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	result := func(job abilitygrading.Input, level int) abilitygrading.Result {
		return abilitygrading.Result{Assessments: []abilitygrading.Assessment{{
			OptionID: job.Abilities[0].Evidences[0].OptionID, AbilityCode: goCode, Level: level,
			Source: "explicit", RequirementKind: "required", EvidenceQuote: "独立使用 Go 完成服务端功能",
			Reason: "对照 Go 等级标准，原文要求独立完成开发。", Confidence: .9,
		}}, PromptVersion: abilitygrading.PromptVersion, Provider: "test", Model: "test"}
	}
	first := claim()
	if err := grading.Complete(ctx, first, result(first, 3)); err != nil {
		t.Fatal(err)
	}
	assertStored := func(jdStatus, jobStatus string, level, attempts int, stale, paused bool) {
		t.Helper()
		jd, err := markets.FindByID(ctx, userID, jdID)
		if err != nil {
			t.Fatal(err)
		}
		if string(jd.Status) != jdStatus || jd.AbilityGrading.Status != jobStatus || jd.AbilityGrading.Stale != stale || jd.AbilityGrading.Paused != paused || len(jd.AbilityLevels) != 1 || jd.AbilityLevels[0].Level != level {
			t.Fatalf("unexpected JD state: status=%s grading=%+v levels=%+v", jd.Status, jd.AbilityGrading, jd.AbilityLevels)
		}
		var actualAttempts int
		if err := db.QueryRowContext(ctx, `SELECT attempts FROM jd_ability_level_jobs WHERE job_description_id=$1`, jdID).Scan(&actualAttempts); err != nil || actualAttempts != attempts {
			t.Fatalf("attempts changed: %d want %d: %v", actualAttempts, attempts, err)
		}
	}
	assertNoClaim := func() {
		t.Helper()
		if _, err := grading.Claim(ctx, time.Minute*5); !errors.Is(err, abilitygrading.ErrNoJob) {
			t.Fatalf("unexpected extra grading call: %v", err)
		}
	}

	// Actual target edits retain grades for both reference and excluded JDs.
	setTarget("backend-framework-middleware")
	assertStored("reference", "succeeded", 3, 1, false, false)
	assertNoClaim()
	setTarget("frontend-web-miniapp")
	assertStored("excluded", "succeeded", 3, 1, false, false)
	analyze() // Recreates requirement IDs while the JD is excluded.
	assertStored("excluded", "succeeded", 3, 1, false, false)
	var restored int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jd_ability_option_levels WHERE job_description_id=$1 AND option_id<>$2`, jdID, first.Abilities[0].Evidences[0].OptionID).Scan(&restored); err != nil || restored != 1 {
		t.Fatalf("semantic grades were not restored to new option IDs: %d %v", restored, err)
	}
	setTarget("backend-business")
	assertStored("included", "succeeded", 3, 1, false, false)
	assertNoClaim() // Target-only changes and identical reparses do not cost tokens.

	// Changed grading input keeps the old success, but pauses while excluded.
	setTarget("frontend-web-miniapp")
	parsed.AbilityRequirements[0].Options[0].Qualifier = "独立开发"
	analyze()
	assertStored("excluded", "queued", 3, 0, true, true)
	assertNoClaim()
	assertStored("excluded", "queued", 3, 0, true, true) // No fake success.
	setTarget("backend-business")
	assertStored("included", "queued", 3, 0, true, false)
	profile, err := markets.ProfileByTarget(ctx, userID, targetID)
	if err != nil || profile.AbilityGradingPendingCount != 1 || len(profile.Abilities) != 1 || profile.Abilities[0].CoveredJDCount != 1 || profile.Abilities[0].LevelSummary.SampleCount != 0 {
		t.Fatalf("stale grades entered the market, or coverage disappeared: %+v %v", profile, err)
	}
	requirements, err := NewKnowledgeGapsRepository(db).LoadRequirements(ctx, userID, targetID)
	if err != nil || len(requirements) != 1 || requirements[0].Options[0].Graded {
		t.Fatalf("stale grades entered gap calculations: %+v %v", requirements, err)
	}

	// Target changes during execution don't invalidate a grade of unchanged data.
	second := claim()
	setTarget("frontend-web-miniapp")
	if err := grading.Complete(ctx, second, result(second, 4)); err != nil {
		t.Fatal(err)
	}
	assertStored("excluded", "succeeded", 4, 1, false, false)
	setTarget("backend-business")
	assertNoClaim()

	// Replacing the options invalidates the lease, even for identical semantics.
	parsed.AbilityRequirements[0].Options[0].Qualifier = "性能调优"
	analyze()
	for range 3 {
		old := claim()
		analyze()
		if err := grading.Complete(ctx, old, result(old, 5)); !errors.Is(err, abilitygrading.ErrLeaseLost) {
			t.Fatalf("worker holding deleted option IDs wrote a result: %v", err)
		}
	}
	current := claim()
	if current.Attempts != 1 {
		t.Fatalf("cancelled reanalysis exhausted the retry budget: %d", current.Attempts)
	}
	if err := grading.Fail(ctx, current, "model_timeout", false); err != nil {
		t.Fatal(err)
	}
	assertStored("included", "failed", 4, 3, true, false)
	// A target toggle must not silently reset a technical failure/retry budget.
	setTarget("frontend-web-miniapp")
	assertStored("excluded", "failed", 4, 3, true, true)
	setTarget("backend-business")
	assertStored("included", "failed", 4, 3, true, false)

	// Standards are part of the input too. A changed standard revives the task,
	// and another change during model execution blocks the outdated write.
	exec(`UPDATE ability_levels SET description=description||' 新标准' WHERE ability_id=(SELECT id FROM abilities WHERE name='Go') AND level=3`)
	standardJob := claim()
	if standardJob.Attempts != 1 {
		t.Fatalf("new standard did not reset the attempt budget: %d", standardJob.Attempts)
	}
	exec(`UPDATE ability_levels SET description=description||' 再次修订' WHERE ability_id=(SELECT id FROM abilities WHERE name='Go') AND level=3`)
	if err := grading.Complete(ctx, standardJob, result(standardJob, 5)); !errors.Is(err, abilitygrading.ErrLeaseLost) {
		t.Fatalf("outdated standard result was accepted: %v", err)
	}
	assertStored("included", "queued", 4, 0, true, false)
	standardJob = claim()
	if err := grading.Complete(ctx, standardJob, result(standardJob, 4)); err != nil {
		t.Fatal(err)
	}
	assertStored("included", "succeeded", 4, 1, false, false)

	// Edits preserve the previous result; deleted JDs still cascade normally.
	if _, err := markets.UpdateRawText(ctx, userID, jdID, raw+"需要优化数据库访问。", "edited-"+jdID.String()); err != nil {
		t.Fatal(err)
	}
	assertStored("processing", "queued", 4, 0, true, true)
	assertNoClaim()
}

func TestJDGradingMigrationRetainsLegacySuccessIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx := context.Background()
	if err := goose.DownTo(db, "sql", 32); err != nil {
		t.Fatal(err)
	}
	userID, targetID, jdID, reqID, optionID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO job_targets(id,user_id,title,employment_type) VALUES($1,$2,'后端','internship')`, targetID, userID)
	exec(`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status)
		VALUES($1,$2,$3,'熟悉 Go 开发后端服务',$4,'excluded','valid')`, jdID, userID, targetID, jdID.String())
	exec(`INSERT INTO job_description_ability_requirements(id,job_description_id,operator,required_count,evidence,sort_order)
		VALUES($1,$2,'single',1,'熟悉 Go 开发后端服务',1)`, reqID, jdID)
	exec(`INSERT INTO job_description_ability_requirement_options(id,requirement_id,ability_id,raw_label,evidence,sort_order)
		SELECT $1,$2,id,'Go','熟悉 Go 开发后端服务',1 FROM abilities WHERE name='Go'`, optionID, reqID)
	exec(`INSERT INTO jd_ability_level_jobs(user_id,target_id,job_description_id,status,last_error,prompt_version,attempts)
		VALUES($1,$2,$3,'succeeded','not_in_market',$4,1)`, userID, targetID, jdID, abilitygrading.PromptVersion)
	exec(`INSERT INTO jd_ability_option_levels(option_id,job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,confidence,prompt_version)
		SELECT $1,$2,id,3,'explicit','unspecified','熟悉 Go 开发后端服务','符合等级标准',.9,$3 FROM abilities WHERE name='Go'`, optionID, jdID, abilitygrading.PromptVersion)
	if err := goose.Up(db, "sql"); err != nil {
		t.Fatal(err)
	}
	var status string
	var current bool
	var snapshotCount, attempts int
	if err := db.QueryRowContext(ctx, `SELECT status,result_fingerprint=jd_ability_grading_fingerprint($1,$2),
		jsonb_array_length(result_snapshot),attempts FROM jd_ability_level_jobs WHERE job_description_id=$1`, jdID, abilitygrading.PromptVersion).Scan(&status, &current, &snapshotCount, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || !current || snapshotCount != 1 || attempts != 1 {
		t.Fatalf("legacy successful grading was discarded: %s %v %d %d", status, current, snapshotCount, attempts)
	}
}
