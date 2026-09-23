package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilitygrading"
	"github.com/google/uuid"
)

type AbilityGradingRepository struct {
	database *sql.DB
}

func NewAbilityGradingRepository(database *sql.DB) *AbilityGradingRepository {
	return &AbilityGradingRepository{database: database}
}

func (r *AbilityGradingRepository) SetConfigurationBlocked(ctx context.Context, blocked bool) error {
	if blocked {
		_, err := r.database.ExecContext(ctx, `UPDATE jd_ability_level_jobs
			SET status='queued',next_attempt_at=NOW()+INTERVAL '1 day',last_error='platform_model_not_configured',
			    lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,updated_at=NOW()
			WHERE status IN ('queued','failed') AND attempts<max_attempts`)
		return err
	}
	_, err := r.database.ExecContext(ctx, `UPDATE jd_ability_level_jobs
		SET status='queued',next_attempt_at=NOW(),last_error=NULL,updated_at=NOW()
		WHERE last_error='platform_model_not_configured'`)
	return err
}

func (r *AbilityGradingRepository) RecoverExpired(ctx context.Context) (int64, error) {
	result, err := r.database.ExecContext(ctx, `UPDATE jd_ability_level_jobs
		SET status='failed',next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,
		    lease_expires_at=NULL,last_error='worker_interrupted',updated_at=NOW()
		WHERE status='running' AND lease_expires_at<NOW()`)
	if err != nil {
		return 0, fmt.Errorf("recover JD ability grading jobs: %w", err)
	}
	return result.RowsAffected()
}

func (r *AbilityGradingRepository) Claim(ctx context.Context, lease time.Duration) (abilitygrading.Input, error) {
	// A target change can make a previously queued job irrelevant. Finish those
	// jobs without deleting their last successful assessments; a later target
	// change back to included will explicitly enqueue a fresh run.
	_, _ = r.database.ExecContext(ctx, `UPDATE jd_ability_level_jobs job
		SET status='succeeded',last_error='not_in_market',updated_at=NOW(),completed_at=NOW()
		FROM job_descriptions jd
		WHERE jd.id=job.job_description_id AND jd.status<>'included'
		  AND job.status IN ('queued','failed')`)

	token := uuid.New()
	var input abilitygrading.Input
	var responsibilities []byte
	err := r.database.QueryRowContext(ctx, `WITH candidate AS (
		SELECT job.id FROM jd_ability_level_jobs job
		JOIN job_descriptions jd ON jd.id=job.job_description_id
		WHERE job.status IN ('queued','failed') AND job.attempts<job.max_attempts
		  AND job.next_attempt_at<=NOW() AND jd.status='included' AND jd.validation_status='valid'
		  AND NOT EXISTS(
			SELECT 1 FROM job_description_ability_requirements requirement
			JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
			WHERE requirement.job_description_id=jd.id AND option.resolution_status='pending_review'
		  )
		ORDER BY job.created_at FOR UPDATE OF job SKIP LOCKED LIMIT 1
	) UPDATE jd_ability_level_jobs job
	SET status='running',attempts=job.attempts+1,lease_token=$1,heartbeat_at=NOW(),
	    lease_expires_at=NOW()+($2*INTERVAL '1 millisecond'),updated_at=NOW()
	FROM candidate,job_descriptions jd
	WHERE job.id=candidate.id AND jd.id=job.job_description_id
	RETURNING job.id,job.user_id,job.target_id,job.job_description_id,job.lease_token,
	          job.attempts,job.max_attempts,COALESCE(jd.title,''),jd.responsibilities,jd.raw_text`, token, lease.Milliseconds()).Scan(
		&input.ID, &input.UserID, &input.TargetID, &input.JobDescriptionID, &input.LeaseToken,
		&input.Attempts, &input.MaxAttempts, &input.Title, &responsibilities, &input.RawText)
	if errors.Is(err, sql.ErrNoRows) {
		return input, abilitygrading.ErrNoJob
	}
	if err != nil {
		return input, fmt.Errorf("claim JD ability grading job: %w", err)
	}
	if err := json.Unmarshal(responsibilities, &input.Responsibilities); err != nil {
		return input, fmt.Errorf("decode JD responsibilities for grading: %w", err)
	}
	input.Abilities, err = r.loadAbilities(ctx, input.JobDescriptionID)
	if err != nil {
		return input, err
	}
	if len(input.Abilities) == 0 {
		if err := r.Complete(ctx, input, abilitygrading.Result{
			Assessments: []abilitygrading.Assessment{}, PromptVersion: abilitygrading.PromptVersion,
		}); err != nil {
			return input, err
		}
		return input, abilitygrading.ErrNoJob
	}
	return input, nil
}

