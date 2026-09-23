package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/google/uuid"
)

type AnalysisRepository struct {
	database *sql.DB
}

func NewAnalysisRepository(database *sql.DB) *AnalysisRepository {
	return &AnalysisRepository{database: database}
}

func (r *AnalysisRepository) Catalog(ctx context.Context) (jdanalysis.Catalog, error) {
	jobRows, err := r.database.QueryContext(ctx, `
		SELECT category.code, category.name, category.description, specialty.code, specialty.name,
		       specialty.definition, specialty.include_signals,
		       specialty.exclude_signals, specialty.confused_with
		FROM job_categories category
		LEFT JOIN job_specialties specialty
		  ON specialty.category_id = category.id AND specialty.is_active
		WHERE category.is_active
		ORDER BY category.sort_order, specialty.sort_order`)
	if err != nil {
		return jdanalysis.Catalog{}, fmt.Errorf("load job catalog: %w", err)
	}
	jobCategories := make([]jdanalysis.JobCategoryOption, 0)
	categoryIndex := make(map[string]int)
	for jobRows.Next() {
		var categoryCode, categoryName, categoryDefinition string
		var specialtyCode, specialtyName, specialtyDefinition sql.NullString
		var includeSignals, excludeSignals, confusedWith []byte
		if err := jobRows.Scan(
			&categoryCode, &categoryName, &categoryDefinition, &specialtyCode, &specialtyName,
			&specialtyDefinition, &includeSignals, &excludeSignals, &confusedWith,
		); err != nil {
			jobRows.Close()
			return jdanalysis.Catalog{}, fmt.Errorf("scan job catalog: %w", err)
		}
		index, exists := categoryIndex[categoryCode]
		if !exists {
			index = len(jobCategories)
			categoryIndex[categoryCode] = index
			jobCategories = append(jobCategories, jdanalysis.JobCategoryOption{Code: categoryCode, Name: categoryName, Definition: categoryDefinition})
		}
		if specialtyCode.Valid {
			specialty := jdanalysis.SpecialtyOption{
				Code: specialtyCode.String, Name: specialtyName.String, Definition: specialtyDefinition.String,
			}
			if err := json.Unmarshal(includeSignals, &specialty.IncludeSignals); err != nil {
				jobRows.Close()
				return jdanalysis.Catalog{}, fmt.Errorf("decode specialty include signals: %w", err)
			}
			if err := json.Unmarshal(excludeSignals, &specialty.ExcludeSignals); err != nil {
				jobRows.Close()
				return jdanalysis.Catalog{}, fmt.Errorf("decode specialty exclude signals: %w", err)
			}
			if err := json.Unmarshal(confusedWith, &specialty.ConfusedWith); err != nil {
				jobRows.Close()
				return jdanalysis.Catalog{}, fmt.Errorf("decode specialty confusion rules: %w", err)
			}
			jobCategories[index].Specialties = append(jobCategories[index].Specialties, specialty)
		}
	}
	if err := jobRows.Err(); err != nil {
		jobRows.Close()
		return jdanalysis.Catalog{}, fmt.Errorf("iterate job catalog: %w", err)
	}
	if err := jobRows.Close(); err != nil {
		return jdanalysis.Catalog{}, fmt.Errorf("close job catalog: %w", err)
	}

	abilityRows, err := r.database.QueryContext(ctx, `
		SELECT ability.code, ability.name, category.code, category.name, ability.aliases, COALESCE(ability.definition,'')
		FROM abilities ability JOIN ability_categories category ON category.id=ability.category_id
		WHERE ability.is_active ORDER BY category.sort_order, ability.sort_order`)
	if err != nil {
		return jdanalysis.Catalog{}, fmt.Errorf("load ability catalog: %w", err)
	}
	defer abilityRows.Close()
	abilities := make([]jdanalysis.AbilityOption, 0)
	for abilityRows.Next() {
		var ability jdanalysis.AbilityOption
		var aliases []byte
		if err := abilityRows.Scan(&ability.Code, &ability.Name, &ability.CategoryCode, &ability.CategoryName, &aliases, &ability.Definition); err != nil {
			return jdanalysis.Catalog{}, fmt.Errorf("scan ability catalog: %w", err)
		}
		if err := json.Unmarshal(aliases, &ability.Aliases); err != nil {
			return jdanalysis.Catalog{}, fmt.Errorf("decode ability aliases: %w", err)
		}
		abilities = append(abilities, ability)
	}
	if err := abilityRows.Err(); err != nil {
		return jdanalysis.Catalog{}, fmt.Errorf("iterate ability catalog: %w", err)
	}
	return jdanalysis.Catalog{JobCategories: jobCategories, Abilities: abilities}, nil
}

