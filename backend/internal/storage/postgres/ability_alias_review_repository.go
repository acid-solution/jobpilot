package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/LeoninCS/jobpilot-next/backend/internal/abilityreview"
	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/google/uuid"
)

type aliasReviewSource struct {
	JDID, MaterialID        uuid.UUID
	Label, Evidence, Reason string
}

func enqueueJDResultAliases(ctx context.Context, tx *sql.Tx, userID, jdID uuid.UUID, result jdanalysis.Result) error {
	proposals := append([]jdanalysis.AbilityAliasProposal(nil), result.AliasProposals...)
	for _, requirement := range result.AbilityRequirements {
		for _, option := range requirement.Options {
			if option.CatalogCode != "" && option.NormalizationReason != "" {
				proposals = append(proposals, jdanalysis.AbilityAliasProposal{CatalogCode: option.CatalogCode, Label: option.RawLabel, Evidence: option.Evidence, Reason: option.NormalizationReason})
			}
		}
	}
	for _, p := range proposals {
		var abilityID uuid.UUID
		err := tx.QueryRowContext(ctx, `SELECT ability.id FROM abilities ability WHERE code=$1 AND is_active
			AND EXISTS(SELECT 1 FROM job_description_ability_requirement_options option JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
			WHERE requirement.job_description_id=$2 AND option.ability_id=ability.id)`, p.CatalogCode, jdID).Scan(&abilityID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err = enqueueAliasReview(ctx, tx, userID, abilityID, aliasReviewSource{JDID: jdID, Label: p.Label, Evidence: p.Evidence, Reason: p.Reason}); err != nil {
			return err
		}
	}
	return nil
}

// This is called inside the business result transaction. Its failure rolls
// back the enqueue along with the result, never leaving a partial association.
func enqueueAliasReview(ctx context.Context, tx *sql.Tx, userID, abilityID uuid.UUID, source aliasReviewSource) error {
	label, reason := strings.TrimSpace(source.Label), strings.TrimSpace(source.Reason)
	normalized := NormalizeAbilityName(label)
	if normalized == "" || len([]rune(label)) > 150 || reason == "" || !strings.Contains(source.Evidence, label) {
		return nil
	}
	if _, known, err := findMatchingAbility(ctx, tx, normalized); err != nil || known {
		return err
	}
	var exists bool
	// Explicit revocation must not be undone by the next JD containing the same
	// wording. Changing this policy requires an explicit administrative action.
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reviewed_ability_aliases WHERE normalized_name=$1 AND ability_id=$2 AND NOT is_active)`, normalized, abilityID).Scan(&exists); err != nil || exists {
		return err
	}
	key := "alias:" + abilityID.String() + ":" + normalized
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ability_review_requests
		WHERE candidate_key=$1 AND status='succeeded' AND decision='reject_alias'
		AND evidence_hashes @> jsonb_build_array(to_jsonb(md5($2))))`, key, source.Evidence).Scan(&exists); err != nil || exists {
		return err
	}
	var requestID uuid.UUID
	err := tx.QueryRowContext(ctx, `INSERT INTO ability_review_requests(candidate_key,normalized_name,proposed_name,
		proposed_category_code,application_reason,evidence_hashes,initiated_by_user_id,review_type,target_ability_id)
		SELECT $1,$2,$3,category.code,$4,jsonb_build_array(to_jsonb(md5($5))),$6,'alias',ability.id
		FROM abilities ability JOIN ability_categories category ON category.id=ability.category_id WHERE ability.id=$7 AND ability.is_active
		ON CONFLICT (candidate_key) WHERE status IN ('queued','running','failed') DO UPDATE SET
		evidence_hashes=(SELECT jsonb_agg(DISTINCT value) FROM jsonb_array_elements(ability_review_requests.evidence_hashes || EXCLUDED.evidence_hashes)),updated_at=NOW()
		RETURNING id`, key, normalized, label, reason, source.Evidence, userID, abilityID).Scan(&requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("enqueue alias review: %w", err)
	}
	if source.JDID != uuid.Nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO ability_alias_review_sources(review_request_id,job_description_id,evidence,normalization_reason,source_hash)
			SELECT $1,id,$3,$4,encode(digest(raw_text,'sha256'),'hex') FROM job_descriptions WHERE id=$2 AND user_id=$5
			ON CONFLICT DO NOTHING`, requestID, source.JDID, source.Evidence, reason, userID)
	} else if source.MaterialID != uuid.Nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO ability_alias_review_sources(review_request_id,material_id,evidence,normalization_reason,source_hash)
			SELECT $1,id,$3,$4,encode(digest(source_text,'sha256'),'hex') FROM user_profile_materials WHERE id=$2 AND user_id=$5
			ON CONFLICT DO NOTHING`, requestID, source.MaterialID, source.Evidence, reason, userID)
	} else {
		return errors.New("alias review is missing a source")
	}
	return err
}

