package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityidentity"
	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/google/uuid"
)

type AbilityReviewQuotaLimits struct {
	UserDaily   int
	GlobalDaily int
}

func DefaultAbilityReviewQuotaLimits() AbilityReviewQuotaLimits {
	return AbilityReviewQuotaLimits{UserDaily: 3, GlobalDaily: 30}
}

type AbilityReviewRepository struct {
	database *sql.DB
	quota    AbilityReviewQuotaLimits
}

func NewAbilityReviewRepository(database *sql.DB, configured ...AbilityReviewQuotaLimits) *AbilityReviewRepository {
	quota := DefaultAbilityReviewQuotaLimits()
	if len(configured) > 0 {
		quota = configured[0]
	}
	return &AbilityReviewRepository{database: database, quota: quota}
}

func (r *AbilityReviewRepository) SetConfigurationBlocked(ctx context.Context, blocked bool) error {
	if blocked {
		_, err := r.database.ExecContext(ctx, `UPDATE ability_review_requests SET status='queued',next_attempt_at=NOW()+INTERVAL '1 day',last_error='platform_model_not_configured',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE status IN ('queued','failed') AND attempts<max_attempts`)
		return err
	}
	_, err := r.database.ExecContext(ctx, `UPDATE ability_review_requests SET status='queued',next_attempt_at=NOW(),last_error=NULL,updated_at=NOW() WHERE last_error='platform_model_not_configured'`)
	return err
}

func NormalizeAbilityName(value string) string {
	return abilityidentity.NormalizeName(value)
}

func (r *AbilityReviewRepository) RecoverExpired(ctx context.Context) (int64, error) {
	result, err := r.database.ExecContext(ctx, `UPDATE ability_review_requests
		SET status='failed', next_attempt_at=NOW(), lease_token=NULL, heartbeat_at=NULL,
		    lease_expires_at=NULL, last_error='worker_interrupted', updated_at=NOW()
		WHERE status='running' AND lease_expires_at < NOW()`)
	if err != nil {
		return 0, fmt.Errorf("recover ability reviews: %w", err)
	}
	return result.RowsAffected()
}

func (r *AbilityReviewRepository) Claim(ctx context.Context, lease time.Duration) (abilityreview.Input, error) {
	token := uuid.New()
	var input abilityreview.Input
	var aliases, nearest []byte
	err := r.database.QueryRowContext(ctx, `WITH candidate AS (
		SELECT id FROM ability_review_requests
		WHERE status IN ('queued','failed') AND attempts < max_attempts AND next_attempt_at <= NOW()
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE ability_review_requests request
	SET status='running', attempts=request.attempts+1, lease_token=$1, heartbeat_at=NOW(),
	    lease_expires_at=NOW()+($2*INTERVAL '1 millisecond'), updated_at=NOW()
	FROM candidate WHERE request.id=candidate.id
	RETURNING request.id, request.initiated_by_user_id, request.lease_token, request.attempts,
	 request.max_attempts, request.candidate_key, request.normalized_name, request.proposed_name,
	 request.proposed_category_code, request.proposed_aliases, request.proposed_definition,
	 request.application_reason, request.nearest_candidate_codes,request.review_type,
	 COALESCE((SELECT code FROM abilities WHERE id=request.target_ability_id),'')`, token, lease.Milliseconds()).Scan(
		&input.ID, &input.UserID, &input.LeaseToken, &input.Attempts, &input.MaxAttempts,
		&input.CandidateKey, &input.NormalizedName, &input.Name, &input.CategoryCode,
		&aliases, &input.Definition, &input.ApplicationReason, &nearest, &input.ReviewType, &input.TargetAbilityCode)
	if errors.Is(err, sql.ErrNoRows) {
		return input, abilityreview.ErrNoRequest
	}
	if err != nil {
		return input, fmt.Errorf("claim ability review: %w", err)
	}
	_ = json.Unmarshal(aliases, &input.Aliases)
	_ = json.Unmarshal(nearest, &input.NearestCandidateCodes)
	if input.ReviewType == "alias" {
		input.Evidence, err = r.loadAliasEvidence(ctx, input.ID)
	} else {
		input.Evidence, err = r.loadEvidence(ctx, input.ID)
	}
	if err != nil {
		return input, err
	}
	input.Catalog, err = r.loadAbilityCatalog(ctx)
	return input, err
}