func (r *AnalysisRepository) RecoverExpired(ctx context.Context) (int64, error) {
	var recovered int64
	err := r.database.QueryRowContext(ctx, `
		WITH recovered AS (
			UPDATE analysis_jobs
			SET status = 'failed', next_attempt_at = NOW(), locked_at = NULL,
			    heartbeat_at = NULL, lease_expires_at = NULL, lease_token = NULL,
			    last_error = 'worker_interrupted', updated_at = NOW()
			WHERE job_type = 'jd_analysis' AND status = 'running'
			  AND lease_expires_at < NOW()
			RETURNING job_description_id, user_id, attempts >= max_attempts AS exhausted,
			          preserve_previous_result
		), marked AS (
			UPDATE job_descriptions jd
			SET status = 'failed',
			    relevance_reason = 'JD 分析因 Worker 中断且已达到最大尝试次数。',
			    updated_at = NOW()
			FROM recovered
			WHERE recovered.exhausted
			  AND NOT recovered.preserve_previous_result
			  AND jd.id = recovered.job_description_id
			  AND jd.user_id = recovered.user_id
			RETURNING jd.id
		)
		SELECT COUNT(*) FROM recovered`).Scan(&recovered)
	if err != nil {
		return 0, fmt.Errorf("recover expired analysis jobs: %w", err)
	}
	return recovered, nil
}

func (r *AnalysisRepository) Claim(ctx context.Context, leaseDuration time.Duration) (jdanalysis.Job, error) {
	leaseToken := uuid.New()
	const query = `
		WITH candidate AS (
			SELECT id
			FROM analysis_jobs
			WHERE job_type = 'jd_analysis'
			  AND status IN ('queued', 'failed')
			  AND attempts < max_attempts
			  AND next_attempt_at <= NOW()
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE analysis_jobs job
		SET status = 'running', attempts = job.attempts + 1,
		    lease_token = $1, heartbeat_at = NOW(),
		    lease_expires_at = NOW() + ($2 * INTERVAL '1 millisecond'),
		    locked_at = NOW(), updated_at = NOW()
		FROM candidate, job_descriptions jd
		WHERE job.id = candidate.id AND jd.id = job.job_description_id
		RETURNING job.id, job.user_id, job.target_id, job.job_description_id,
		          jd.raw_text, job.attempts, job.max_attempts, job.lease_token,
		          job.preserve_previous_result`
	var result jdanalysis.Job
	if err := r.database.QueryRowContext(ctx, query, leaseToken, leaseDuration.Milliseconds()).Scan(
		&result.ID, &result.UserID, &result.TargetID, &result.JobDescriptionID,
		&result.RawText, &result.Attempts, &result.MaxAttempts, &result.LeaseToken,
		&result.PreservePreviousResult,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return jdanalysis.Job{}, jdanalysis.ErrNoJob
		}
		return jdanalysis.Job{}, fmt.Errorf("claim analysis job: %w", err)
	}
	return result, nil
}

func (r *AnalysisRepository) Heartbeat(ctx context.Context, job jdanalysis.Job, leaseDuration time.Duration) error {
	result, err := r.database.ExecContext(ctx, `
		UPDATE analysis_jobs
		SET heartbeat_at = NOW(),
		    lease_expires_at = NOW() + ($3 * INTERVAL '1 millisecond'),
		    updated_at = NOW()
		WHERE id = $1 AND status = 'running' AND lease_token = $2`,
		job.ID, job.LeaseToken, leaseDuration.Milliseconds())
	if err != nil {
		return fmt.Errorf("heartbeat analysis job: %w", err)
	}
	return requireLease(result)
}