func (r *AbilityGradingRepository) loadAbilities(ctx context.Context, jdID uuid.UUID) ([]abilitygrading.Ability, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT ability.id,ability.code,ability.name,
		requirement.requirement_kind,requirement.operator,requirement.required_count,requirement.evidence,option.qualifier
		FROM job_description_ability_requirement_options option
		JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
		JOIN abilities ability ON ability.id=option.ability_id AND ability.is_active
		WHERE requirement.job_description_id=$1
		ORDER BY ability.sort_order,requirement.sort_order,option.sort_order`, jdID)
	if err != nil {
		return nil, fmt.Errorf("load abilities for JD grading: %w", err)
	}
	type item struct {
		id    uuid.UUID
		value abilitygrading.Ability
		seen  map[string]struct{}
	}
	values := make([]item, 0)
	positions := map[uuid.UUID]int{}
	for rows.Next() {
		var abilityID uuid.UUID
		var code, name, kind, operator, quote, qualifier string
		var requiredCount int
		if err := rows.Scan(&abilityID, &code, &name, &kind, &operator, &requiredCount, &quote, &qualifier); err != nil {
			rows.Close()
			return nil, err
		}
		position, ok := positions[abilityID]
		if !ok {
			position = len(values)
			positions[abilityID] = position
			values = append(values, item{id: abilityID, value: abilitygrading.Ability{Code: code, Name: name, Levels: []abilitygrading.LevelDefinition{}, Evidences: []abilitygrading.Evidence{}}, seen: map[string]struct{}{}})
		}
		key := kind + "\x00" + quote + "\x00" + qualifier
		if _, duplicate := values[position].seen[key]; duplicate {
			continue
		}
		values[position].seen[key] = struct{}{}
		values[position].value.Evidences = append(values[position].value.Evidences, abilitygrading.Evidence{
			RequirementKind: kind, Operator: operator, RequiredCount: requiredCount, Quote: quote, Qualifier: qualifier,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return []abilitygrading.Ability{}, nil
	}
	ids := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.id)
	}
	levelRows, err := r.database.QueryContext(ctx, `SELECT ability_id,level,description
		FROM ability_levels WHERE ability_id=ANY($1) ORDER BY ability_id,level`, uuidArray(ids))
	if err != nil {
		return nil, fmt.Errorf("load ability level standards: %w", err)
	}
	for levelRows.Next() {
		var abilityID uuid.UUID
		var level abilitygrading.LevelDefinition
		if err := levelRows.Scan(&abilityID, &level.Level, &level.Description); err != nil {
			levelRows.Close()
			return nil, err
		}
		if position, ok := positions[abilityID]; ok {
			values[position].value.Levels = append(values[position].value.Levels, level)
		}
	}
	if err := levelRows.Close(); err != nil {
		return nil, err
	}
	result := make([]abilitygrading.Ability, 0, len(values))
	for _, value := range values {
		if len(value.value.Levels) != 6 {
			return nil, fmt.Errorf("ability %s does not have a complete L0-L5 standard", value.value.Code)
		}
		result = append(result, value.value)
	}
	return result, nil
}

func (r *AbilityGradingRepository) Heartbeat(ctx context.Context, input abilitygrading.Input, lease time.Duration) error {
	result, err := r.database.ExecContext(ctx, `UPDATE jd_ability_level_jobs
		SET heartbeat_at=NOW(),lease_expires_at=NOW()+($3*INTERVAL '1 millisecond'),updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, input.ID, input.LeaseToken, lease.Milliseconds())
	if err != nil {
		return err
	}
	return requireGradingLease(result)
}

