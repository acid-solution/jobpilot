package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/google/uuid"
)

func TestAnalysisLeaseRecoveryAndStaleWriteProtection(t *testing.T) {
	databaseURL := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	userID, targetID, jdID, jobID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	invalidJDID, invalidJobID := uuid.New(), uuid.New()
	defer func() {
		_, _ = database.ExecContext(context.Background(), "DELETE FROM analysis_jobs WHERE id = $1", invalidJobID)
		_, _ = database.ExecContext(context.Background(), "DELETE FROM job_descriptions WHERE id = $1", invalidJDID)
		_, _ = database.ExecContext(context.Background(), "DELETE FROM analysis_jobs WHERE id = $1", jobID)
		_, _ = database.ExecContext(context.Background(), "DELETE FROM job_descriptions WHERE id = $1", jdID)
		_, _ = database.ExecContext(context.Background(), "DELETE FROM job_targets WHERE id = $1", targetID)
	}()
	if _, err := database.ExecContext(ctx, `
		INSERT INTO job_targets (id, user_id, title, employment_type, directions, catalog_status)
		VALUES ($1, $2, '后端开发', 'internship', '[]'::jsonb, 'valid')`, targetID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO target_directions (target_id, category_id, specialty_id, sort_order)
		VALUES ($1, '10000000-0000-0000-0000-000000000001', NULL, 1)`, targetID); err != nil {
		t.Fatal(err)
	}
	emptyProfile, err := NewMarketRepository(database).ProfileByTarget(ctx, userID, targetID)
	if err != nil {
		t.Fatal(err)
	}
	emptyJSON, err := json.Marshal(emptyProfile)
	if err != nil {
		t.Fatal(err)
	}
	if emptyProfile.Abilities == nil || string(emptyJSON) != `{"included_jd_count":0,"required_jd_count":10,"complete":false,"ability_grading_pending_count":0,"ability_grading_failed_count":0,"abilities":[]}` {
		t.Fatalf("empty market profile must expose an array, profile=%#v json=%s", emptyProfile, emptyJSON)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO job_descriptions (id, user_id, target_id, raw_text, raw_text_hash, status)
		VALUES ($1, $2, $3, 'Go 后端实习生，负责服务端开发，要求熟悉 Go 和 PostgreSQL。', encode(digest('Go 后端实习生，负责服务端开发，要求熟悉 Go 和 PostgreSQL。','sha256'),'hex'), 'processing')`, jdID, userID, targetID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO analysis_jobs (id, user_id, target_id, job_description_id, job_type, status)
		VALUES ($1, $2, $3, $4, 'jd_analysis', 'queued')`, jobID, userID, targetID, jdID); err != nil {
		t.Fatal(err)
	}

	repository := NewAnalysisRepository(database)
	catalog, err := repository.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	specialtyCodes := make(map[string]struct{})
	specialtyCount := 0
	for _, category := range catalog.JobCategories {
		for _, specialty := range category.Specialties {
			specialtyCount++
			specialtyCodes[specialty.Code] = struct{}{}
			if strings.TrimSpace(specialty.Definition) == "" || len(specialty.IncludeSignals) == 0 || len(specialty.ExcludeSignals) == 0 || len(specialty.ConfusedWith) == 0 {
				t.Fatalf("incomplete specialty semantics: %#v", specialty)
			}
		}
	}
	if specialtyCount < 28 {
		t.Fatalf("specialty count=%d want at least 28 seeded specialties", specialtyCount)
	}
	for _, category := range catalog.JobCategories {
		for _, specialty := range category.Specialties {
			for _, confused := range specialty.ConfusedWith {
				if _, exists := specialtyCodes[confused.SpecialtyCode]; !exists || strings.TrimSpace(confused.Distinction) == "" {
					t.Fatalf("invalid confused_with entry for %s: %#v", specialty.Code, confused)
				}
			}
		}
	}
	type claimResult struct {
		job jdanalysis.Job
		err error
	}
	claims := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			job, err := repository.Claim(ctx, 5*time.Minute)
			claims <- claimResult{job: job, err: err}
		}()
	}
	wait.Wait()
	close(claims)

	var first jdanalysis.Job
	claimed, empty := 0, 0
	for result := range claims {
		switch {
		case result.err == nil:
			first = result.job
			claimed++
		case errors.Is(result.err, jdanalysis.ErrNoJob):
			empty++
		default:
			t.Fatal(result.err)
		}
	}
	if claimed != 1 || empty != 1 || first.LeaseToken == uuid.Nil {
		t.Fatalf("expected one valid claimant, claimed=%d empty=%d job=%#v", claimed, empty, first)
	}
	if err := repository.Heartbeat(ctx, first, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if recovered, err := repository.RecoverExpired(ctx); err != nil || recovered != 0 {
		t.Fatalf("healthy heartbeat was recovered: recovered=%d err=%v", recovered, err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE analysis_jobs SET lease_expires_at = NOW() - INTERVAL '1 second' WHERE id = $1`, jobID); err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.RecoverExpired(ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("RecoverExpired recovered=%d err=%v", recovered, err)
	}
	var recoveredStatus, recoveredError string
	var leaseCleared, heartbeatCleared, expiryCleared bool
	if err := database.QueryRowContext(ctx, `
		SELECT status, last_error, lease_token IS NULL, heartbeat_at IS NULL, lease_expires_at IS NULL
		FROM analysis_jobs WHERE id = $1`, jobID).Scan(
		&recoveredStatus, &recoveredError, &leaseCleared, &heartbeatCleared, &expiryCleared,
	); err != nil {
		t.Fatal(err)
	}
	if recoveredStatus != "failed" || recoveredError != "worker_interrupted" ||
		!leaseCleared || !heartbeatCleared || !expiryCleared {
		t.Fatalf("unexpected recovered state: status=%s error=%s cleared=%v/%v/%v",
			recoveredStatus, recoveredError, leaseCleared, heartbeatCleared, expiryCleared)
	}

	second, err := repository.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.LeaseToken == first.LeaseToken || second.Attempts != 2 {
		t.Fatalf("task was not re-leased: first=%#v second=%#v", first, second)
	}
	var goAbilityCode string
	if err := database.QueryRowContext(ctx, `SELECT code FROM abilities WHERE name = 'Go'`).Scan(&goAbilityCode); err != nil {
		t.Fatal(err)
	}
	validResult := jdanalysis.Result{
		DocumentType: jdanalysis.DocumentJobDescription, ValidationStatus: jdanalysis.ValidationValid,
		Title: "Go 后端实习生", EmploymentType: "internship", Responsibilities: []string{"负责服务端开发"},
		Classifications: []jdanalysis.JobClassification{{
			CategoryCode: "backend", SpecialtyCode: "backend-business", Relation: "primary", Evidence: "负责服务端开发",
			Reason: "职责面向具体服务端业务实现。",
		}},
		AbilityMentions: []jdanalysis.AbilityMention{{Name: "Go", CatalogCode: goAbilityCode, Evidence: "熟悉 Go", RequiredLevel: intPointer(3)}},
		AbilityRequirements: []jdanalysis.AbilityRequirement{{
			Operator: jdanalysis.RequirementSingle, RequiredCount: 1, Evidence: "熟悉 Go",
			Options: []jdanalysis.AbilityRequirementOption{{
				RawLabel: "Go", AbilityName: "Go", CatalogCode: goAbilityCode,
				Evidence: "熟悉 Go", RequiredLevel: intPointer(3),
			}},
		}},
	}
	if err := repository.Complete(ctx, first, validResult); !errors.Is(err, jdanalysis.ErrLeaseLost) {
		t.Fatalf("stale claimant should lose write access, got %v", err)
	}
	if err := repository.Complete(ctx, second, validResult); err != nil {
		t.Fatal(err)
	}
	var jobStatus, validationStatus, jdStatus string
	if err := database.QueryRowContext(ctx, `
		SELECT job.status, jd.validation_status, jd.status
		FROM analysis_jobs job JOIN job_descriptions jd ON jd.id = job.job_description_id
		WHERE job.id = $1`, jobID).Scan(&jobStatus, &validationStatus, &jdStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "succeeded" || validationStatus != "valid" || jdStatus != "included" {
		t.Fatalf("unexpected final state: job=%s validation=%s jd=%s", jobStatus, validationStatus, jdStatus)
	}
	profile, err := NewMarketRepository(database).ProfileByTarget(ctx, userID, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.IncludedJDCount != 1 || len(profile.Abilities) != 1 || profile.Abilities[0].Name != "Go" || profile.Abilities[0].CoveredJDCount != 1 {
		t.Fatalf("unexpected market profile: %#v", profile)
	}

	if _, err := database.ExecContext(ctx, `
		UPDATE analysis_jobs
		SET status = 'queued', attempts = 0, next_attempt_at = NOW(), preserve_previous_result = TRUE
		WHERE id = $1`, jobID); err != nil {
		t.Fatal(err)
	}
	reanalysis, err := repository.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !reanalysis.PreservePreviousResult {
		t.Fatal("historical reanalysis did not preserve previous result")
	}
	if err := repository.Fail(ctx, reanalysis, "model_invalid_response", false); err != nil {
		t.Fatal(err)
	}
	var preservedStatus string
	var preservedClassifications int
	if err := database.QueryRowContext(ctx, `
		SELECT jd.status, COUNT(classification.id)
		FROM job_descriptions jd
		LEFT JOIN job_description_classifications classification ON classification.job_description_id = jd.id
		WHERE jd.id = $1
		GROUP BY jd.status`, jdID).Scan(&preservedStatus, &preservedClassifications); err != nil {
		t.Fatal(err)
	}
	if preservedStatus != "included" || preservedClassifications != 1 {
		t.Fatalf("failed reanalysis replaced old result: status=%s classifications=%d", preservedStatus, preservedClassifications)
	}
	retried, err := NewMarketRepository(database).RetryAnalysis(ctx, userID, jdID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != "included" || retried.ValidationStatus != "valid" {
		t.Fatalf("retry discarded preserved result: %#v", retried)
	}
	reanalysis, err = repository.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	validResult.Classifications[0].Reason = "新版目录确认该职责属于具体业务服务实现。"
	validResult.PromptVersion = jdanalysis.PromptVersion
	if err := repository.Complete(ctx, reanalysis, validResult); err != nil {
		t.Fatal(err)
	}
	var savedReason, promptVersion string
	var preservePrevious bool
	if err := database.QueryRowContext(ctx, `
		SELECT classification.reason, jd.analysis_prompt_version, job.preserve_previous_result
		FROM job_description_classifications classification
		JOIN job_descriptions jd ON jd.id = classification.job_description_id
		JOIN analysis_jobs job ON job.job_description_id = jd.id
		WHERE jd.id = $1`, jdID).Scan(&savedReason, &promptVersion, &preservePrevious); err != nil {
		t.Fatal(err)
	}
	if savedReason != validResult.Classifications[0].Reason || promptVersion != jdanalysis.PromptVersion || preservePrevious {
		t.Fatalf("new result was not atomically installed: reason=%q prompt=%q preserve=%v", savedReason, promptVersion, preservePrevious)
	}

	if _, err := database.ExecContext(ctx, `
		INSERT INTO job_descriptions (id, user_id, target_id, raw_text, raw_text_hash, status)
		VALUES ($1, $2, $3, '本人熟悉 Go，具有三个项目开发经历。', encode(digest('本人熟悉 Go，具有三个项目开发经历。','sha256'),'hex'), 'processing')`, invalidJDID, userID, targetID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO analysis_jobs (id, user_id, target_id, job_description_id, job_type, status)
		VALUES ($1, $2, $3, $4, 'jd_analysis', 'queued')`, invalidJobID, userID, targetID, invalidJDID); err != nil {
		t.Fatal(err)
	}
	invalidJob, err := repository.Claim(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, invalidJob, jdanalysis.Result{
		DocumentType: jdanalysis.DocumentNonJobDescription, ValidationStatus: jdanalysis.ValidationInvalid,
		ValidationReason: "该文本更像求职者简历，不是岗位招聘说明",
	}); err != nil {
		t.Fatal(err)
	}
	var invalidJobStatus, invalidJDStatus, invalidValidation, invalidReason string
	if err := database.QueryRowContext(ctx, `
		SELECT job.status, jd.status, jd.validation_status, jd.validation_reason
		FROM analysis_jobs job JOIN job_descriptions jd ON jd.id = job.job_description_id
		WHERE job.id = $1`, invalidJobID).Scan(
		&invalidJobStatus, &invalidJDStatus, &invalidValidation, &invalidReason,
	); err != nil {
		t.Fatal(err)
	}
	if invalidJobStatus != "succeeded" || invalidJDStatus != "excluded" ||
		invalidValidation != "invalid" || invalidReason == "" {
		t.Fatalf("invalid JD was treated as technical failure: job=%s jd=%s validation=%s reason=%q",
			invalidJobStatus, invalidJDStatus, invalidValidation, invalidReason)
	}
}

func TestResolveReviewedDynamicJobSpecialtyInOneTransaction(t *testing.T) {
	databaseURL := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	name := "集成测试动态小类-" + uuid.NewString()
	values := []jdanalysis.JobClassification{{
		CategoryCode: "backend", Relation: "primary", Evidence: "负责搜索召回链路", Reason: "审核确认该职责需要独立小类。",
		Candidate: &jdanalysis.JobClassificationCandidate{
			Scope: "specialty", SpecialtyName: name, Definition: "研发搜索召回和在线排序服务。",
			IncludeSignals: []string{"建设搜索召回链路"}, ExcludeSignals: []string{"仅调用搜索 API"},
			ConfusedWith: []jdanalysis.SpecialtyConfusion{{SpecialtyCode: "backend-business", Distinction: "是否以召回排序系统为主要交付物。"}},
			Reason:       "现有后端小类无法准确表达搜索推荐系统职责。",
		},
	}}
	resolved, err := resolveJobClassificationCandidates(ctx, tx, values, &jdanalysis.ClassificationReviewResult{Decision: "accept", Reason: "审核通过"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Candidate != nil || resolved[0].SpecialtyCode == "" {
		t.Fatalf("candidate was not resolved: %#v", resolved)
	}
	var source, savedName string
	if err := tx.QueryRowContext(ctx, `SELECT source,name FROM job_specialties WHERE code=$1`, resolved[0].SpecialtyCode).Scan(&source, &savedName); err != nil {
		t.Fatal(err)
	}
	if source != "dynamic_review" || savedName != name {
		t.Fatalf("unexpected dynamic specialty: source=%s name=%s", source, savedName)
	}

	categoryName := "集成测试动态大类-" + uuid.NewString()
	newCategory, err := resolveJobClassificationCandidates(ctx, tx, []jdanalysis.JobClassification{{
		Relation: "primary", Evidence: "负责量子设备控制软件", Reason: "职责不属于现有岗位大类。",
		Candidate: &jdanalysis.JobClassificationCandidate{
			Scope: "category", CategoryName: categoryName, CategoryDefinition: "面向量子设备的软件研发岗位。",
			SpecialtyName: "量子设备控制", Definition: "研发量子设备控制与校准软件。",
			IncludeSignals: []string{"开发量子设备控制系统"}, ExcludeSignals: []string{"仅使用量子计算 SDK"},
			ConfusedWith: []jdanalysis.SpecialtyConfusion{{SpecialtyCode: "embedded-system", Distinction: "目标是量子设备控制体系，而非通用嵌入式系统。"}},
			Reason:       "现有大类无法表达该稳定研发方向。",
		},
	}}, &jdanalysis.ClassificationReviewResult{Decision: "correct", Reason: "审核通过"})
	if err != nil {
		t.Fatal(err)
	}
	var categorySource, categorySavedName, specialtySource string
	if err := tx.QueryRowContext(ctx, `
		SELECT category.source,category.name,specialty.source
		FROM job_categories category JOIN job_specialties specialty ON specialty.category_id=category.id
		WHERE category.code=$1 AND specialty.code=$2`, newCategory[0].CategoryCode, newCategory[0].SpecialtyCode,
	).Scan(&categorySource, &categorySavedName, &specialtySource); err != nil {
		t.Fatal(err)
	}
	if categorySource != "dynamic_review" || specialtySource != "dynamic_review" || categorySavedName != categoryName {
		t.Fatalf("unexpected dynamic category: category=%s specialty=%s name=%s", categorySource, specialtySource, categorySavedName)
	}
}

func TestMarketProfileCountsDistinctSatisfiableJDs(t *testing.T) {
	databaseURL := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("JOBPILOT_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	userID, targetID := uuid.New(), uuid.New()
	jd1, jd2 := uuid.New(), uuid.New()
	defer func() {
		_, _ = database.ExecContext(context.Background(), "DELETE FROM job_descriptions WHERE target_id = $1", targetID)
		_, _ = database.ExecContext(context.Background(), "DELETE FROM job_targets WHERE id = $1", targetID)
	}()
	if _, err := database.ExecContext(ctx, `
		INSERT INTO job_targets (id, user_id, title, employment_type, directions, catalog_status)
		VALUES ($1, $2, '后端开发', 'internship', '[]'::jsonb, 'valid')`, targetID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO job_descriptions (id, user_id, target_id, raw_text, raw_text_hash, title, status, validation_status)
		VALUES
		  ($1, $3, $4, 'Go、Python、C++、Java 任意一门', encode(digest('Go、Python、C++、Java 任意一门','sha256'),'hex'), 'JD1', 'included', 'valid'),
		  ($2, $3, $4, 'Rust、C、C++ 任意一门', encode(digest('Rust、C、C++ 任意一门','sha256'),'hex'), 'JD2', 'included', 'valid')`, jd1, jd2, userID, targetID); err != nil {
		t.Fatal(err)
	}

	insertRequirement := func(jdID uuid.UUID, operator string, requiredCount int, evidence string, abilityNames ...string) {
		t.Helper()
		var requirementID uuid.UUID
		if err := database.QueryRowContext(ctx, `
			INSERT INTO job_description_ability_requirements (
				job_description_id, operator, required_count, evidence, sort_order
			) VALUES ($1, $2, $3, $4,
				(SELECT COALESCE(MAX(sort_order), 0) + 1 FROM job_description_ability_requirements WHERE job_description_id = $1))
			RETURNING id`, jdID, operator, requiredCount, evidence).Scan(&requirementID); err != nil {
			t.Fatal(err)
		}
		for index, abilityName := range abilityNames {
			if _, err := database.ExecContext(ctx, `
				INSERT INTO job_description_ability_requirement_options (
					requirement_id, ability_id, raw_label, evidence, sort_order
				) SELECT $1, id, $2::text, $2::text, $3 FROM abilities WHERE name = $2::text`, requirementID, abilityName, index+1); err != nil {
				t.Fatal(err)
			}
		}
	}
	insertRequirement(jd1, "any_of", 1, "Go、Python、C++、Java 任意一门", "Go", "Python", "C++", "Java")
	insertRequirement(jd1, "single", 1, "C++ 也可用于性能模块", "C++")
	insertRequirement(jd2, "any_of", 1, "Rust、C、C++ 任意一门", "Rust", "C", "C++")
	insertRequirement(jd2, "at_least_n", 2, "MySQL、Redis 至少掌握两项", "MySQL", "Redis")

	profile, err := NewMarketRepository(database).ProfileByTarget(ctx, userID, targetID)
	if err != nil {
		t.Fatal(err)
	}
	coverage := make(map[string]int, len(profile.Abilities))
	for _, ability := range profile.Abilities {
		coverage[ability.Name] = ability.CoveredJDCount
	}
	if profile.IncludedJDCount != 2 || coverage["C++"] != 2 || coverage["Go"] != 1 ||
		coverage["Python"] != 1 || coverage["Java"] != 1 || coverage["Rust"] != 1 || coverage["C"] != 1 {
		t.Fatalf("unexpected satisfiable JD coverage: profile=%#v coverage=%#v", profile, coverage)
	}
	if mysqlCoverage, exists := coverage["MySQL"]; !exists || mysqlCoverage != 0 {
		t.Fatalf("at_least_n option should remain visible but not count as independently satisfying: %#v", coverage)
	}
	if redisCoverage, exists := coverage["Redis"]; !exists || redisCoverage != 0 {
		t.Fatalf("at_least_n option should remain visible but not count as independently satisfying: %#v", coverage)
	}
}

func intPointer(value int) *int { return &value }