func (r *AnalysisRepository) Complete(ctx context.Context, job jdanalysis.Job, result jdanalysis.Result) error {
	responsibilities, err := json.Marshal(result.Responsibilities)
	if err != nil {
		return fmt.Errorf("marshal responsibilities: %w", err)
	}
	resolvedMentions := make([]jdanalysis.AbilityMention, 0, len(result.AbilityMentions))
	for _, mention := range result.AbilityMentions {
		if mention.CatalogCode != "" {
			resolvedMentions = append(resolvedMentions, mention)
		}
	}
	mentions, err := json.Marshal(resolvedMentions)
	if err != nil {
		return fmt.Errorf("marshal ability mentions: %w", err)
	}
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin completion transaction: %w", err)
	}
	defer transaction.Rollback()

	claim, err := transaction.ExecContext(ctx, `
		UPDATE analysis_jobs
		SET status = 'succeeded', locked_at = NULL, last_error = NULL,
		    heartbeat_at = NULL, lease_expires_at = NULL, lease_token = NULL,
		    preserve_previous_result = FALSE, updated_at = NOW()
		WHERE id = $1 AND status = 'running' AND lease_token = $2`, job.ID, job.LeaseToken)
	if err != nil {
		return fmt.Errorf("complete analysis job: %w", err)
	}
	if err := requireLease(claim); err != nil {
		return err
	}

	jdStatus := "excluded"
	relevanceReason := "无法识别为岗位招聘说明"
	primaryCategory, secondaryCategory := "", ""
	if result.ValidationStatus == jdanalysis.ValidationValid {
		result.Classifications, err = resolveJobClassificationCandidates(ctx, transaction, result.Classifications, result.ClassificationReview)
		if err != nil {
			return err
		}
		outcome, err := saveJDClassification(ctx, transaction, job, result)
		if err != nil {
			return err
		}
		jdStatus, relevanceReason = outcome.status, outcome.reason
		primaryCategory, secondaryCategory = outcome.primaryCategory, outcome.secondaryCategory
	}
	if result.ValidationStatus != jdanalysis.ValidationValid {
		relevanceReason = result.ValidationReason
	}
	var reviewDecision, reviewReason, reviewProvider, reviewModel, reviewPromptVersion, reviewRequestID any
	var reviewInputTokens, reviewOutputTokens any
	if result.ClassificationReview != nil {
		reviewDecision = result.ClassificationReview.Decision
		reviewReason = result.ClassificationReview.Reason
		reviewProvider = result.ClassificationReview.Provider
		reviewModel = result.ClassificationReview.Model
		reviewPromptVersion = result.ClassificationReview.PromptVersion
		reviewRequestID = result.ClassificationReview.ProviderRequestID
		reviewInputTokens = result.ClassificationReview.InputTokens
		reviewOutputTokens = result.ClassificationReview.OutputTokens
		if result.ClassificationReview.Decision == "reject" {
			relevanceReason = "岗位分类审核未通过：" + result.ClassificationReview.Reason
		}
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE job_descriptions
		SET title = $2, company = $3, employment_type = $4,
		    responsibilities = $5, ability_mentions = $6, conditions = $7,
		    document_type = $8, validation_status = $9, validation_reason = $10,
		    analysis_provider = $11, analysis_model = $12, analysis_prompt_version = $13,
		    analysis_completed_at = NOW(), status = $14, relevance_reason = $15,
		    primary_category = $16, secondary_category = $17,
		    classification_review_decision = $18, classification_review_reason = $19,
		    classification_review_provider = $20, classification_review_model = $21,
		    classification_review_prompt_version = $22, classification_review_request_id = $23,
		    classification_review_input_tokens = $24, classification_review_output_tokens = $25,
		    classification_reviewed_at = CASE WHEN $18::varchar IS NULL THEN NULL ELSE NOW() END,
		    updated_at = NOW()
		WHERE id = $1 AND user_id = $26`,
		job.JobDescriptionID, result.Title, result.Company, result.EmploymentType,
		responsibilities, mentions, strings.Join(result.Conditions, "；"),
		result.DocumentType, result.ValidationStatus, result.ValidationReason,
		result.Provider, result.Model, result.PromptVersion, jdStatus, relevanceReason,
		primaryCategory, secondaryCategory, reviewDecision, reviewReason, reviewProvider, reviewModel,
		reviewPromptVersion, reviewRequestID, reviewInputTokens, reviewOutputTokens, job.UserID,
	); err != nil {
		return fmt.Errorf("save JD analysis: %w", err)
	}
	if jdStatus == "included" {
		if err := enqueueJDAbilityGrading(ctx, transaction, job.UserID, job.TargetID, job.JobDescriptionID); err != nil {
			return fmt.Errorf("enqueue JD ability grading: %w", err)
		}
	} else {
		if _, err := transaction.ExecContext(ctx, `DELETE FROM jd_ability_level_jobs WHERE job_description_id=$1`, job.JobDescriptionID); err != nil {
			return fmt.Errorf("clear irrelevant JD ability grading job: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `DELETE FROM jd_ability_level_assessments WHERE job_description_id=$1`, job.JobDescriptionID); err != nil {
			return fmt.Errorf("clear irrelevant JD ability levels: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit analysis completion: %w", err)
	}
	return nil
}

type classificationOutcome struct {
	status            string
	reason            string
	primaryCategory   string
	secondaryCategory string
}

type savedClassification struct {
	categoryCode  string
	specialtyCode string
	categoryName  string
	specialtyName string
	relation      string
}

type pendingAbilityOption struct {
	id     uuid.UUID
	option jdanalysis.AbilityRequirementOption
}

func resolveJobClassificationCandidates(ctx context.Context, tx *sql.Tx, classifications []jdanalysis.JobClassification, review *jdanalysis.ClassificationReviewResult) ([]jdanalysis.JobClassification, error) {
	hasCandidate := false
	for _, classification := range classifications {
		if classification.Candidate != nil {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		return classifications, nil
	}
	if review == nil || (review.Decision != "accept" && review.Decision != "correct") {
		return nil, errors.New("refusing to persist an unreviewed job classification candidate")
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(741905)`); err != nil {
		return nil, fmt.Errorf("lock dynamic job catalog: %w", err)
	}

	resolved := make([]jdanalysis.JobClassification, 0, len(classifications))
	for _, classification := range classifications {
		if classification.Candidate == nil {
			resolved = append(resolved, classification)
			continue
		}
		categoryID, categoryCode, err := resolveDynamicJobCategory(ctx, tx, classification.CategoryCode, classification.Candidate)
		if err != nil {
			return nil, err
		}
		specialtyCode, err := resolveDynamicJobSpecialty(ctx, tx, categoryID, classification.Candidate)
		if err != nil {
			return nil, err
		}
		classification.CategoryCode = categoryCode
		classification.SpecialtyCode = specialtyCode
		classification.Candidate = nil
		resolved = append(resolved, classification)
	}
	return resolved, nil
}

