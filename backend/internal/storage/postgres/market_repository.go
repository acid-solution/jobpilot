package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/LeoninCS/jobpilot-next/backend/internal/readiness"
	"github.com/google/uuid"
)

type MarketRepository struct {
	database *repositoryDatabase
	quota    AbilityReviewQuotaLimits
}

func NewMarketRepository(database *sql.DB, configured ...AbilityReviewQuotaLimits) *MarketRepository {
	quota := DefaultAbilityReviewQuotaLimits()
	if len(configured) > 0 {
		quota = configured[0]
	}
	return &MarketRepository{database: newRepositoryDatabase(database), quota: quota}
}

func (r *MarketRepository) ListByTarget(
	ctx context.Context,
	userID uuid.UUID,
	targetID uuid.UUID,
	filter market.ListFilter,
) ([]market.JobDescription, error) {
	query := `
		SELECT jd.id, jd.target_id, COALESCE(jd.title, ''), COALESCE(jd.company, ''), jd.status,
		       COALESCE(jd.primary_category, ''), COALESCE(jd.secondary_category, ''),
		       COALESCE(jd.relevance_reason, ''), COALESCE(jd.conditions, ''), jd.raw_text,
		       jd.created_at, jd.updated_at,
		       COALESCE(CASE WHEN job.status='failed' AND job.attempts < job.max_attempts THEN 'queued' ELSE job.status END, ''),
		       jd.employment_type, jd.responsibilities, jd.ability_mentions,
		       COALESCE(jd.analysis_provider, ''), COALESCE(jd.analysis_model, ''),
		       COALESCE(jd.analysis_prompt_version, ''), COALESCE(job.last_error, ''),
		       COALESCE(jd.document_type, ''), jd.validation_status,
		       COALESCE(jd.validation_reason, '')
		FROM job_descriptions jd
		LEFT JOIN analysis_jobs job
		  ON job.job_description_id = jd.id AND job.job_type = 'jd_analysis'
		WHERE jd.user_id = $1 AND jd.target_id = $2`
	arguments := []any{userID, targetID}
	placeholder := func(value any) string {
		arguments = append(arguments, value)
		return fmt.Sprintf("$%d", len(arguments))
	}
	if len(filter.Statuses) > 0 {
		parts := make([]string, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			switch status {
			case market.StatusFailed:
				parts = append(parts, "job.status = 'failed' AND job.attempts >= job.max_attempts")
			case market.StatusProcessing:
				parts = append(parts, "(job.status IN ('queued','running') OR (job.status='failed' AND job.attempts < job.max_attempts))")
			default:
				parts = append(parts, "(job.status = 'succeeded' AND jd.status = "+placeholder(status)+")")
			}
		}
		query += " AND (" + strings.Join(parts, " OR ") + ")"
	}
	likeCondition := func(value string, expression string) {
		if value == "" {
			return
		}
		query += " AND " + expression + " ILIKE " + placeholder("%"+value+"%")
	}
	likeCondition(filter.Company, "COALESCE(jd.company,'')")
	if filter.Ability != "" {
		query += ` AND EXISTS (
			SELECT 1 FROM job_description_ability_requirement_options option
			JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
			JOIN abilities ability ON ability.id=option.ability_id
			WHERE requirement.job_description_id=jd.id AND ability.name ILIKE ` + placeholder("%"+filter.Ability+"%") + ")"
	}
	if filter.Category != "" {
		value := placeholder("%" + filter.Category + "%")
		query += " AND (COALESCE(jd.primary_category,'') ILIKE " + value + " OR COALESCE(jd.secondary_category,'') ILIKE " + value + ")"
	}
	if filter.Query != "" {
		value := placeholder("%" + filter.Query + "%")
		query += ` AND (
			COALESCE(jd.title,'') ILIKE ` + value + ` OR COALESCE(jd.company,'') ILIKE ` + value + `
			OR COALESCE(jd.primary_category,'') ILIKE ` + value + ` OR COALESCE(jd.secondary_category,'') ILIKE ` + value + `
			OR EXISTS (
				SELECT 1 FROM job_description_ability_requirement_options option
				JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
				JOIN abilities ability ON ability.id=option.ability_id
				WHERE requirement.job_description_id=jd.id AND ability.name ILIKE ` + value + `
			)
		)`
	}
	query += " ORDER BY jd.created_at DESC"

	rows, err := r.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list job descriptions: %w", err)
	}
	defer rows.Close()

	result := make([]market.JobDescription, 0)
	for rows.Next() {
		item, err := scanJDWithJobStatus(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job descriptions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := r.loadJDDetails(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *MarketRepository) Delete(ctx context.Context, userID, jdID uuid.UUID) error {
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete jd transaction: %w", err)
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `DELETE FROM job_descriptions WHERE id=$1 AND user_id=$2`, jdID, userID)
	if err != nil {
		return fmt.Errorf("delete jd: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return market.ErrNotFound
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM ability_review_requests request WHERE request.review_type='ability' AND request.status<>'succeeded' AND NOT EXISTS(SELECT 1 FROM job_description_ability_requirement_options option WHERE option.review_request_id=request.id)`); err != nil {
		return fmt.Errorf("clear orphaned ability reviews: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit delete jd: %w", err)
	}
	return nil
}

func (r *MarketRepository) FindByID(ctx context.Context, userID, jdID uuid.UUID) (market.JobDescription, error) {
	const query = `
		SELECT jd.id, jd.target_id, COALESCE(jd.title, ''), COALESCE(jd.company, ''), jd.status,
		       COALESCE(jd.primary_category, ''), COALESCE(jd.secondary_category, ''),
		       COALESCE(jd.relevance_reason, ''), COALESCE(jd.conditions, ''), jd.raw_text,
		       jd.created_at, jd.updated_at,
		       COALESCE(CASE WHEN job.status='failed' AND job.attempts < job.max_attempts THEN 'queued' ELSE job.status END, ''),
		       jd.employment_type, jd.responsibilities, jd.ability_mentions,
		       COALESCE(jd.analysis_provider, ''), COALESCE(jd.analysis_model, ''),
		       COALESCE(jd.analysis_prompt_version, ''), COALESCE(job.last_error, ''),
		       COALESCE(jd.document_type, ''), jd.validation_status,
		       COALESCE(jd.validation_reason, '')
		FROM job_descriptions jd
		LEFT JOIN analysis_jobs job
		  ON job.job_description_id = jd.id AND job.job_type = 'jd_analysis'
		WHERE jd.user_id = $1 AND jd.id = $2`

	result, err := scanJDWithJobStatus(r.database.QueryRowContext(ctx, query, userID, jdID))
	if errors.Is(err, sql.ErrNoRows) {
		return market.JobDescription{}, market.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	items := []market.JobDescription{result}
	err = r.loadJDDetails(ctx, items)
	return items[0], err
}

func (r *MarketRepository) RetryAbilityReviews(ctx context.Context, userID, jdID uuid.UUID) (market.JobDescription, error) {
	var userCalls, globalCalls int
	if err := r.database.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE user_id=$1),COUNT(*) FROM platform_model_usage WHERE purpose='ability_review' AND created_at >= date_trunc('day',NOW() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai'`, userID).Scan(&userCalls, &globalCalls); err != nil {
		return market.JobDescription{}, err
	}
	if quotaReached(userCalls, r.quota.UserDaily) || quotaReached(globalCalls, r.quota.GlobalDaily) {
		var next time.Time
		if err := r.database.QueryRowContext(ctx, `SELECT (date_trunc('day',NOW() AT TIME ZONE 'Asia/Shanghai')+INTERVAL '1 day') AT TIME ZONE 'Asia/Shanghai'`).Scan(&next); err != nil {
			return market.JobDescription{}, err
		}
		return market.JobDescription{}, &market.AbilityReviewQuotaError{NextAvailableAt: next}
	}
	result, err := r.database.ExecContext(ctx, `UPDATE ability_review_requests request SET status='queued',attempts=0,next_attempt_at=NOW(),last_error=NULL,lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,updated_at=NOW()
		WHERE request.status='failed' AND EXISTS(SELECT 1 FROM job_description_ability_requirement_options option JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id JOIN job_descriptions jd ON jd.id=requirement.job_description_id WHERE option.review_request_id=request.id AND jd.id=$1 AND jd.user_id=$2)`, jdID, userID)
	if err != nil {
		return market.JobDescription{}, fmt.Errorf("retry ability reviews: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		var exists bool
		_ = r.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_descriptions WHERE id=$1 AND user_id=$2)`, jdID, userID).Scan(&exists)
		if !exists {
			return market.JobDescription{}, market.ErrNotFound
		}
	}
	_, _ = r.database.ExecContext(ctx, `UPDATE job_description_ability_requirement_options option SET resolution_status='pending_review' FROM job_description_ability_requirements requirement WHERE option.requirement_id=requirement.id AND requirement.job_description_id=$1 AND option.review_request_id IS NOT NULL AND option.ability_id IS NULL`, jdID)
	return r.FindByID(ctx, userID, jdID)
}

func (r *MarketRepository) RetryAbilityGrading(ctx context.Context, userID, jdID uuid.UUID) (market.JobDescription, error) {
	result, err := r.database.ExecContext(ctx, `UPDATE jd_ability_level_jobs job
		SET status='queued',attempts=0,next_attempt_at=NOW(),last_error=NULL,
		    lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,updated_at=NOW()
		FROM job_descriptions jd
		WHERE job.job_description_id=jd.id AND jd.id=$1 AND jd.user_id=$2
		  AND jd.status='included' AND jd.validation_status='valid' AND job.status='failed'`, jdID, userID)
	if err != nil {
		return market.JobDescription{}, fmt.Errorf("retry JD ability grading: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		var exists bool
		if err := r.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_descriptions WHERE id=$1 AND user_id=$2)`, jdID, userID).Scan(&exists); err != nil {
			return market.JobDescription{}, err
		}
		if !exists {
			return market.JobDescription{}, market.ErrNotFound
		}
		return market.JobDescription{}, market.ErrPrecondition
	}
	return r.FindByID(ctx, userID, jdID)
}

func (r *MarketRepository) RetryAnalysis(ctx context.Context, userID, jdID uuid.UUID) (market.JobDescription, error) {
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return market.JobDescription{}, fmt.Errorf("begin retry transaction: %w", err)
	}
	defer transaction.Rollback()

	if err = lockUserMutationTx(ctx, transaction.Tx, userID); err != nil {
		return market.JobDescription{}, err
	}
	if err = seedHistoricalJDStages(ctx, transaction.Tx, userID, jdID); err != nil {
		return market.JobDescription{}, err
	}

	var preservePreviousResult bool
	if _, err = transaction.ExecContext(ctx, `UPDATE jd_analysis_stages SET attempts=0,retryable=TRUE WHERE job_description_id=$1 AND status='failed' AND EXISTS(SELECT 1 FROM job_descriptions WHERE id=$1 AND user_id=$2)`, jdID, userID); err != nil {
		return market.JobDescription{}, err
	}
	err = transaction.QueryRowContext(ctx, `
		UPDATE analysis_jobs job
		SET status = 'queued', attempts = 0, next_attempt_at = NOW(), locked_at = NULL,
		    lease_token = NULL, heartbeat_at = NULL, lease_expires_at = NULL,
		    last_error = NULL, preserve_previous_result = job.preserve_previous_result OR (jd.validation_status='valid' AND jd.status IN ('included','reference','excluded')), updated_at = NOW()
		FROM job_descriptions jd
		WHERE job.job_description_id = jd.id AND jd.id = $1 AND jd.user_id = $2
		  AND job.job_type = 'jd_analysis'
		RETURNING job.preserve_previous_result`, jdID, userID).Scan(&preservePreviousResult)
	if errors.Is(err, sql.ErrNoRows) {
		return market.JobDescription{}, market.ErrNotFound
	}
	if err != nil {
		return market.JobDescription{}, fmt.Errorf("requeue jd analysis: %w", err)
	}
	if !preservePreviousResult {
		if _, err := transaction.ExecContext(ctx, `
		UPDATE job_descriptions
		SET status = 'processing', relevance_reason = NULL,
		    validation_status = CASE WHEN EXISTS(SELECT 1 FROM jd_analysis_stages stage WHERE stage.job_description_id=job_descriptions.id AND stage.source_hash=encode(digest(job_descriptions.raw_text,'sha256'),'hex') AND stage.stage='extraction' AND stage.status='succeeded') THEN validation_status ELSE 'pending' END,
		    updated_at = NOW()
		WHERE id = $1 AND user_id = $2`, jdID, userID); err != nil {
			return market.JobDescription{}, fmt.Errorf("reset jd status: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return market.JobDescription{}, fmt.Errorf("commit retry transaction: %w", err)
	}
	return r.FindByID(ctx, userID, jdID)
}

func (r *MarketRepository) ProfileByTarget(ctx context.Context, userID, targetID uuid.UUID) (market.Profile, error) {
	profile := market.Profile{
		RequiredJDCount: readiness.RequiredJDCount,
		Abilities:       make([]market.AbilitySummary, 0),
	}
	if err := r.database.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM job_descriptions
		WHERE user_id = $1 AND target_id = $2
		  AND status = 'included' AND validation_status = 'valid'`, userID, targetID,
	).Scan(&profile.IncludedJDCount); err != nil {
		return market.Profile{}, fmt.Errorf("count included JDs: %w", err)
	}
	profile.Complete = profile.IncludedJDCount >= profile.RequiredJDCount

	rows, err := r.database.QueryContext(ctx, `
		SELECT ability.id, ability.name, category.name,
		       jd.id, COALESCE(jd.title, '未明确岗位'), requirement.operator,
		       requirement.evidence, option.qualifier
		FROM job_description_ability_requirement_options option
		JOIN job_description_ability_requirements requirement ON requirement.id = option.requirement_id
		JOIN job_descriptions jd ON jd.id = requirement.job_description_id
		JOIN abilities ability ON ability.id = option.ability_id
		JOIN ability_categories category ON category.id = ability.category_id
		WHERE jd.user_id = $1 AND jd.target_id = $2
		  AND jd.status = 'included' AND jd.validation_status = 'valid'
		ORDER BY category.sort_order, ability.sort_order, jd.created_at,
		         requirement.sort_order, option.sort_order`, userID, targetID)
	if err != nil {
		return market.Profile{}, fmt.Errorf("load market abilities: %w", err)
	}
	defer rows.Close()
	type accumulator struct {
		index   int
		seenJDs map[uuid.UUID]struct{}
	}
	indexes := make(map[uuid.UUID]*accumulator)
	for rows.Next() {
		var abilityID, jdID uuid.UUID
		var abilityName, categoryName, jdTitle, operator, evidence, qualifier string
		if err := rows.Scan(&abilityID, &abilityName, &categoryName, &jdID, &jdTitle, &operator, &evidence, &qualifier); err != nil {
			return market.Profile{}, fmt.Errorf("scan market ability: %w", err)
		}
		current, exists := indexes[abilityID]
		if !exists {
			current = &accumulator{index: len(profile.Abilities), seenJDs: make(map[uuid.UUID]struct{})}
			indexes[abilityID] = current
			profile.Abilities = append(profile.Abilities, market.AbilitySummary{
				AbilityID: abilityID, Name: abilityName, Category: categoryName, Evidences: []market.AbilityEvidence{},
			})
		}
		if _, exists := current.seenJDs[jdID]; !exists && (operator == "single" || operator == "any_of") {
			current.seenJDs[jdID] = struct{}{}
			profile.Abilities[current.index].CoveredJDCount++
		}
		profile.Abilities[current.index].Evidences = append(profile.Abilities[current.index].Evidences, market.AbilityEvidence{
			JobDescriptionID: jdID, JobTitle: jdTitle, Evidence: evidence, Qualifier: qualifier,
		})
	}
	if err := rows.Err(); err != nil {
		return market.Profile{}, fmt.Errorf("iterate market abilities: %w", err)
	}
	if err := rows.Close(); err != nil {
		return market.Profile{}, fmt.Errorf("close market abilities: %w", err)
	}

	if err := r.database.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER(WHERE level_job.status IN ('queued','running') OR (level_job.status='failed' AND level_job.attempts<level_job.max_attempts)
		    OR level_job.input_fingerprint<>jd_ability_grading_fingerprint(jd.id,$3)),
		COUNT(*) FILTER(WHERE level_job.status='failed' AND level_job.attempts>=level_job.max_attempts)
		FROM jd_ability_level_jobs level_job
		JOIN job_descriptions jd ON jd.id=level_job.job_description_id
		WHERE jd.user_id=$1 AND jd.target_id=$2 AND jd.status='included' AND jd.validation_status='valid'`, userID, targetID, abilitygrading.PromptVersion,
	).Scan(&profile.AbilityGradingPendingCount, &profile.AbilityGradingFailedCount); err != nil {
		return market.Profile{}, fmt.Errorf("count JD ability grading jobs: %w", err)
	}

	type abilityLevelState struct {
		required  map[uuid.UUID]market.LevelEvidence
		preferred map[uuid.UUID]market.LevelEvidence
		pending   int
		failed    int
	}
	states := make(map[uuid.UUID]*abilityLevelState)
	assessmentRows, err := r.database.QueryContext(ctx, `SELECT assessment.ability_id,jd.id,COALESCE(jd.title,'未明确岗位'),
		assessment.level,assessment.source,assessment.requirement_kind,assessment.evidence_quote,
		assessment.reason,assessment.confidence
		FROM jd_ability_level_assessments assessment
		JOIN job_descriptions jd ON jd.id=assessment.job_description_id
		JOIN jd_ability_level_jobs level_job ON level_job.job_description_id=jd.id
		WHERE jd.user_id=$1 AND jd.target_id=$2 AND jd.status='included' AND jd.validation_status='valid'
		  AND level_job.result_fingerprint=jd_ability_grading_fingerprint(jd.id,$3)
		ORDER BY jd.created_at,assessment.created_at`, userID, targetID, abilitygrading.PromptVersion)
	if err != nil {
		return market.Profile{}, fmt.Errorf("load market ability levels: %w", err)
	}
	for assessmentRows.Next() {
		var abilityID uuid.UUID
		var evidence market.LevelEvidence
		if err := assessmentRows.Scan(&abilityID, &evidence.JobDescriptionID, &evidence.JobTitle, &evidence.Level,
			&evidence.Source, &evidence.RequirementKind, &evidence.Evidence, &evidence.Reason, &evidence.Confidence); err != nil {
			assessmentRows.Close()
			return market.Profile{}, fmt.Errorf("scan market ability level: %w", err)
		}
		state := states[abilityID]
		if state == nil {
			state = &abilityLevelState{required: map[uuid.UUID]market.LevelEvidence{}, preferred: map[uuid.UUID]market.LevelEvidence{}}
			states[abilityID] = state
		}
		bucket := state.required
		if evidence.RequirementKind == "preferred" {
			bucket = state.preferred
		}
		previous, exists := bucket[evidence.JobDescriptionID]
		if !exists || evidence.Level > previous.Level || (evidence.Level == previous.Level && evidence.Source == "explicit" && previous.Source != "explicit") {
			bucket[evidence.JobDescriptionID] = evidence
		}
	}
	if err := assessmentRows.Err(); err != nil {
		assessmentRows.Close()
		return market.Profile{}, fmt.Errorf("iterate market ability levels: %w", err)
	}
	assessmentRows.Close()

	statusRows, err := r.database.QueryContext(ctx, `SELECT option.ability_id,
		COUNT(DISTINCT jd.id) FILTER(WHERE level_job.status IN ('queued','running') OR (level_job.status='failed' AND level_job.attempts<level_job.max_attempts)
		    OR level_job.input_fingerprint<>jd_ability_grading_fingerprint(jd.id,$3)),
		COUNT(DISTINCT jd.id) FILTER(WHERE level_job.status='failed' AND level_job.attempts>=level_job.max_attempts)
		FROM job_description_ability_requirement_options option
		JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
		JOIN job_descriptions jd ON jd.id=requirement.job_description_id
		LEFT JOIN jd_ability_level_jobs level_job ON level_job.job_description_id=jd.id
		WHERE jd.user_id=$1 AND jd.target_id=$2 AND jd.status='included' AND jd.validation_status='valid'
		  AND option.ability_id IS NOT NULL
		GROUP BY option.ability_id`, userID, targetID, abilitygrading.PromptVersion)
	if err != nil {
		return market.Profile{}, fmt.Errorf("load market ability grading status: %w", err)
	}
	for statusRows.Next() {
		var abilityID uuid.UUID
		var pending, failed int
		if err := statusRows.Scan(&abilityID, &pending, &failed); err != nil {
			statusRows.Close()
			return market.Profile{}, err
		}
		state := states[abilityID]
		if state == nil {
			state = &abilityLevelState{required: map[uuid.UUID]market.LevelEvidence{}, preferred: map[uuid.UUID]market.LevelEvidence{}}
			states[abilityID] = state
		}
		state.pending, state.failed = pending, failed
	}
	statusRows.Close()

	for abilityID, accumulator := range indexes {
		state := states[abilityID]
		if state == nil {
			state = &abilityLevelState{required: map[uuid.UUID]market.LevelEvidence{}, preferred: map[uuid.UUID]market.LevelEvidence{}}
		}
		status := "ready"
		if state.pending > 0 {
			status = "processing"
		} else if state.failed > 0 {
			status = "failed"
		} else if len(state.required) == 0 && len(state.preferred) == 0 {
			status = "pending"
		}
		required := make([]market.LevelEvidence, 0, len(state.required))
		for _, evidence := range state.required {
			required = append(required, evidence)
		}
		preferred := make([]market.LevelEvidence, 0, len(state.preferred))
		for _, evidence := range state.preferred {
			preferred = append(preferred, evidence)
		}
		lessEvidence := func(a, b market.LevelEvidence) bool {
			if a.JobTitle != b.JobTitle {
				return a.JobTitle < b.JobTitle
			}
			return a.JobDescriptionID.String() < b.JobDescriptionID.String()
		}
		sort.Slice(required, func(i, j int) bool { return lessEvidence(required[i], required[j]) })
		sort.Slice(preferred, func(i, j int) bool { return lessEvidence(preferred[i], preferred[j]) })
		profile.Abilities[accumulator.index].LevelSummary = market.BuildLevelSummary(required, status)
		profile.Abilities[accumulator.index].PreferredLevelSummary = market.BuildLevelSummary(preferred, status)
		profile.Abilities[accumulator.index].TargetLevel = profile.Abilities[accumulator.index].LevelSummary.RecommendedLevel
	}
	return profile, nil
}

func scanJDWithJobStatus(row rowScanner) (market.JobDescription, error) {
	var result market.JobDescription
	var responsibilities []byte
	var abilityMentions []byte
	if err := row.Scan(
		&result.ID,
		&result.TargetID,
		&result.Title,
		&result.Company,
		&result.Status,
		&result.PrimaryCategory,
		&result.SecondaryCategory,
		&result.Reason,
		&result.Conditions,
		&result.RawText,
		&result.CreatedAt,
		&result.UpdatedAt,
		&result.JobStatus,
		&result.EmploymentType,
		&responsibilities,
		&abilityMentions,
		&result.AnalysisProvider,
		&result.AnalysisModel,
		&result.AnalysisPromptVersion,
		&result.JobErrorCode,
		&result.DocumentType,
		&result.ValidationStatus,
		&result.ValidationReason,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return market.JobDescription{}, market.ErrNotFound
		}
		return market.JobDescription{}, fmt.Errorf("scan job description: %w", err)
	}
	if err := json.Unmarshal(responsibilities, &result.Responsibilities); err != nil {
		return market.JobDescription{}, fmt.Errorf("decode responsibilities: %w", err)
	}
	if err := json.Unmarshal(abilityMentions, &result.AbilityMentions); err != nil {
		return market.JobDescription{}, fmt.Errorf("decode ability mentions: %w", err)
	}
	return result, nil
}
