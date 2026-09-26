package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/google/uuid"
)

func TestAbilityAliasReviewLifecycleIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	repo := NewAbilityReviewRepository(db)
	var goID, javaID uuid.UUID
	var goCode, javaCode string
	if err := db.QueryRowContext(ctx, `SELECT id,code FROM abilities WHERE name='Go'`).Scan(&goID, &goCode); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id,code FROM abilities WHERE name='Java'`).Scan(&javaID, &javaCode); err != nil {
		t.Fatal(err)
	}
	type sourceIDs struct{ user, jd, option uuid.UUID }
	seed := func(label, quote string, abilityID uuid.UUID) sourceIDs {
		t.Helper()
		s := sourceIDs{uuid.New(), uuid.New(), uuid.New()}
		target, requirement := uuid.New(), uuid.New()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		exec := func(query string, args ...any) {
			t.Helper()
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
		}
		exec(`INSERT INTO job_targets(id,user_id,title,employment_type) VALUES($1,$2,'后端','internship')`, target, s.user)
		exec(`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status,validation_status) VALUES($1,$2,$3,$4,$5,'included','valid')`, s.jd, s.user, target, quote, s.jd.String())
		exec(`INSERT INTO job_description_ability_requirements(id,job_description_id,operator,required_count,evidence,sort_order) VALUES($1,$2,'single',1,$3,1)`, requirement, s.jd, quote)
		exec(`INSERT INTO job_description_ability_requirement_options(id,requirement_id,ability_id,raw_label,evidence,sort_order,normalization_reason) VALUES($1,$2,$3,$4,$5,1,'本语境下对应该能力')`, s.option, requirement, abilityID, label, quote)
		if err = enqueueAliasReview(ctx, tx, s.user, abilityID, aliasReviewSource{JDID: s.jd, Label: label, Evidence: quote, Reason: "本语境下对应该能力"}); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return s
	}
	claim := func() abilityreview.Input {
		t.Helper()
		input, err := repo.Claim(ctx, time.Minute*5)
		if err != nil {
			t.Fatal(err)
		}
		if input.ReviewType != "alias" {
			t.Fatalf("unexpected task: %+v", input)
		}
		return input
	}
	complete := func(input abilityreview.Input, decision string, usage uuid.UUID) {
		t.Helper()
		if err := repo.Complete(ctx, input, abilityreview.Result{Decision: decision, Reason: "独立审核结论", ExistingAbilityCode: input.TargetAbilityCode, ContextIndependent: decision == "approve_alias", PromptVersion: abilityreview.AliasPromptVersion, Model: "mock", Provider: "test", ProviderRequestID: "mock-response", InputTokens: 10, OutputTokens: 5}, usage); err != nil {
			t.Fatal(err)
		}
	}
	assertMapped := func(s sourceIDs, want uuid.UUID) {
		t.Helper()
		var id uuid.UUID
		var status, resolution string
		if err := db.QueryRowContext(ctx, `SELECT option.ability_id,option.resolution_status,jd.status FROM job_description_ability_requirement_options option JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id JOIN job_descriptions jd ON jd.id=requirement.job_description_id WHERE option.id=$1`, s.option).Scan(&id, &resolution, &status); err != nil || id != want || resolution != "resolved" || status != "included" {
			t.Fatalf("business mapping changed: %s %s %s %v", id, resolution, status, err)
		}
	}
	aliasExists := func(label string) bool {
		t.Helper()
		var found bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM abilities,jsonb_array_elements_text(aliases) alias WHERE normalize_ability_name(alias)=$1)`, NormalizeAbilityName(label)).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}

	// All layers agree on full-width characters, Unicode whitespace and separators.
	for _, value := range []string{"Ｇｏ－Ｌａｎｇ", "Auto·Gen", "Auto_Gen", "  Go\tLang ", "C++", "C#", ".NET"} {
		var normalized string
		if err := db.QueryRowContext(ctx, `SELECT normalize_ability_name($1)`, value).Scan(&normalized); err != nil || normalized != NormalizeAbilityName(value) {
			t.Fatalf("normalization disagreement for %q: %q %q %v", value, normalized, NormalizeAbilityName(value), err)
		}
	}
	seed("Golang", "使用 Golang 开发服务", goID)
	if _, err := repo.Claim(ctx, time.Minute); !errors.Is(err, abilityreview.ErrNoRequest) {
		t.Fatalf("known aliases should not create a task: %v", err)
	}

	first := seed("Go语言", "使用 Go语言 开发服务", goID)
	second := seed("Go语言", "使用 Go语言 开发网关", goID)
	input := claim()
	if len(input.Evidence) != 2 || input.TargetAbilityCode != goCode {
		t.Fatalf("deduplication/evidence missing: %+v", input)
	}
	if _, err := repo.Claim(ctx, time.Minute); !errors.Is(err, abilityreview.ErrNoRequest) {
		t.Fatalf("duplicate task: %v", err)
	}
	stale := input
	stale.LeaseToken = uuid.New()
	approved := abilityreview.Result{Decision: "approve_alias", Reason: "通用同义名称", ExistingAbilityCode: goCode, ContextIndependent: true}
	if err := repo.Complete(ctx, stale, approved, uuid.Nil); !errors.Is(err, abilityreview.ErrLeaseLost) {
		t.Fatalf("stale lease wrote alias: %v", err)
	}
	if aliasExists("Go语言") {
		t.Fatal("stale worker published alias")
	}
	usage, _, err := repo.ReserveUsage(ctx, input, "mock")
	if err != nil {
		t.Fatal(err)
	}
	complete(input, "approve_alias", usage)
	if !aliasExists("Go语言") {
		t.Fatal("approved alias missing")
	}
	assertMapped(first, goID)
	assertMapped(second, goID)
	var aliasID uuid.UUID
	var usageStatus, purpose string
	if err := db.QueryRowContext(ctx, `SELECT id FROM reviewed_ability_aliases WHERE review_request_id=$1 AND is_active`, input.ID).Scan(&aliasID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT status,purpose FROM platform_model_usage WHERE id=$1`, usage).Scan(&usageStatus, &purpose); err != nil || usageStatus != "succeeded" || purpose != "ability_alias_review" {
		t.Fatalf("usage not recorded: %s %s %v", usageStatus, purpose, err)
	}
	var vectorJobs int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embedding_jobs job JOIN abilities ability ON ability.id=job.source_id WHERE source_type='ability' AND source_id=$1 AND source_hash=encode(digest(ability.name||ability.aliases::text||COALESCE(ability.definition,''),'sha256'),'hex') AND status='queued'`, goID).Scan(&vectorJobs); err != nil || vectorJobs != 1 {
		t.Fatalf("new alias didn't refresh vector: %d %v", vectorJobs, err)
	}
	if err := repo.RevokeReviewedAlias(ctx, aliasID, "人工核查不适合作为公共别名"); err != nil {
		t.Fatal(err)
	}
	if aliasExists("Go语言") {
		t.Fatal("revoked alias still matches")
	}
	seed("Go语言", "再次使用 Go语言 开发服务", goID)
	if _, err := repo.Claim(ctx, time.Minute); !errors.Is(err, abilityreview.ErrNoRequest) {
		t.Fatalf("revoked alias was automatically reapplied: %v", err)
	}
	assertMapped(first, goID)

	contextual := seed("Go并发编程", "掌握 Go并发编程 以实现并发任务", goID)
	rejected := claim()
	complete(rejected, "reject_alias", uuid.Nil)
	assertMapped(contextual, goID)
	if aliasExists("Go并发编程") {
		t.Fatal("contextual wording became an alias")
	}
	seed("Go并发编程", "掌握 Go并发编程 以实现并发任务", goID)
	if _, err := repo.Claim(ctx, time.Minute); !errors.Is(err, abilityreview.ErrNoRequest) {
		t.Fatalf("same rejection was reviewed again: %v", err)
	}
	seed("Go并发编程", "掌握 Go并发编程 以设计协程池", goID)
	complete(claim(), "reject_alias", uuid.Nil)

	failedSource := seed("Go测试别称", "用 Go测试别称 编写后端接口", goID)
	failed := claim()
	if err := repo.Complete(ctx, failed, abilityreview.Result{Decision: "approve_alias", Reason: "未声明脱离语境同义", ExistingAbilityCode: goCode}, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	var status, lastError string
	if err := db.QueryRowContext(ctx, `SELECT status,last_error FROM ability_review_requests WHERE id=$1`, failed.ID).Scan(&status, &lastError); err != nil || status != "failed" || lastError != "model_invalid_response" {
		t.Fatalf("malformed response not retryable: %s %s %v", status, lastError, err)
	}
	assertMapped(failedSource, goID)
	if _, err := db.ExecContext(ctx, `UPDATE ability_review_requests SET next_attempt_at=NOW() WHERE id=$1`, failed.ID); err != nil {
		t.Fatal(err)
	}
	complete(claim(), "reject_alias", uuid.Nil)

	changed := seed("Go旧别称", "掌握 Go旧别称 开发接口", goID)
	old := claim()
	if _, err := db.ExecContext(ctx, `UPDATE job_descriptions SET raw_text='已经换成新职责' WHERE id=$1`, changed.jd); err != nil {
		t.Fatal(err)
	}
	complete(old, "approve_alias", uuid.Nil)
	if aliasExists("Go旧别称") {
		t.Fatal("outdated source published an alias")
	}
	assertMapped(changed, goID)

	conflictGo := seed("公共框架别称", "掌握 公共框架别称 开发 Go 服务", goID)
	conflictJava := seed("公共框架别称", "掌握 公共框架别称 开发 Java 服务", javaID)
	owner := claim()
	other := claim()
	// Both models may approve concurrently; only one target can claim the alias.
	results := make(chan error, 2)
	for _, candidate := range []abilityreview.Input{owner, other} {
		go func(input abilityreview.Input) {
			results <- repo.Complete(ctx, input, abilityreview.Result{Decision: "approve_alias", Reason: "独立审核通过", ExistingAbilityCode: input.TargetAbilityCode, ContextIndependent: true}, uuid.Nil)
		}(candidate)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var accepted int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reviewed_ability_aliases WHERE normalized_name=$1 AND is_active`, NormalizeAbilityName("公共框架别称")).Scan(&accepted); err != nil || accepted != 1 {
		t.Fatalf("ambiguous alias published twice: %d %v", accepted, err)
	}
	assertMapped(conflictGo, goID)
	assertMapped(conflictJava, javaID)
	var rejectedCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ability_review_requests WHERE id IN ($1,$2) AND decision='reject_alias'`, other.ID, owner.ID).Scan(&rejectedCount); err != nil || rejectedCount != 1 {
		t.Fatalf("conflicting reviewer wasn't rejected: %d %v", rejectedCount, err)
	}

	rolledBack := seed("Go回滚名称", "掌握 Go回滚名称 开发服务", goID)
	rollbackInput := claim()
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_alias_receipt_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.proposed_name='Go回滚名称' AND NEW.status='succeeded' THEN RAISE EXCEPTION 'test receipt failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER fail_alias_receipt_test BEFORE UPDATE ON ability_review_requests FOR EACH ROW EXECUTE FUNCTION fail_alias_receipt_test()`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, rollbackInput, approved, uuid.Nil); err == nil {
		t.Fatal("expected receipt failure")
	}
	if aliasExists("Go回滚名称") {
		t.Fatal("alias survived a failed review transaction")
	}
	var orphanedAlias int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reviewed_ability_aliases WHERE review_request_id=$1`, rollbackInput.ID).Scan(&orphanedAlias); err != nil || orphanedAlias != 0 {
		t.Fatalf("partial audit record: %d %v", orphanedAlias, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_alias_receipt_test ON ability_review_requests; DROP FUNCTION fail_alias_receipt_test()`); err != nil {
		t.Fatal(err)
	}
	complete(rollbackInput, "reject_alias", uuid.Nil)
	assertMapped(rolledBack, goID)

	// Resolving a pending new-ability request must also perform a separate alias
	// review, rather than silently treating reuse_existing as alias approval.
	followup := seed("Go工程语言", "使用 Go工程语言 开发服务", goID)
	if _, err := db.ExecContext(ctx, `DELETE FROM ability_review_requests WHERE review_type='alias' AND normalized_name=$1`, NormalizeAbilityName("Go工程语言")); err != nil {
		t.Fatal(err)
	}
	requestID, token := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO ability_review_requests(id,candidate_key,normalized_name,proposed_name,initiated_by_user_id,status,attempts,lease_token,lease_expires_at) VALUES($1,$2,$3,'Go工程语言',$4,'running',1,$5,NOW()+INTERVAL '5 minutes')`, requestID, "unknown:"+NormalizeAbilityName("Go工程语言"), NormalizeAbilityName("Go工程语言"), followup.user, token); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE job_description_ability_requirement_options SET ability_id=NULL,resolution_status='pending_review',review_request_id=$2 WHERE id=$1`, followup.option, requestID); err != nil {
		t.Fatal(err)
	}
	catalog, err := repo.loadAbilityCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, abilityreview.Input{ID: requestID, LeaseToken: token, Catalog: catalog}, abilityreview.Result{Decision: "reuse_existing", Reason: "该表述在本 JD 中归入 Go", ExistingAbilityCode: goCode}, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	aliasFollowup := claim()
	if aliasFollowup.Name != "Go工程语言" {
		t.Fatalf("wrong follow-up request: %+v", aliasFollowup)
	}
	assertMapped(followup, goID)
	complete(aliasFollowup, "reject_alias", uuid.Nil)

	// Confirmed materials and their independent reassessment use the same review
	// path without changing the user's level after an alias refusal.
	materialID, materialUser := uuid.New(), uuid.New()
	quote := "使用 Go开发语言 和 Go二次名称 实现网关"
	if _, err := db.ExecContext(ctx, `INSERT INTO user_profile_materials(id,user_id,type,title,source_text,status) VALUES($1,$2,'experience','测试材料',$3,'processing')`, materialID, materialUser, quote); err != nil {
		t.Fatal(err)
	}
	draft := profile.EvidenceDraft{AbilityID: goID, Level: 2, Quote: quote, Reason: "独立完成接口", Confidence: 0.8, RawLabel: "Go开发语言", MappingReason: "材料中对应该语言"}
	if _, err := NewProfileRepository(db).CompleteMaterialAnalysis(ctx, materialUser, materialID, []profile.EvidenceDraft{draft}); err != nil {
		t.Fatal(err)
	}
	materialAlias := claim()
	if len(materialAlias.Evidence) != 1 || materialAlias.Name != draft.RawLabel {
		t.Fatalf("material alias not queued: %+v", materialAlias)
	}
	complete(materialAlias, "reject_alias", uuid.Nil)
	jobID, jobToken := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO material_reassessment_jobs(id,user_id,material_id,source_hash,status,lease_token,lease_expires_at) VALUES($1,$2,$3,$4,'running',$5,NOW()+INTERVAL '5 minutes')`, jobID, materialUser, materialID, materialHash(quote), jobToken); err != nil {
		t.Fatal(err)
	}
	draft.RawLabel = "Go二次名称"
	if err := NewMaterialReassessmentRepository(db, NewProfileRepository(db), nil).CompleteReassessment(ctx, profile.ReassessmentJob{ID: jobID, UserID: materialUser, MaterialID: materialID, SourceHash: materialHash(quote), LeaseToken: jobToken}, []profile.EvidenceDraft{draft}); err != nil {
		t.Fatal(err)
	}
	complete(claim(), "reject_alias", uuid.Nil)
	var currentLevel int
	if err := db.QueryRowContext(ctx, `SELECT current_level FROM user_capability_profiles WHERE user_id=$1 AND ability_id=$2`, materialUser, goID).Scan(&currentLevel); err != nil || currentLevel != 2 {
		t.Fatalf("alias rejection changed user's level: %d %v", currentLevel, err)
	}

	leaseSource := seed("Go恢复名称", "掌握 Go恢复名称 开发接口", goID)
	interrupted := claim()
	if _, err := db.ExecContext(ctx, `UPDATE ability_review_requests SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, interrupted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	reclaimed := claim()
	if err := repo.Complete(ctx, interrupted, approved, uuid.Nil); !errors.Is(err, abilityreview.ErrLeaseLost) {
		t.Fatalf("interrupted worker retained write rights: %v", err)
	}
	complete(reclaimed, "reject_alias", uuid.Nil)
	assertMapped(leaseSource, goID)

	// Alias calls share the existing account/global platform-review budget.
	quotaSource := seed("Go额度别称", "用 Go额度别称 编写接口", goID)
	quota := claim()
	if _, err := db.ExecContext(ctx, `UPDATE ability_review_requests SET initiated_by_user_id=$2 WHERE id=$1`, quota.ID, input.UserID); err != nil {
		t.Fatal(err)
	}
	quota.UserID = input.UserID
	limited := NewAbilityReviewRepository(db)
	limited.quota = AbilityReviewQuotaLimits{UserDaily: 1, GlobalDaily: 0}
	if _, next, err := limited.ReserveUsage(ctx, quota, "mock"); !errors.Is(err, abilityreview.ErrQuota) || next.IsZero() {
		t.Fatalf("alias bypassed account quota: %v %v", next, err)
	}
	assertMapped(quotaSource, goID)
}