func resolveDynamicJobCategory(ctx context.Context, tx *sql.Tx, existingCode string, candidate *jdanalysis.JobClassificationCandidate) (uuid.UUID, string, error) {
	if candidate.Scope == "specialty" {
		var id uuid.UUID
		var code string
		if err := tx.QueryRowContext(ctx, `SELECT id, code FROM job_categories WHERE code=$1 AND is_active`, existingCode).Scan(&id, &code); err != nil {
			return uuid.Nil, "", fmt.Errorf("resolve parent category for dynamic specialty: %w", err)
		}
		return id, code, nil
	}
	normalized := NormalizeAbilityName(candidate.CategoryName)
	if normalized == "" {
		return uuid.Nil, "", errors.New("dynamic job category has an empty normalized name")
	}
	var id uuid.UUID
	var code string
	err := tx.QueryRowContext(ctx, `SELECT id, code FROM job_categories WHERE normalized_name=$1 AND is_active`, normalized).Scan(&id, &code)
	if err == nil {
		return id, code, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, "", fmt.Errorf("find dynamic job category: %w", err)
	}
	id = uuid.New()
	code = dynamicJobCatalogCode("job-category-dyn-", id)
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO job_categories(id,code,name,description,normalized_name,source,sort_order)
		SELECT $1,$2,$3,$4,$5,'dynamic_review',COALESCE(MAX(sort_order),0)+1 FROM job_categories
		RETURNING id`, id, code, candidate.CategoryName, candidate.CategoryDefinition, normalized).Scan(&id); err != nil {
		return uuid.Nil, "", fmt.Errorf("create dynamic job category: %w", err)
	}
	return id, code, nil
}

func resolveDynamicJobSpecialty(ctx context.Context, tx *sql.Tx, categoryID uuid.UUID, candidate *jdanalysis.JobClassificationCandidate) (string, error) {
	normalized := NormalizeAbilityName(candidate.SpecialtyName)
	if normalized == "" {
		return "", errors.New("dynamic job specialty has an empty normalized name")
	}
	var code string
	err := tx.QueryRowContext(ctx, `SELECT code FROM job_specialties WHERE category_id=$1 AND normalized_name=$2 AND is_active`, categoryID, normalized).Scan(&code)
	if err == nil {
		return code, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("find dynamic job specialty: %w", err)
	}
	includeSignals, _ := json.Marshal(candidate.IncludeSignals)
	excludeSignals, _ := json.Marshal(candidate.ExcludeSignals)
	confusedWith, _ := json.Marshal(candidate.ConfusedWith)
	id := uuid.New()
	code = dynamicJobCatalogCode("job-specialty-dyn-", id)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO job_specialties(
			id,category_id,code,name,description,definition,include_signals,exclude_signals,
			confused_with,normalized_name,source,sort_order
		)
		SELECT $1,$2,$3,$4,$5,$5,$6,$7,$8,$9,'dynamic_review',COALESCE(MAX(sort_order),0)+1
		FROM job_specialties WHERE category_id=$2`, id, categoryID, code, candidate.SpecialtyName,
		candidate.Definition, includeSignals, excludeSignals, confusedWith, normalized); err != nil {
		return "", fmt.Errorf("create dynamic job specialty: %w", err)
	}
	return code, nil
}