func (r *AbilityGradingRepository) Complete(ctx context.Context, input abilitygrading.Input, result abilitygrading.Result) error {
	result.Assessments = normalizeAssessmentKinds(input, result.Assessments)
	if err := validateGradingResult(input, result); err != nil {
		return r.Fail(ctx, input, "model_invalid_response", input.Attempts < input.MaxAttempts)
	}
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	updated, err := tx.ExecContext(ctx, `UPDATE jd_ability_level_jobs
		SET status='succeeded',last_error=NULL,lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,
		    provider=$3,model=$4,prompt_version=$5,provider_request_id=$6,input_tokens=$7,output_tokens=$8,
		    completed_at=NOW(),updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, input.ID, input.LeaseToken,
		result.Provider, result.Model, result.PromptVersion, result.ProviderRequestID, result.InputTokens, result.OutputTokens)
	if err != nil {
		return err
	}
	if err := requireGradingLease(updated); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM jd_ability_level_assessments WHERE job_description_id=$1`, input.JobDescriptionID); err != nil {
		return err
	}
	for _, assessment := range result.Assessments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO jd_ability_level_assessments(
			job_description_id,ability_id,level,source,requirement_kind,evidence_quote,reason,
			confidence,level_standard_version,prompt_version)
			SELECT $1,ability.id,$3,$4,$5,$6,$7,$8,1,$9 FROM abilities ability
			WHERE ability.code=$2 AND ability.is_active`, input.JobDescriptionID, assessment.AbilityCode,
			assessment.Level, assessment.Source, assessment.RequirementKind, assessment.EvidenceQuote,
			assessment.Reason, assessment.Confidence, abilitygrading.PromptVersion); err != nil {
			return fmt.Errorf("save JD ability assessment: %w", err)
		}
	}
	return tx.Commit()
}

func normalizeAssessmentKinds(input abilitygrading.Input, assessments []abilitygrading.Assessment) []abilitygrading.Assessment {
	abilities := make(map[string]abilitygrading.Ability, len(input.Abilities))
	for _, ability := range input.Abilities {
		abilities[ability.Code] = ability
	}
	for index := range assessments {
		assessment := &assessments[index]
		ability, ok := abilities[assessment.AbilityCode]
		if !ok {
			continue
		}
		kind := "unspecified"
		for _, evidence := range ability.Evidences {
			if !strings.Contains(evidence.Quote, assessment.EvidenceQuote) && !strings.Contains(assessment.EvidenceQuote, evidence.Quote) {
				continue
			}
			if evidence.RequirementKind == "required" || evidence.RequirementKind == "preferred" {
				kind = evidence.RequirementKind
				break
			}
			if hasPreferredSignal(evidence.Quote) || hasBonusHeading(evidenceContext(input.RawText, evidence.Quote)) {
				kind = "preferred"
			}
		}
		assessment.RequirementKind = kind
	}
	return assessments
}

func evidenceContext(rawText, quote string) string {
	position := strings.Index(rawText, quote)
	if position < 0 {
		return quote
	}
	start := position - 100
	if start < 0 {
		start = 0
	}
	end := position + len(quote) + 40
	if end > len(rawText) {
		end = len(rawText)
	}
	return rawText[start:end]
}

func hasPreferredSignal(value string) bool {
	value = strings.ToLower(value)
	for _, signal := range []string{"加分", "优先", "更佳", "bonus", "preferred", "a plus"} {
		if strings.Contains(value, signal) {
			return true
		}
	}
	return false
}

func hasBonusHeading(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "加分项") || strings.Contains(value, "bonus item") || strings.Contains(value, "bonus requirement")
}

func (r *AbilityGradingRepository) Fail(ctx context.Context, input abilitygrading.Input, code string, retryable bool) error {
	final := !retryable || input.Attempts >= input.MaxAttempts
	attempts := input.Attempts
	if final {
		attempts = input.MaxAttempts
	}
	next := time.Now().Add(time.Duration(10*(1<<max(input.Attempts-1, 0))) * time.Second)
	result, err := r.database.ExecContext(ctx, `UPDATE jd_ability_level_jobs
		SET status='failed',attempts=$3,next_attempt_at=$4,last_error=$5,lease_token=NULL,
		    heartbeat_at=NULL,lease_expires_at=NULL,updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, input.ID, input.LeaseToken, attempts, next, truncateErrorCode(code))
	if err != nil {
		return err
	}
	return requireGradingLease(result)
}