func (r *AbilityReviewRepository) loadEvidence(ctx context.Context, requestID uuid.UUID) ([]string, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT DISTINCT option.evidence
		FROM job_description_ability_requirement_options option
		WHERE option.review_request_id=$1 ORDER BY option.evidence LIMIT 3`, requestID)
	if err != nil {
		return nil, fmt.Errorf("load review evidence: %w", err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *AbilityReviewRepository) loadAbilityCatalog(ctx context.Context) ([]abilityreview.CatalogAbility, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT ability.code, ability.name, category.code, category.name,
		ability.aliases, COALESCE(ability.definition,''),
		COALESCE(l2.description,''), COALESCE(l3.description,'')
		FROM abilities ability JOIN ability_categories category ON category.id=ability.category_id
		LEFT JOIN ability_levels l2 ON l2.ability_id=ability.id AND l2.level=2
		LEFT JOIN ability_levels l3 ON l3.ability_id=ability.id AND l3.level=3
		WHERE ability.is_active ORDER BY category.sort_order, ability.sort_order`)
	if err != nil {
		return nil, fmt.Errorf("load review catalog: %w", err)
	}
	defer rows.Close()
	result := []abilityreview.CatalogAbility{}
	for rows.Next() {
		var item abilityreview.CatalogAbility
		var aliases []byte
		if err := rows.Scan(&item.Code, &item.Name, &item.CategoryCode, &item.CategoryName, &aliases, &item.Definition, &item.Level2, &item.Level3); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(aliases, &item.Aliases)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *AbilityReviewRepository) Heartbeat(ctx context.Context, input abilityreview.Input, lease time.Duration) error {
	result, err := r.database.ExecContext(ctx, `UPDATE ability_review_requests SET heartbeat_at=NOW(),
		lease_expires_at=NOW()+($3*INTERVAL '1 millisecond'),updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, input.ID, input.LeaseToken, lease.Milliseconds())
	if err != nil {
		return err
	}
	return requireReviewLease(result)
}

func (r *AbilityReviewRepository) ResolveWithoutModel(ctx context.Context, input abilityreview.Input) (bool, error) {
	if input.ReviewType == "alias" {
		return r.resolveAliasWithoutModel(ctx, input)
	}
	abilityID, ok, err := findMatchingAbility(ctx, r.database, input.NormalizedName)
	if err != nil || !ok {
		return false, err
	}
	result := abilityreview.Result{Decision: "reuse_existing", Reason: "能力目录中已存在相同名称或别名。", PromptVersion: abilityreview.PromptVersion}
	var code string
	if err := r.database.QueryRowContext(ctx, `SELECT code FROM abilities WHERE id=$1`, abilityID).Scan(&code); err != nil {
		return false, err
	}
	result.ExistingAbilityCode = code
	return true, r.Complete(ctx, input, result, uuid.Nil)
}

func (r *AbilityReviewRepository) ReserveUsage(ctx context.Context, input abilityreview.Input, model string) (uuid.UUID, time.Time, error) {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return uuid.Nil, time.Time{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(741903)`); err != nil {
		return uuid.Nil, time.Time{}, err
	}
	var userCount, globalCount int
	query := `SELECT COUNT(*) FILTER (WHERE user_id=$1), COUNT(*) FROM platform_model_usage
		WHERE purpose IN ('ability_review','ability_alias_review') AND created_at >= date_trunc('day', NOW() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai'`
	if err = tx.QueryRowContext(ctx, query, input.UserID).Scan(&userCount, &globalCount); err != nil {
		return uuid.Nil, time.Time{}, err
	}
	if quotaReached(userCount, r.quota.UserDaily) || quotaReached(globalCount, r.quota.GlobalDaily) {
		var next time.Time
		if err = tx.QueryRowContext(ctx, `SELECT (date_trunc('day', NOW() AT TIME ZONE 'Asia/Shanghai') + INTERVAL '1 day' + INTERVAL '30 seconds') AT TIME ZONE 'Asia/Shanghai'`).Scan(&next); err != nil {
			next = time.Now().Add(24 * time.Hour)
		}
		_, err = tx.ExecContext(ctx, `UPDATE ability_review_requests SET status='queued', attempts=GREATEST(attempts-1,0),
			next_attempt_at=$3,lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='quota_exceeded',updated_at=NOW()
			WHERE id=$1 AND status='running' AND lease_token=$2`, input.ID, input.LeaseToken, next)
		if err != nil {
			return uuid.Nil, time.Time{}, err
		}
		if err = tx.Commit(); err != nil {
			return uuid.Nil, time.Time{}, err
		}
		return uuid.Nil, next, abilityreview.ErrQuota
	}
	usageID := uuid.New()
	purpose := "ability_review"
	if input.ReviewType == "alias" {
		purpose = "ability_alias_review"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_model_usage(id,request_id,user_id,purpose,provider,model,status)
		VALUES($1,$2,$3,$5,'deepseek',$4,'dispatched')`, usageID, input.ID, input.UserID, model, purpose)
	if err != nil {
		return uuid.Nil, time.Time{}, err
	}
	return usageID, time.Time{}, tx.Commit()
}

func quotaReached(used, limit int) bool {
	return limit > 0 && used >= limit
}

func (r *AbilityReviewRepository) Complete(ctx context.Context, input abilityreview.Input, result abilityreview.Result, usageID uuid.UUID) error {
	if input.ReviewType == "alias" {
		return r.completeAliasReview(ctx, input, result, usageID)
	}
	if err := validateReviewResult(result, input.Catalog); err != nil {
		return r.Fail(ctx, input, usageID, "model_invalid_response", input.Attempts < input.MaxAttempts)
	}
	tx, affectedJDs, err := r.beginReviewMutation(ctx, input.ID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var leaseValid bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ability_review_requests WHERE id=$1 AND status='running' AND lease_token=$2 FOR UPDATE)`, input.ID, input.LeaseToken).Scan(&leaseValid); err != nil {
		return err
	}
	if !leaseValid {
		return abilityreview.ErrLeaseLost
	}
	if err := enqueueAbilityGradingForReview(ctx, tx, input.ID); err != nil {
		return err
	}
	var abilityID uuid.UUID
	switch result.Decision {
	case "reuse_existing":
		err = tx.QueryRowContext(ctx, `SELECT id FROM abilities WHERE code=$1 AND is_active`, result.ExistingAbilityCode).Scan(&abilityID)
	case "approve_new":
		if _, lockErr := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(741904)`); lockErr != nil {
			return lockErr
		}
		// Only the approved canonical name identifies the ability. A proposed
		// alias belonging to another ability must not merge these two abilities.
		found, ok, findErr := findMatchingAbility(ctx, tx, NormalizeAbilityName(result.NewAbility.Name))
		if findErr != nil {
			return findErr
		}
		if ok {
			abilityID = found
			result.Decision = "reuse_existing"
		}
		if abilityID == uuid.Nil {
			aliasesValue, aliasErr := nonConflictingAbilityAliases(ctx, tx, result.NewAbility.Name, result.NewAbility.Aliases)
			if aliasErr != nil {
				return aliasErr
			}
			aliases, _ := json.Marshal(aliasesValue)
			code := "ability-dyn-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			newAbilityID := uuid.New()
			err = tx.QueryRowContext(ctx, `INSERT INTO abilities(id,category_id,code,name,aliases,definition,normalized_name,source,sort_order)
				SELECT $1,category.id,$2,$3,$4,$5,$6,'dynamic_review',COALESCE(MAX(existing.sort_order),0)+1
				FROM ability_categories category LEFT JOIN abilities existing ON existing.category_id=category.id
				WHERE category.code=$7 GROUP BY category.id RETURNING id`, newAbilityID, code, result.NewAbility.Name, aliases, result.NewAbility.Definition, NormalizeAbilityName(result.NewAbility.Name), result.NewAbility.CategoryCode).Scan(&abilityID)
			if err == nil {
				for _, level := range result.NewAbility.Levels {
					if _, err = tx.ExecContext(ctx, `INSERT INTO ability_levels(ability_id,level,description) VALUES($1,$2,$3)`, abilityID, level.Level, level.Description); err != nil {
						break
					}
				}
			}
		}
	case "reject":
		_, err = tx.ExecContext(ctx, `DELETE FROM job_description_ability_requirement_options WHERE review_request_id=$1 AND ability_id IS NULL`, input.ID)
		if err == nil {
			err = cleanupRequirementGroups(ctx, tx, affectedJDs...)
		}
	}
	if err != nil {
		return fmt.Errorf("apply ability review: %w", err)
	}
	if result.Decision != "reject" {
		_, err = tx.ExecContext(ctx, `UPDATE job_description_ability_requirement_options SET ability_id=$2,resolution_status='resolved',candidate_metadata='{}'::jsonb WHERE review_request_id=$1 AND ability_id IS NULL`, input.ID, abilityID)
		if err == nil {
			err = rebuildResolvedMentions(ctx, tx, input.ID)
		}
		if err != nil {
			return err
		}
		if result.Decision == "reuse_existing" {
			if err := enqueueAliasesForResolvedReview(ctx, tx, input.ID, abilityID, result.Reason); err != nil {
				return err
			}
		}
	}
	var resolved any = nil
	if abilityID != uuid.Nil {
		resolved = abilityID
	}
	_, err = tx.ExecContext(ctx, `UPDATE ability_review_requests SET status='succeeded',decision=$3::varchar,resolved_ability_id=$4,
		decision_reason=$5,provider=$6,model=$7,prompt_version=$8,decided_at=NOW(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error=NULL,
		proposed_aliases=CASE WHEN $3::varchar='reject' THEN '[]'::jsonb ELSE proposed_aliases END,
		proposed_name=CASE WHEN $3::varchar='reject' THEN normalized_name ELSE proposed_name END,
		proposed_category_code=CASE WHEN $3::varchar='reject' THEN '' ELSE proposed_category_code END,
		proposed_definition=CASE WHEN $3::varchar='reject' THEN '' ELSE proposed_definition END,
		application_reason=CASE WHEN $3::varchar='reject' THEN '' ELSE application_reason END,
		nearest_candidate_codes=CASE WHEN $3::varchar='reject' THEN '[]'::jsonb ELSE nearest_candidate_codes END,updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, input.ID, input.LeaseToken, result.Decision, resolved, result.Reason, result.Provider, result.Model, result.PromptVersion)
	if err != nil {
		return err
	}
	if usageID != uuid.Nil {
		_, err = tx.ExecContext(ctx, `UPDATE platform_model_usage SET status='succeeded',provider_request_id=$2,input_tokens=$3,output_tokens=$4,updated_at=NOW() WHERE id=$1`, usageID, result.ProviderRequestID, result.InputTokens, result.OutputTokens)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *AbilityReviewRepository) Fail(ctx context.Context, input abilityreview.Input, usageID uuid.UUID, code string, retryable bool) error {
	tx, _, err := r.beginReviewMutation(ctx, input.ID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	final := !retryable || input.Attempts >= input.MaxAttempts
	status := "failed"
	next := time.Now().Add(time.Duration(10*(1<<max(input.Attempts-1, 0))) * time.Second)
	attempts := input.Attempts
	if final {
		attempts = input.MaxAttempts
	}
	result, err := tx.ExecContext(ctx, `UPDATE ability_review_requests SET status=$3,attempts=$6,next_attempt_at=$4,last_error=$5,
		lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2`, input.ID, input.LeaseToken, status, next, code, attempts)
	if err != nil {
		return err
	}
	if err = requireReviewLease(result); err != nil {
		return err
	}
	if final {
		if _, err = tx.ExecContext(ctx, `UPDATE job_description_ability_requirement_options SET resolution_status='review_failed' WHERE review_request_id=$1`, input.ID); err != nil {
			return err
		}
	}
	if usageID != uuid.Nil {
		if _, err = tx.ExecContext(ctx, `UPDATE platform_model_usage SET status='failed',error_code=$2,updated_at=NOW() WHERE id=$1`, usageID, code); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Called under the catalog transaction lock, so another review cannot claim
// an alias between this check and the new ability's insertion.
func nonConflictingAbilityAliases(ctx context.Context, q queryer, name string, aliases []string) ([]string, error) {
	kept := make([]string, 0, len(aliases))
	seen := map[string]bool{NormalizeAbilityName(name): true}
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		normalized := NormalizeAbilityName(alias)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		_, conflict, err := findMatchingAbility(ctx, q, normalized)
		if err != nil {
			return nil, err
		}
		if !conflict {
			kept = append(kept, alias)
		}
	}
	return kept, nil
}

func findMatchingAbility(ctx context.Context, q queryer, normalized string) (uuid.UUID, bool, error) {
	rows := q.QueryRowContext(ctx, `SELECT id FROM abilities WHERE is_active AND (normalize_ability_name(name)=$1 OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(aliases)='array' THEN aliases ELSE '[]'::jsonb END) alias WHERE normalize_ability_name(alias)=$1)) LIMIT 1`, normalized)
	var id uuid.UUID
	err := rows.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return id, false, nil
	}
	return id, err == nil, err
}
func validateReviewResult(result abilityreview.Result, catalog []abilityreview.CatalogAbility) error {
	if strings.TrimSpace(result.Reason) == "" {
		return errors.New("missing reason")
	}
	switch result.Decision {
	case "reuse_existing":
		for _, a := range catalog {
			if a.Code == result.ExistingAbilityCode {
				return nil
			}
		}
		return errors.New("unknown ability")
	case "reject":
		return nil
	case "approve_new":
		if strings.TrimSpace(result.NewAbility.Name) == "" || len([]rune(result.NewAbility.Name)) > 150 || strings.TrimSpace(result.NewAbility.Definition) == "" || len([]rune(result.NewAbility.Definition)) > 1000 {
			return errors.New("incomplete ability")
		}
		if len(result.NewAbility.Aliases) > 20 {
			return errors.New("too many aliases")
		}
		for _, alias := range result.NewAbility.Aliases {
			if strings.TrimSpace(alias) == "" || len([]rune(alias)) > 150 {
				return errors.New("invalid alias")
			}
		}
		cats := map[string]bool{}
		for _, a := range catalog {
			cats[a.CategoryCode] = true
		}
		if !cats[result.NewAbility.CategoryCode] {
			return errors.New("unknown category")
		}
		if len(result.NewAbility.Levels) != 6 {
			return errors.New("six levels required")
		}
		seen := map[int]bool{}
		for _, l := range result.NewAbility.Levels {
			if l.Level < 0 || l.Level > 5 || seen[l.Level] || strings.TrimSpace(l.Description) == "" || len([]rune(l.Description)) > 500 {
				return errors.New("invalid levels")
			}
			seen[l.Level] = true
		}
		return nil
	default:
		return errors.New("unknown decision")
	}
}
func requireReviewLease(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return abilityreview.ErrLeaseLost
	}
	return nil
}

func enqueueAbilityGradingForReview(ctx context.Context, tx *sql.Tx, requestID uuid.UUID) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO jd_ability_level_jobs(user_id,target_id,job_description_id,status,attempts,next_attempt_at)
		SELECT DISTINCT jd.user_id,jd.target_id,jd.id,'queued',0,NOW()
		FROM job_description_ability_requirement_options option
		JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
		JOIN job_descriptions jd ON jd.id=requirement.job_description_id
		WHERE option.review_request_id=$1 AND jd.status='included' AND jd.validation_status='valid'
		ON CONFLICT(job_description_id) DO UPDATE SET status='queued',attempts=0,next_attempt_at=NOW(),
		lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error=NULL,completed_at=NULL,updated_at=NOW()`, requestID)
	return err
}

func cleanupRequirementGroups(ctx context.Context, tx *sql.Tx, jdIDs ...uuid.UUID) error {
	if len(jdIDs) == 0 {
		return nil
	}
	ids, err := json.Marshal(jdIDs)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM job_description_ability_requirements requirement WHERE job_description_id IN (SELECT value::uuid FROM jsonb_array_elements_text($1::jsonb) value) AND NOT EXISTS(SELECT 1 FROM job_description_ability_requirement_options option WHERE option.requirement_id=requirement.id)`, ids)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE job_description_ability_requirements requirement SET operator='single',required_count=1 WHERE job_description_id IN (SELECT value::uuid FROM jsonb_array_elements_text($1::jsonb) value) AND operator='any_of' AND (SELECT COUNT(*) FROM job_description_ability_requirement_options option WHERE option.requirement_id=requirement.id)=1`, ids)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM job_description_ability_requirements requirement WHERE job_description_id IN (SELECT value::uuid FROM jsonb_array_elements_text($1::jsonb) value) AND operator='at_least_n' AND (SELECT COUNT(*) FROM job_description_ability_requirement_options option WHERE option.requirement_id=requirement.id)<required_count`, ids)
	return err
}
func rebuildResolvedMentions(ctx context.Context, tx *sql.Tx, requestID uuid.UUID) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO job_description_abilities(job_description_id,ability_id,raw_name,evidence,required_level)
		SELECT DISTINCT requirement.job_description_id,option.ability_id,option.raw_label,option.evidence,option.required_level
		FROM job_description_ability_requirement_options option JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
		WHERE option.review_request_id=$1 AND option.ability_id IS NOT NULL ON CONFLICT DO NOTHING`, requestID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE job_descriptions jd SET ability_mentions=source.value,updated_at=NOW() FROM (
		SELECT requirement.job_description_id,jsonb_agg(jsonb_build_object('name',ability.name,'catalog_code',ability.code,'qualifier',option.qualifier,'evidence',option.evidence,'required_level',option.required_level) ORDER BY requirement.sort_order,option.sort_order) value
		FROM job_description_ability_requirement_options option JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id JOIN abilities ability ON ability.id=option.ability_id
		WHERE requirement.job_description_id IN(SELECT DISTINCT requirement2.job_description_id FROM job_description_ability_requirement_options option2 JOIN job_description_ability_requirements requirement2 ON requirement2.id=option2.requirement_id WHERE option2.review_request_id=$1)
		GROUP BY requirement.job_description_id) source WHERE jd.id=source.job_description_id`, requestID)
	return err
}