func dynamicJobCatalogCode(prefix string, id uuid.UUID) string {
	compact := strings.ReplaceAll(id.String(), "-", "")
	return prefix + compact[:12]
}

func saveJDClassification(ctx context.Context, transaction *sql.Tx, job jdanalysis.Job, result jdanalysis.Result) (classificationOutcome, error) {
	if _, err := transaction.ExecContext(ctx, `DELETE FROM job_description_classifications WHERE job_description_id = $1`, job.JobDescriptionID); err != nil {
		return classificationOutcome{}, fmt.Errorf("clear JD classifications: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM job_description_abilities WHERE job_description_id = $1`, job.JobDescriptionID); err != nil {
		return classificationOutcome{}, fmt.Errorf("clear JD abilities: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM job_description_ability_requirements WHERE job_description_id = $1`, job.JobDescriptionID); err != nil {
		return classificationOutcome{}, fmt.Errorf("clear JD ability requirements: %w", err)
	}

	saved := make([]savedClassification, 0, len(result.Classifications))
	for _, classification := range result.Classifications {
		var item savedClassification
		var categoryID uuid.UUID
		var specialtyID uuid.NullUUID
		var specialtyCode, specialtyName sql.NullString
		err := transaction.QueryRowContext(ctx, `
			SELECT category.id, category.code, category.name, specialty.id, specialty.code, specialty.name
			FROM job_categories category
			LEFT JOIN job_specialties specialty
			  ON specialty.code = NULLIF($2, '') AND specialty.category_id = category.id AND specialty.is_active
			WHERE category.code = $1 AND category.is_active
			  AND ($2 = '' OR specialty.id IS NOT NULL)`, classification.CategoryCode, classification.SpecialtyCode,
		).Scan(&categoryID, &item.categoryCode, &item.categoryName, &specialtyID, &specialtyCode, &specialtyName)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return classificationOutcome{}, fmt.Errorf("resolve JD classification: %w", err)
		}
		item.specialtyCode = specialtyCode.String
		item.specialtyName = specialtyName.String
		item.relation = classification.Relation
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO job_description_classifications (
				job_description_id, category_id, specialty_id, relation, evidence, reason
			)
			SELECT $1, category.id, specialty.id, $4, $5, $6
			FROM job_categories category
			LEFT JOIN job_specialties specialty
			  ON specialty.code = NULLIF($3, '') AND specialty.category_id = category.id
			WHERE category.code = $2
			ON CONFLICT DO NOTHING`, job.JobDescriptionID, classification.CategoryCode,
			classification.SpecialtyCode, classification.Relation, classification.Evidence, classification.Reason); err != nil {
			return classificationOutcome{}, fmt.Errorf("save JD classification: %w", err)
		}
		saved = append(saved, item)
	}

	for _, mention := range result.AbilityMentions {
		if mention.CatalogCode == "" {
			continue
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO job_description_abilities (
				job_description_id, ability_id, raw_name, evidence, required_level
			)
			SELECT $1, ability.id, $3, $4, $5
			FROM abilities ability
			WHERE ability.code = $2 AND ability.is_active
			ON CONFLICT DO NOTHING`, job.JobDescriptionID, mention.CatalogCode,
			mention.Name, mention.Evidence, mention.RequiredLevel); err != nil {
			return classificationOutcome{}, fmt.Errorf("save normalized JD ability: %w", err)
		}
	}

	pending := make([]pendingAbilityOption, 0)
	for requirementIndex, requirement := range result.AbilityRequirements {
		var requirementID uuid.UUID
		if err := transaction.QueryRowContext(ctx, `
			INSERT INTO job_description_ability_requirements (
				job_description_id, operator, required_count, requirement_kind, evidence, sort_order
			) VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), 'unspecified'), $5, $6)
			RETURNING id`, job.JobDescriptionID, requirement.Operator,
			requirement.RequiredCount, requirement.RequirementKind, requirement.Evidence, requirementIndex+1,
		).Scan(&requirementID); err != nil {
			return classificationOutcome{}, fmt.Errorf("save JD ability requirement: %w", err)
		}
		for optionIndex, option := range requirement.Options {
			metadata, _ := json.Marshal(option.Candidate)
			var optionID uuid.UUID
			if err := transaction.QueryRowContext(ctx, `
				INSERT INTO job_description_ability_requirement_options (
					requirement_id, ability_id, raw_label, qualifier, evidence, required_level, sort_order,
					resolution_status, candidate_metadata
				) VALUES (
					$1, (SELECT id FROM abilities WHERE code = NULLIF($2, '') AND is_active),
					$3, $4, $5, $6, $7, CASE WHEN $2='' THEN 'pending_review' ELSE 'resolved' END, $8
				) RETURNING id`, requirementID, option.CatalogCode, option.RawLabel, option.Qualifier,
				option.Evidence, option.RequiredLevel, optionIndex+1, metadata).Scan(&optionID); err != nil {
				return classificationOutcome{}, fmt.Errorf("save JD ability requirement option: %w", err)
			}
			if option.CatalogCode == "" {
				pending = append(pending, pendingAbilityOption{id: optionID, option: option})
			}
		}
	}
	outcome, err := determineTargetRelationship(ctx, transaction, job.TargetID, result.EmploymentType, saved)
	if err != nil {
		return classificationOutcome{}, err
	}
	if outcome.status == "included" {
		for _, item := range pending {
			if err := enqueueAbilityReview(ctx, transaction, job.UserID, item); err != nil {
				return classificationOutcome{}, err
			}
		}
		if err := cleanupRequirementGroups(ctx, transaction); err != nil {
			return classificationOutcome{}, err
		}
	}
	return outcome, nil
}