func validateGradingResult(input abilitygrading.Input, result abilitygrading.Result) error {
	abilities := make(map[string]abilitygrading.Ability, len(input.Abilities))
	for _, ability := range input.Abilities {
		abilities[ability.Code] = ability
	}
	if len(input.Abilities) > 0 && len(result.Assessments) == 0 {
		return errors.New("missing ability assessments")
	}
	seen := make(map[string]struct{})
	seenAbility := make(map[string]struct{})
	for _, assessment := range result.Assessments {
		ability, ok := abilities[assessment.AbilityCode]
		if !ok || assessment.Level < 1 || assessment.Level > 5 {
			return errors.New("assessment refers to an unknown ability or level")
		}
		if assessment.Source != "explicit" && assessment.Source != "inferred" {
			return errors.New("assessment has an invalid source")
		}
		if assessment.RequirementKind != "required" && assessment.RequirementKind != "preferred" && assessment.RequirementKind != "unspecified" {
			return errors.New("assessment has an invalid requirement kind")
		}
		assessment.EvidenceQuote = strings.TrimSpace(assessment.EvidenceQuote)
		assessment.Reason = strings.TrimSpace(assessment.Reason)
		if assessment.EvidenceQuote == "" || !strings.Contains(input.RawText, assessment.EvidenceQuote) || assessment.Reason == "" ||
			len([]rune(assessment.Reason)) > 800 || assessment.Confidence < 0 || assessment.Confidence > 1 {
			return errors.New("assessment is missing valid evidence, reason, or confidence")
		}
		evidenceMatches := false
		for _, evidence := range ability.Evidences {
			if strings.Contains(evidence.Quote, assessment.EvidenceQuote) || strings.Contains(assessment.EvidenceQuote, evidence.Quote) {
				evidenceMatches = true
				break
			}
		}
		if !evidenceMatches {
			return errors.New("assessment evidence does not belong to the ability")
		}
		key := assessment.AbilityCode + "\x00" + assessment.RequirementKind
		if _, duplicate := seen[key]; duplicate {
			return errors.New("duplicate assessment for ability and requirement kind")
		}
		seen[key] = struct{}{}
		seenAbility[assessment.AbilityCode] = struct{}{}
	}
	if len(seenAbility) != len(input.Abilities) {
		return errors.New("not every ability was assessed")
	}
	return nil
}

func requireGradingLease(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return abilitygrading.ErrLeaseLost
	}
	return nil
}

func truncateErrorCode(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 160 {
		return value[:160]
	}
	return value
}

func enqueueJDAbilityGrading(ctx context.Context, tx *sql.Tx, userID, targetID, jdID uuid.UUID) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO jd_ability_level_jobs(
		user_id,target_id,job_description_id,status,attempts,next_attempt_at)
	SELECT $1,$2,$3,'queued',0,NOW()
	WHERE EXISTS(
		SELECT 1 FROM job_description_ability_requirements requirement
		JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
		WHERE requirement.job_description_id=$3 AND option.ability_id IS NOT NULL
	)
	ON CONFLICT(job_description_id) DO UPDATE SET
		user_id=EXCLUDED.user_id,target_id=EXCLUDED.target_id,status='queued',attempts=0,next_attempt_at=NOW(),
		lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error=NULL,completed_at=NULL,updated_at=NOW()`, userID, targetID, jdID)
	return err
}