func TestJDParsedReuseEnqueuesAliasReviewIsolated(t *testing.T) {
	db := agentTransactionDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	user, target, jd, jobID, lease := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	quote := "使用 Go源语言 开发服务"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO job_targets(id,user_id,title,employment_type,catalog_status) VALUES($1,$2,'后端开发','internship','valid')`, target, user)
	exec(`INSERT INTO target_directions(target_id,category_id,sort_order) SELECT $1,id,1 FROM job_categories WHERE code='backend'`, target)
	exec(`INSERT INTO job_descriptions(id,user_id,target_id,raw_text,raw_text_hash,status) VALUES($1,$2,$3,$4,encode(digest($4,'sha256'),'hex'),'processing')`, jd, user, target, quote)
	exec(`INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status,attempts,lease_token,lease_expires_at) VALUES($1,$2,$3,$4,'jd_analysis','running',1,$5,NOW()+INTERVAL '5 minutes')`, jobID, user, target, jd, lease)
	result := jdanalysis.Result{DocumentType: jdanalysis.DocumentJobDescription, ValidationStatus: jdanalysis.ValidationValid, Title: "后端实习", EmploymentType: "internship", Responsibilities: []string{quote}, Conditions: []string{}, VectorNormalized: true, PromptVersion: jdanalysis.PromptVersion,
		Classifications:     []jdanalysis.JobClassification{{CategoryCode: "backend", SpecialtyCode: "backend-business", Relation: "primary", Evidence: quote, Reason: "负责业务服务"}},
		AbilityRequirements: []jdanalysis.AbilityRequirement{{Operator: jdanalysis.RequirementSingle, RequiredCount: 1, Evidence: quote, Options: []jdanalysis.AbilityRequirementOption{{RawLabel: "Go源语言", AbilityName: "Go", CatalogCode: "ability-005", Evidence: quote, NormalizationReason: "本句中表示 Go"}}}}}
	analysis := NewAnalysisRepository(db)
	if err := analysis.Complete(ctx, jdanalysis.Job{ID: jobID, UserID: user, TargetID: target, JobDescriptionID: jd, LeaseToken: lease}, result); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM job_descriptions WHERE id=$1`, jd).Scan(&status); err != nil || status != "included" {
		t.Fatalf("JD not included before alias review: %s %v", status, err)
	}

	normalizationID, normalizationLease := uuid.New(), uuid.New()
	exec(`INSERT INTO jd_normalization_jobs(id,user_id,job_description_id,source_hash,status,lease_token,lease_expires_at) VALUES($1,$2,$3,encode(digest($4,'sha256'),'hex'),'running',$5,NOW()+INTERVAL '5 minutes')
		ON CONFLICT(job_description_id) DO UPDATE SET id=EXCLUDED.id,status=EXCLUDED.status,lease_token=EXCLUDED.lease_token,lease_expires_at=EXCLUDED.lease_expires_at,source_hash=EXCLUDED.source_hash`, normalizationID, user, jd, quote, normalizationLease)
	normalizationJob := jdanalysis.NormalizationJob{ID: normalizationID, UserID: user, JobDescriptionID: jd, SourceHash: materialHash(quote), LeaseToken: normalizationLease}
	normalization := NewJDNormalizationRepository(db, analysis, nil)
	_, loaded, err := normalization.LoadNormalization(ctx, normalizationJob)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AbilityRequirements[0].Options[0].NormalizationReason != "本句中表示 Go" {
		t.Fatal("normalization reason wasn't persisted")
	}
	loaded.VectorNormalized = true
	if err := normalization.CompleteNormalization(ctx, normalizationJob, loaded); err != nil {
		t.Fatal(err)
	}
	review := NewAbilityReviewRepository(db)
	input, err := review.Claim(ctx, time.Minute*5)
	if err != nil || input.ReviewType != "alias" || len(input.Evidence) != 1 {
		t.Fatalf("parsed/renormalized alias missing: %+v %v", input, err)
	}
	if _, err := review.Claim(ctx, time.Minute); !errors.Is(err, abilityreview.ErrNoRequest) {
		t.Fatalf("normalization duplicated alias task: %v", err)
	}
	if err := review.Complete(ctx, input, abilityreview.Result{Decision: "reject_alias", Reason: "名称不可靠", PromptVersion: abilityreview.AliasPromptVersion}, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	var mapped int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_description_ability_requirement_options option JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id WHERE requirement.job_description_id=$1 AND option.resolution_status='resolved'`, jd).Scan(&mapped); err != nil || mapped != 1 {
		t.Fatalf("alias refusal broke completed JD: %d %v", mapped, err)
	}
}