func enqueueAbilityReview(ctx context.Context, tx *sql.Tx, userID uuid.UUID, pending pendingAbilityOption) error {
	name := strings.TrimSpace(pending.option.RawLabel)
	normalized := NormalizeAbilityName(name)
	if normalized == "" {
		return nil
	}
	category, definition, reason := "", "", ""
	aliases, nearest := []string{}, []string{}
	if pending.option.Candidate != nil {
		category = pending.option.Candidate.CategoryCode
		aliases = pending.option.Candidate.Aliases
		definition = pending.option.Candidate.Definition
		reason = pending.option.Candidate.Reason
		nearest = pending.option.Candidate.NearestCandidateCodes
	}
	key := category + ":" + normalized
	if category == "" {
		key = "unknown:" + normalized
	}
	var rejectedBefore bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ability_review_requests WHERE normalized_name=$1 AND status='succeeded' AND decision='reject' AND evidence_hashes @> jsonb_build_array(to_jsonb(md5($2))))`, normalized, pending.option.Evidence).Scan(&rejectedBefore); err != nil {
		return err
	}
	if rejectedBefore {
		_, err := tx.ExecContext(ctx, `DELETE FROM job_description_ability_requirement_options WHERE id=$1`, pending.id)
		return err
	}
	aliasJSON, _ := json.Marshal(aliases)
	nearestJSON, _ := json.Marshal(nearest)
	var requestID uuid.UUID
	err := tx.QueryRowContext(ctx, `INSERT INTO ability_review_requests(candidate_key,normalized_name,proposed_name,proposed_category_code,proposed_aliases,proposed_definition,application_reason,nearest_candidate_codes,evidence_hashes,initiated_by_user_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,jsonb_build_array(to_jsonb(md5($9))),$10)
		ON CONFLICT (candidate_key) WHERE status IN ('queued','running','failed') DO UPDATE SET evidence_hashes=(SELECT jsonb_agg(DISTINCT value) FROM jsonb_array_elements(ability_review_requests.evidence_hashes || EXCLUDED.evidence_hashes)),updated_at=NOW()
		RETURNING id`, key, normalized, name, category, aliasJSON, definition, reason, nearestJSON, pending.option.Evidence, userID).Scan(&requestID)
	if err != nil {
		return fmt.Errorf("enqueue ability review: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE job_description_ability_requirement_options SET review_request_id=$2 WHERE id=$1`, pending.id, requestID)
	return err
}

