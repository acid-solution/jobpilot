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

	"github.com/LeoninCS/jobpilot-next/backend/internal/market"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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

func (r *MarketRepository) CreateWithAnalysisJob(
	ctx context.Context,
	userID uuid.UUID,
	targetID uuid.UUID,
	rawText string,
	rawTextHash string,
) (market.JobDescription, error) {
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return market.JobDescription{}, fmt.Errorf("begin jd transaction: %w", err)
	}
	defer transaction.Rollback()

	jdID := uuid.New()
	jobID := uuid.New()
	const insertJD = `
		INSERT INTO job_descriptions (id, user_id, target_id, raw_text, raw_text_hash, status)
		VALUES ($1, $2, $3, $4, $5, 'processing')
		RETURNING id, target_id, COALESCE(title, ''), COALESCE(company, ''), status,
		          COALESCE(primary_category, ''), COALESCE(secondary_category, ''),
		          COALESCE(relevance_reason, ''), COALESCE(conditions, ''), raw_text,
		          created_at, updated_at`

	result, err := scanJD(transaction.QueryRowContext(ctx, insertJD, jdID, userID, targetID, rawText, rawTextHash), "queued")
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			if rollbackErr := transaction.Rollback(); rollbackErr != nil {
				return market.JobDescription{}, errors.Join(err, rollbackErr)
			}
			return market.JobDescription{}, r.duplicateError(ctx, userID, targetID, rawTextHash, uuid.Nil)
		}
		return market.JobDescription{}, err
	}

	const insertJob = `
		INSERT INTO analysis_jobs (
			id, user_id, target_id, job_description_id, job_type, status
		) VALUES ($1, $2, $3, $4, 'jd_analysis', 'queued')`
	if _, err := transaction.ExecContext(ctx, insertJob, jobID, userID, targetID, jdID); err != nil {
		return market.JobDescription{}, fmt.Errorf("insert jd analysis job: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return market.JobDescription{}, fmt.Errorf("commit jd transaction: %w", err)
	}
	result.ValidationStatus = "pending"
	return result, nil
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
	// The Agent transaction uses one connection. Finish this result set before
	// querying each JD's review and grading details on that connection.
	for i := range result {
		if err := r.loadAbilityReviewSummary(ctx, &result[i]); err != nil {
			return nil, err
		}
		if err := r.loadJDAbilityLevels(ctx, &result[i]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *MarketRepository) UpdateRawText(ctx context.Context, userID, jdID uuid.UUID, rawText, rawTextHash string) (market.JobDescription, error) {
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return market.JobDescription{}, fmt.Errorf("begin edit jd transaction: %w", err)
	}
	defer transaction.Rollback()

	var targetID uuid.UUID
	err = transaction.QueryRowContext(ctx, `SELECT target_id FROM job_descriptions WHERE id=$1 AND user_id=$2 FOR UPDATE`, jdID, userID).Scan(&targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return market.JobDescription{}, market.ErrNotFound
	}
	if err != nil {
		return market.JobDescription{}, fmt.Errorf("lock jd for editing: %w", err)
	}
	var existingID uuid.UUID
	err = transaction.QueryRowContext(ctx, `SELECT id FROM job_descriptions WHERE user_id=$1 AND target_id=$2 AND raw_text_hash=$3 AND id<>$4 LIMIT 1`, userID, targetID, rawTextHash, jdID).Scan(&existingID)
	if err == nil {
		return market.JobDescription{}, &market.DuplicateError{ExistingID: existingID}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return market.JobDescription{}, fmt.Errorf("check edited jd duplicate: %w", err)
	}

	if _, err = transaction.ExecContext(ctx, `DELETE FROM job_description_classifications WHERE job_description_id=$1`, jdID); err != nil {
		return market.JobDescription{}, fmt.Errorf("clear jd classifications: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM job_description_abilities WHERE job_description_id=$1`, jdID); err != nil {
		return market.JobDescription{}, fmt.Errorf("clear jd abilities: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM job_description_ability_requirements WHERE job_description_id=$1`, jdID); err != nil {
		return market.JobDescription{}, fmt.Errorf("clear jd requirements: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM jd_ability_level_jobs WHERE job_description_id=$1`, jdID); err != nil {
		return market.JobDescription{}, fmt.Errorf("clear JD ability grading job: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM jd_ability_level_assessments WHERE job_description_id=$1`, jdID); err != nil {
		return market.JobDescription{}, fmt.Errorf("clear JD ability levels: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM ability_review_requests request WHERE request.status<>'succeeded' AND NOT EXISTS(SELECT 1 FROM job_description_ability_requirement_options option WHERE option.review_request_id=request.id)`); err != nil {
		return market.JobDescription{}, fmt.Errorf("clear orphaned ability reviews: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `UPDATE job_descriptions SET raw_text=$3,raw_text_hash=$4,status='processing',title=NULL,company=NULL,primary_category=NULL,secondary_category=NULL,relevance_reason=NULL,conditions=NULL,employment_type='',responsibilities='[]'::jsonb,ability_mentions='[]'::jsonb,analysis_provider=NULL,analysis_model=NULL,analysis_prompt_version=NULL,document_type=NULL,validation_status='pending',validation_reason=NULL,updated_at=NOW() WHERE id=$1 AND user_id=$2`, jdID, userID, rawText, rawTextHash)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			if rollbackErr := transaction.Rollback(); rollbackErr != nil {
				return market.JobDescription{}, errors.Join(err, rollbackErr)
			}
			return market.JobDescription{}, r.duplicateError(ctx, userID, targetID, rawTextHash, jdID)
		}
		return market.JobDescription{}, fmt.Errorf("update jd text: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `UPDATE analysis_jobs SET status='queued',attempts=0,next_attempt_at=NOW(),locked_at=NULL,lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error=NULL,preserve_previous_result=FALSE,updated_at=NOW() WHERE job_description_id=$1 AND job_type='jd_analysis'`, jdID)
	if err != nil {
		return market.JobDescription{}, fmt.Errorf("requeue edited jd: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		_, err = transaction.ExecContext(ctx, `INSERT INTO analysis_jobs(id,user_id,target_id,job_description_id,job_type,status) VALUES($1,$2,$3,$4,'jd_analysis','queued')`, uuid.New(), userID, targetID, jdID)
		if err != nil {
			return market.JobDescription{}, fmt.Errorf("create edited jd job: %w", err)
		}
	}
	if err = transaction.Commit(); err != nil {
		return market.JobDescription{}, fmt.Errorf("commit edited jd: %w", err)
	}
	return r.FindByID(ctx, userID, jdID)
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
	if _, err = transaction.ExecContext(ctx, `DELETE FROM ability_review_requests request WHERE request.status<>'succeeded' AND NOT EXISTS(SELECT 1 FROM job_description_ability_requirement_options option WHERE option.review_request_id=request.id)`); err != nil {
		return fmt.Errorf("clear orphaned ability reviews: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit delete jd: %w", err)
	}
	return nil
}

func (r *MarketRepository) duplicateError(ctx context.Context, userID, targetID uuid.UUID, rawTextHash string, excludeID uuid.UUID) error {
	query := `SELECT id FROM job_descriptions WHERE user_id=$1 AND target_id=$2 AND raw_text_hash=$3`
	arguments := []any{userID, targetID, rawTextHash}
	if excludeID != uuid.Nil {
		query += ` AND id<>$4`
		arguments = append(arguments, excludeID)
	}
	query += ` LIMIT 1`
	var existingID uuid.UUID
	if err := r.database.QueryRowContext(ctx, query, arguments...).Scan(&existingID); err != nil {
		return market.ErrDuplicateJD
	}
	return &market.DuplicateError{ExistingID: existingID}
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
	if err == nil {
		err = r.loadAbilityReviewSummary(ctx, &result)
	}
	if err == nil {
		err = r.loadJDAbilityLevels(ctx, &result)
	}
	return result, err
}

func (r *MarketRepository) loadJDAbilityLevels(ctx context.Context, jd *market.JobDescription) error {
	jd.AbilityLevels = []market.JDAbilityLevel{}
	err := r.database.QueryRowContext(ctx, `SELECT COALESCE(status,'not_started'),COALESCE(last_error,'')
		FROM jd_ability_level_jobs WHERE job_description_id=$1`, jd.ID).Scan(&jd.AbilityGrading.Status, &jd.AbilityGrading.Error)
	if errors.Is(err, sql.ErrNoRows) {
		jd.AbilityGrading.Status = "not_started"
	} else if err != nil {
		return err
	}
	rows, err := r.database.QueryContext(ctx, `SELECT assessment.ability_id,ability.name,assessment.level,
		assessment.source,assessment.requirement_kind,assessment.evidence_quote,assessment.reason,assessment.confidence
		FROM jd_ability_level_assessments assessment
		JOIN abilities ability ON ability.id=assessment.ability_id
		WHERE assessment.job_description_id=$1
		ORDER BY ability.sort_order,CASE assessment.requirement_kind WHEN 'preferred' THEN 1 ELSE 0 END`, jd.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var value market.JDAbilityLevel
		if err := rows.Scan(&value.AbilityID, &value.Name, &value.Level, &value.Source, &value.RequirementKind,
			&value.Evidence, &value.Reason, &value.Confidence); err != nil {
			return err
		}
		jd.AbilityLevels = append(jd.AbilityLevels, value)
	}
	return rows.Err()
}

func (r *MarketRepository) loadAbilityReviewSummary(ctx context.Context, jd *market.JobDescription) error {
	return r.database.QueryRowContext(ctx, `SELECT
		COUNT(DISTINCT request.id) FILTER(WHERE request.status IN ('queued','running') OR (request.status='failed' AND request.attempts<request.max_attempts)),
		COUNT(DISTINCT request.id) FILTER(WHERE request.status='failed' AND request.attempts>=request.max_attempts),
		MIN(request.next_attempt_at) FILTER(WHERE request.status IN ('queued','failed') AND COALESCE(request.last_error,'')<>'platform_model_not_configured'),
		COALESCE(BOOL_OR(request.last_error='platform_model_not_configured'),FALSE)
		FROM job_description_ability_requirement_options option
		JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
		LEFT JOIN ability_review_requests request ON request.id=option.review_request_id
		WHERE requirement.job_description_id=$1`, jd.ID).Scan(&jd.AbilityReview.PendingCount, &jd.AbilityReview.FailedCount, &jd.AbilityReview.NextAttemptAt, &jd.AbilityReview.Blocked)
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

	var preservePreviousResult bool
	err = transaction.QueryRowContext(ctx, `
		UPDATE analysis_jobs job
		SET status = 'queued', attempts = 0, next_attempt_at = NOW(), locked_at = NULL,
		    lease_token = NULL, heartbeat_at = NULL, lease_expires_at = NULL,
		    last_error = NULL, updated_at = NOW()
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
		    document_type = NULL, validation_status = 'pending', validation_reason = NULL,
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
		RequiredJDCount: 10,
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
		COUNT(*) FILTER(WHERE level_job.status IN ('queued','running') OR (level_job.status='failed' AND level_job.attempts<level_job.max_attempts)),
		COUNT(*) FILTER(WHERE level_job.status='failed' AND level_job.attempts>=level_job.max_attempts)
		FROM jd_ability_level_jobs level_job
		JOIN job_descriptions jd ON jd.id=level_job.job_description_id
		WHERE jd.user_id=$1 AND jd.target_id=$2 AND jd.status='included' AND jd.validation_status='valid'`, userID, targetID,
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
		WHERE jd.user_id=$1 AND jd.target_id=$2 AND jd.status='included' AND jd.validation_status='valid'
		ORDER BY jd.created_at,assessment.created_at`, userID, targetID)
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
		COUNT(DISTINCT jd.id) FILTER(WHERE level_job.status IN ('queued','running') OR (level_job.status='failed' AND level_job.attempts<level_job.max_attempts)),
		COUNT(DISTINCT jd.id) FILTER(WHERE level_job.status='failed' AND level_job.attempts>=level_job.max_attempts)
		FROM job_description_ability_requirement_options option
		JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
		JOIN job_descriptions jd ON jd.id=requirement.job_description_id
		LEFT JOIN jd_ability_level_jobs level_job ON level_job.job_description_id=jd.id
		WHERE jd.user_id=$1 AND jd.target_id=$2 AND jd.status='included' AND jd.validation_status='valid'
		  AND option.ability_id IS NOT NULL
		GROUP BY option.ability_id`, userID, targetID)
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
		sort.Slice(required, func(i, j int) bool { return required[i].JobTitle < required[j].JobTitle })
		sort.Slice(preferred, func(i, j int) bool { return preferred[i].JobTitle < preferred[j].JobTitle })
		profile.Abilities[accumulator.index].LevelSummary = market.BuildLevelSummary(required, status)
		profile.Abilities[accumulator.index].PreferredLevelSummary = market.BuildLevelSummary(preferred, status)
		profile.Abilities[accumulator.index].TargetLevel = profile.Abilities[accumulator.index].LevelSummary.RecommendedLevel
	}
	return profile, nil
}

func scanJD(row rowScanner, jobStatus string) (market.JobDescription, error) {
	var result market.JobDescription
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
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return market.JobDescription{}, market.ErrNotFound
		}
		return market.JobDescription{}, fmt.Errorf("scan job description: %w", err)
	}
	result.JobStatus = jobStatus
	return result, nil
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