func enqueueAliasesForResolvedReview(ctx context.Context, tx *sql.Tx, requestID, abilityID uuid.UUID, reason string) error {
	rows, err := tx.QueryContext(ctx, `SELECT jd.user_id,jd.id,option.raw_label,option.evidence FROM job_description_ability_requirement_options option
		JOIN job_description_ability_requirements requirement ON requirement.id=option.requirement_id
		JOIN job_descriptions jd ON jd.id=requirement.job_description_id WHERE option.review_request_id=$1`, requestID)
	if err != nil {
		return err
	}
	type entry struct {
		userID uuid.UUID
		source aliasReviewSource
	}
	var entries []entry
	for rows.Next() {
		var e entry
		e.source.Reason = reason
		if err = rows.Scan(&e.userID, &e.source.JDID, &e.source.Label, &e.source.Evidence); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err = enqueueAliasReview(ctx, tx, e.userID, abilityID, e.source); err != nil {
			return err
		}
	}
	return nil
}

const aliasEvidenceQuery = `SELECT DISTINCT source.evidence FROM ability_alias_review_sources source
	JOIN ability_review_requests request ON request.id=source.review_request_id
	LEFT JOIN job_descriptions jd ON jd.id=source.job_description_id
	LEFT JOIN user_profile_materials material ON material.id=source.material_id
	WHERE source.review_request_id=$1 AND (
	(jd.status='included' AND jd.validation_status='valid' AND source.source_hash=encode(digest(jd.raw_text,'sha256'),'hex')
	 AND strpos(jd.raw_text,source.evidence)>0 AND EXISTS(SELECT 1 FROM job_description_ability_requirements requirement
	 JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
	 WHERE requirement.job_description_id=jd.id AND option.ability_id=request.target_ability_id
	))
	OR (material.status='ready' AND source.source_hash=encode(digest(material.source_text,'sha256'),'hex')
	 AND strpos(material.source_text,source.evidence)>0 AND EXISTS(SELECT 1 FROM user_profile_evidence evidence
	 WHERE evidence.material_id=material.id AND evidence.ability_id=request.target_ability_id
	 AND normalize_ability_name(evidence.raw_label)=request.normalized_name AND evidence.evidence_quote=source.evidence)))
	ORDER BY source.evidence LIMIT 3`