type targetDirection struct {
	categoryCode  string
	specialtyCode string
}

func determineTargetRelationship(ctx context.Context, transaction *sql.Tx, targetID uuid.UUID, employmentType string, classifications []savedClassification) (classificationOutcome, error) {
	var targetEmploymentType, catalogStatus string
	if err := transaction.QueryRowContext(ctx, `
		SELECT employment_type, catalog_status FROM job_targets WHERE id = $1`, targetID,
	).Scan(&targetEmploymentType, &catalogStatus); err != nil {
		return classificationOutcome{}, fmt.Errorf("load target for JD relationship: %w", err)
	}
	rows, err := transaction.QueryContext(ctx, `
		SELECT category.code, COALESCE(specialty.code, '')
		FROM target_directions direction
		JOIN job_categories category ON category.id = direction.category_id
		LEFT JOIN job_specialties specialty ON specialty.id = direction.specialty_id
		WHERE direction.target_id = $1
		ORDER BY direction.sort_order`, targetID)
	if err != nil {
		return classificationOutcome{}, fmt.Errorf("load target directions for JD relationship: %w", err)
	}
	defer rows.Close()
	targets := make([]targetDirection, 0)
	for rows.Next() {
		var direction targetDirection
		if err := rows.Scan(&direction.categoryCode, &direction.specialtyCode); err != nil {
			return classificationOutcome{}, fmt.Errorf("scan target direction for JD relationship: %w", err)
		}
		targets = append(targets, direction)
	}

	primaryNames := make([]string, 0, 1)
	secondaryNames := make([]string, 0)
	for _, classification := range classifications {
		name := classification.categoryName
		if classification.specialtyName != "" {
			name += " / " + classification.specialtyName
		}
		if classification.relation == "primary" {
			primaryNames = append(primaryNames, name)
		} else {
			secondaryNames = append(secondaryNames, name)
		}
	}
	outcome := classificationOutcome{
		primaryCategory: strings.Join(primaryNames, "、"), secondaryCategory: strings.Join(secondaryNames, "、"),
	}
	if catalogStatus != "valid" || len(targets) == 0 {
		outcome.status, outcome.reason = "reference", "当前求职目标需要重新选择岗位目录，暂不计入画像。"
		return outcome, nil
	}
	outcome.status, outcome.reason = classifyTargetRelationship(targetEmploymentType, employmentType, targets, classifications)
	return outcome, nil
}

func classifyTargetRelationship(targetEmploymentType, employmentType string, targets []targetDirection, classifications []savedClassification) (string, string) {
	if len(classifications) == 0 {
		return "excluded", "未能归入当前岗位目录，暂不计入市场画像。"
	}
	if employmentType == "unknown" || employmentType != targetEmploymentType {
		return "excluded", "求职类型与当前目标不一致或不明确，未计入市场画像。"
	}

	primaryMatch := false
	secondaryMatch := false
	for _, classification := range classifications {
		for _, target := range targets {
			if classification.categoryCode != target.categoryCode {
				continue
			}
			if target.specialtyCode != "" && classification.specialtyCode != target.specialtyCode {
				continue
			}
			if classification.relation == "primary" {
				primaryMatch = true
			} else {
				secondaryMatch = true
			}
			break
		}
	}
	if primaryMatch {
		return "included", "主导分类命中当前目标方向，且求职类型一致，已计入市场画像。"
	}
	if secondaryMatch {
		return "reference", "只有次要分类命中当前目标方向，保留作参考。"
	}
	return "excluded", "岗位分类未命中当前求职目标，未计入市场画像。"
}

func (r *AnalysisRepository) Fail(ctx context.Context, job jdanalysis.Job, code string, retryable bool) error {
	finalFailure := !retryable || job.Attempts >= job.MaxAttempts
	nextAttempt := time.Now().Add(time.Duration(5*(1<<min(job.Attempts, 6))) * time.Second)
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin failure transaction: %w", err)
	}
	defer transaction.Rollback()

	attempts := job.Attempts
	if finalFailure {
		attempts = job.MaxAttempts
	}
	claim, err := transaction.ExecContext(ctx, `
		UPDATE analysis_jobs
		SET status = 'failed', attempts = $3, next_attempt_at = $4,
		    locked_at = NULL, heartbeat_at = NULL, lease_expires_at = NULL,
		    lease_token = NULL, last_error = $5, updated_at = NOW()
		WHERE id = $1 AND status = 'running' AND lease_token = $2`,
		job.ID, job.LeaseToken, attempts, nextAttempt, code)
	if err != nil {
		return fmt.Errorf("fail analysis job: %w", err)
	}
	if err := requireLease(claim); err != nil {
		return err
	}
	if finalFailure && !job.PreservePreviousResult {
		failureReason := analysisFailureReason(code)
		if _, err := transaction.ExecContext(ctx, `
			UPDATE job_descriptions
			SET status = 'failed', relevance_reason = $3, updated_at = NOW()
			WHERE id = $1 AND user_id = $2`, job.JobDescriptionID, job.UserID, failureReason); err != nil {
			return fmt.Errorf("mark JD failed: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit analysis failure: %w", err)
	}
	return nil
}

func analysisFailureReason(code string) string {
	switch code {
	case "model_invalid_response":
		return "模型返回的结构化结果不符合要求，系统自动重试后仍未通过，请重新分析。"
	case "model_not_configured", "model_configuration_unavailable":
		return "JD 分析未完成，请检查模型配置后重试。"
	case "catalog_unavailable":
		return "岗位或能力目录暂时不可用，请稍后重新分析。"
	default:
		return "JD 分析暂时失败，原文已保存，请稍后重新分析。"
	}
}

func requireLease(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected analysis jobs: %w", err)
	}
	if rows == 0 {
		return jdanalysis.ErrLeaseLost
	}
	return nil
}