func (r *AbilityReviewRepository) loadAliasEvidence(ctx context.Context, id uuid.UUID) ([]string, error) {
	rows, err := r.database.QueryContext(ctx, aliasEvidenceQuery, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evidence []string
	for rows.Next() {
		var quote string
		if err := rows.Scan(&quote); err != nil {
			return nil, err
		}
		evidence = append(evidence, quote)
	}
	return evidence, rows.Err()
}

func (r *AbilityReviewRepository) resolveAliasWithoutModel(ctx context.Context, input abilityreview.Input) (bool, error) {
	var targetID uuid.UUID
	err := r.database.QueryRowContext(ctx, `SELECT id FROM abilities WHERE code=$1 AND is_active`, input.TargetAbilityCode).Scan(&targetID)
	result := abilityreview.Result{Decision: "reject_alias", Reason: "目标能力已停用或不存在。", PromptVersion: abilityreview.AliasPromptVersion}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	resolved := errors.Is(err, sql.ErrNoRows)
	if !resolved {
		found, known, err := findMatchingAbility(ctx, r.database, input.NormalizedName)
		if err != nil {
			return false, err
		}
		if known {
			resolved = true
			result.Reason = "该表述已属于另一项能力，不能新增冲突别名。"
			if found == targetID {
				result.Decision = "approve_alias"
				result.Reason = "目录中已经存在同一名称或别名。"
				result.ExistingAbilityCode = input.TargetAbilityCode
				result.ContextIndependent = true
			}
		} else if len(input.Evidence) == 0 {
			resolved = true
			result.Reason = "审核来源已修改、删除或不再有效。"
		}
	}
	if !resolved {
		return false, nil
	}
	return true, r.completeAliasReview(ctx, input, result, uuid.Nil)
}

func validateAliasReviewResult(input abilityreview.Input, result abilityreview.Result) error {
	if strings.TrimSpace(result.Reason) == "" {
		return errors.New("missing alias review reason")
	}
	switch result.Decision {
	case "reject_alias":
		return nil
	case "approve_alias":
		if !result.ContextIndependent || result.ExistingAbilityCode != input.TargetAbilityCode {
			return errors.New("alias equivalence not confirmed")
		}
		for _, a := range input.Catalog {
			if a.Code == input.TargetAbilityCode {
				return nil
			}
		}
		return errors.New("unknown alias target")
	default:
		return errors.New("invalid alias decision")
	}
}

func (r *AbilityReviewRepository) completeAliasReview(ctx context.Context, input abilityreview.Input, result abilityreview.Result, usageID uuid.UUID) error {
	if err := validateAliasReviewResult(input, result); err != nil {
		return r.Fail(ctx, input, usageID, "model_invalid_response", input.Attempts < input.MaxAttempts)
	}
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Alias publication doesn't change account data. Take the global catalog
	// lock first: ability creation may enqueue follow-up aliases while holding it.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(741904)`); err != nil {
		return err
	}
	var name string
	var abilityID uuid.UUID
	err = tx.QueryRowContext(ctx, `SELECT proposed_name,target_ability_id FROM ability_review_requests
		WHERE id=$1 AND review_type='alias' AND status='running' AND lease_token=$2 FOR UPDATE`, input.ID, input.LeaseToken).Scan(&name, &abilityID)
	if errors.Is(err, sql.ErrNoRows) {
		return abilityreview.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if result.Decision == "approve_alias" {
		var active, sourceValid, revoked bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM abilities WHERE id=$1 AND code=$2 AND is_active)`, abilityID, input.TargetAbilityCode).Scan(&active); err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(`+aliasEvidenceQuery+`)`, input.ID).Scan(&sourceValid); err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reviewed_ability_aliases WHERE ability_id=$1 AND normalized_name=$2 AND NOT is_active)`, abilityID, NormalizeAbilityName(name)).Scan(&revoked); err != nil {
			return err
		}
		found, known, err := findMatchingAbility(ctx, tx, NormalizeAbilityName(name))
		if err != nil {
			return err
		}
		switch {
		case !active || revoked:
			result.Decision = "reject_alias"
			result.Reason = "目标能力已停用或该别名已被撤销。"
		case known && found != abilityID:
			result.Decision = "reject_alias"
			result.Reason = "该表述已属于另一项能力，不能新增冲突别名。"
		case !known && !sourceValid:
			result.Decision = "reject_alias"
			result.Reason = "审核来源已修改、删除或不再有效。"
		case !known:
			if _, err = tx.ExecContext(ctx, `INSERT INTO reviewed_ability_aliases(ability_id,alias,normalized_name,review_request_id) VALUES($1,$2,$3,$4)`, abilityID, name, NormalizeAbilityName(name), input.ID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE abilities SET aliases=aliases || jsonb_build_array($2::text) WHERE id=$1`, abilityID, name); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE ability_review_requests SET status='succeeded',decision=$3,resolved_ability_id=$4,
		decision_reason=$5,provider=$6,model=$7,prompt_version=$8,decided_at=NOW(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error=NULL,updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, input.ID, input.LeaseToken, result.Decision, abilityID, result.Reason, result.Provider, result.Model, result.PromptVersion)
	if err != nil {
		return err
	}
	if usageID != uuid.Nil {
		_, err = tx.ExecContext(ctx, `UPDATE platform_model_usage SET status='succeeded',provider_request_id=$2,input_tokens=$3,output_tokens=$4,updated_at=NOW() WHERE id=$1 AND request_id=$5`, usageID, result.ProviderRequestID, result.InputTokens, result.OutputTokens, input.ID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RevokeReviewedAlias is an administrative repository operation; it doesn't
// change existing JD or profile associations and isn't exposed to user Agents.
func (r *AbilityReviewRepository) RevokeReviewedAlias(ctx context.Context, id uuid.UUID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("alias revocation reason is required")
	}
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(741904)`); err != nil {
		return err
	}
	var abilityID uuid.UUID
	var normalized string
	err = tx.QueryRowContext(ctx, `UPDATE reviewed_ability_aliases SET is_active=FALSE,revoked_at=NOW(),revocation_reason=$2 WHERE id=$1 AND is_active RETURNING ability_id,normalized_name`, id, reason).Scan(&abilityID, &normalized)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE abilities SET aliases=COALESCE((SELECT jsonb_agg(alias) FROM jsonb_array_elements_text(aliases) alias WHERE normalize_ability_name(alias)<>$2),'[]'::jsonb) WHERE id=$1`, abilityID, normalized)
	if err != nil {
		return err
	}
	return tx.Commit()
}
