package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/LeoninCS/jobpilot-next/backend/internal/jdanalysis"
	"github.com/google/uuid"
)

type JDNormalizationRepository struct {
	database   *sql.DB
	catalog    *AnalysisRepository
	embeddings *EmbeddingRepository
}

func NewJDNormalizationRepository(db *sql.DB, catalog *AnalysisRepository, embeddings *EmbeddingRepository) *JDNormalizationRepository {
	return &JDNormalizationRepository{database: db, catalog: catalog, embeddings: embeddings}
}
func (r *JDNormalizationRepository) EmbeddingsReady(ctx context.Context) (bool, error) {
	return r.embeddings.EmbeddingsReady(ctx)
}
func (r *JDNormalizationRepository) RecoverNormalization(ctx context.Context) error {
	_, err := r.database.ExecContext(ctx, `UPDATE jd_normalization_jobs SET
		status=CASE WHEN attempts<max_attempts THEN 'queued' ELSE 'failed' END,
		next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='worker_interrupted',updated_at=NOW()
		WHERE status='running' AND lease_expires_at<NOW()`)
	return err
}
func (r *JDNormalizationRepository) ClaimNormalization(ctx context.Context) (jdanalysis.NormalizationJob, error) {
	job := jdanalysis.NormalizationJob{LeaseToken: uuid.New()}
	err := r.database.QueryRowContext(ctx, `WITH candidate AS (
		SELECT job.id FROM jd_normalization_jobs job
		JOIN model_configs config ON config.user_id=job.user_id AND config.provider='deepseek'
		WHERE job.status='queued' AND job.attempts<job.max_attempts AND job.next_attempt_at<=NOW()
		ORDER BY job.created_at FOR UPDATE OF job SKIP LOCKED LIMIT 1)
		UPDATE jd_normalization_jobs j SET status='running',attempts=j.attempts+1,lease_token=$1,
		heartbeat_at=NOW(),lease_expires_at=NOW()+INTERVAL '5 minutes',updated_at=NOW()
		FROM candidate WHERE j.id=candidate.id
		RETURNING j.id,j.user_id,j.job_description_id,j.source_hash`, job.LeaseToken).
		Scan(&job.ID, &job.UserID, &job.JobDescriptionID, &job.SourceHash)
	if errors.Is(err, sql.ErrNoRows) {
		return job, jdanalysis.ErrNoNormalizationJob
	}
	return job, err
}
func (r *JDNormalizationRepository) HeartbeatNormalization(ctx context.Context, job jdanalysis.NormalizationJob) error {
	result, err := r.database.ExecContext(ctx, `UPDATE jd_normalization_jobs SET heartbeat_at=NOW(),lease_expires_at=NOW()+INTERVAL '5 minutes',updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return jdanalysis.ErrNormalizationLeaseLost
	}
	return nil
}
func (r *JDNormalizationRepository) LoadNormalization(ctx context.Context, job jdanalysis.NormalizationJob) (jdanalysis.Catalog, jdanalysis.Result, error) {
	catalog, err := r.catalog.Catalog(ctx)
	if err != nil {
		return catalog, jdanalysis.Result{}, err
	}
	var currentHash, validation string
	result := jdanalysis.Result{}
	err = r.database.QueryRowContext(ctx, `SELECT encode(digest(raw_text,'sha256'),'hex'),validation_status,COALESCE(title,''),COALESCE(company,''),COALESCE(employment_type,'')
		FROM job_descriptions WHERE id=$1 AND user_id=$2`, job.JobDescriptionID, job.UserID).
		Scan(&currentHash, &validation, &result.Title, &result.Company, &result.EmploymentType)
	if err != nil {
		return catalog, result, err
	}
	if currentHash != job.SourceHash || validation != "valid" {
		return catalog, result, jdanalysis.ErrNormalizationSourceChanged
	}
	rows, err := r.database.QueryContext(ctx, `SELECT requirement.id,requirement.operator,requirement.required_count,
		requirement.requirement_kind,requirement.evidence,
		option.id,option.raw_label,option.qualifier,option.evidence,option.required_level,
		COALESCE(ability.code,''),COALESCE(ability.name,''),option.candidate_metadata,
		COALESCE(option.ability_id,'00000000-0000-0000-0000-000000000000'::uuid),
		option.resolution_status,option.normalization_reason,
		COALESCE(option.review_request_id,'00000000-0000-0000-0000-000000000000'::uuid)
		FROM job_description_ability_requirements requirement
		JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
		LEFT JOIN abilities ability ON ability.id=option.ability_id
		WHERE requirement.job_description_id=$1 ORDER BY requirement.sort_order,option.sort_order`, job.JobDescriptionID)
	if err != nil {
		return catalog, result, err
	}
	defer rows.Close()
	indexes := map[uuid.UUID]int{}
	for rows.Next() {
		var requirement jdanalysis.AbilityRequirement
		var option jdanalysis.AbilityRequirementOption
		var level sql.NullInt64
		var metadata []byte
		if err = rows.Scan(&requirement.ExistingID, &requirement.Operator, &requirement.RequiredCount, &requirement.RequirementKind, &requirement.Evidence,
			&option.ExistingID, &option.RawLabel, &option.Qualifier, &option.Evidence, &level, &option.CatalogCode, &option.AbilityName, &metadata,
			&option.OriginalAbilityID, &option.OriginalResolution, &option.NormalizationReason, &option.OriginalReviewRequestID); err != nil {
			return catalog, result, err
		}
		if level.Valid {
			value := int(level.Int64)
			option.RequiredLevel = &value
		}
		if len(metadata) > 0 && string(metadata) != "null" {
			if err = json.Unmarshal(metadata, &option.Candidate); err != nil {
				return catalog, result, err
			}
		}
		result.OriginalOptions = append(result.OriginalOptions, jdanalysis.AbilityOptionSnapshot{
			ID: option.ExistingID, AbilityID: option.OriginalAbilityID,
			ReviewRequestID: option.OriginalReviewRequestID, Resolution: option.OriginalResolution,
		})
		index, exists := indexes[requirement.ExistingID]
		if !exists {
			index = len(result.AbilityRequirements)
			indexes[requirement.ExistingID] = index
			result.AbilityRequirements = append(result.AbilityRequirements, requirement)
		}
		result.AbilityRequirements[index].Options = append(result.AbilityRequirements[index].Options, option)
	}
	if err = rows.Err(); err != nil {
		return catalog, result, err
	}
	return catalog, result, nil
}
func (r *JDNormalizationRepository) CompleteNormalization(ctx context.Context, job jdanalysis.NormalizationJob, result jdanalysis.Result) error {
	if !result.VectorNormalized {
		return errors.New("normalization result was not produced by vector normalizer")
	}
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockUserMutationTx(ctx, tx, job.UserID); err != nil {
		return err
	}
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT TRUE FROM jd_normalization_jobs WHERE id=$1 AND status='running' AND lease_token=$2 FOR UPDATE`, job.ID, job.LeaseToken).Scan(&held)
	if errors.Is(err, sql.ErrNoRows) {
		return jdanalysis.ErrNormalizationLeaseLost
	}
	if err != nil {
		return err
	}
	var rawHash, validation, status string
	var targetID uuid.UUID
	err = tx.QueryRowContext(ctx, `SELECT encode(digest(raw_text,'sha256'),'hex'),validation_status,status,target_id
		FROM job_descriptions WHERE id=$1 AND user_id=$2 FOR UPDATE`, job.JobDescriptionID, job.UserID).Scan(&rawHash, &validation, &status, &targetID)
	if err != nil {
		return err
	}
	if rawHash != job.SourceHash || validation != "valid" {
		return jdanalysis.ErrNormalizationSourceChanged
	}
	// Lock every original option before any update or delete. The model may have
	// removed an option from its result; a completed review of that option must
	// still force a fresh normalization instead of being deleted here.
	optionRows, err := tx.QueryContext(ctx, `SELECT option.id,
		COALESCE(option.ability_id,'00000000-0000-0000-0000-000000000000'::uuid),
		COALESCE(option.review_request_id,'00000000-0000-0000-0000-000000000000'::uuid),
		option.resolution_status
		FROM job_description_ability_requirements requirement
		JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
		WHERE requirement.job_description_id=$1 FOR UPDATE OF option`, job.JobDescriptionID)
	if err != nil {
		return err
	}
	currentOptions := make(map[uuid.UUID]jdanalysis.AbilityOptionSnapshot)
	for optionRows.Next() {
		var item jdanalysis.AbilityOptionSnapshot
		if err = optionRows.Scan(&item.ID, &item.AbilityID, &item.ReviewRequestID, &item.Resolution); err != nil {
			optionRows.Close()
			return err
		}
		currentOptions[item.ID] = item
	}
	err = optionRows.Err()
	optionRows.Close()
	if err != nil {
		return err
	}
	if len(currentOptions) != len(result.OriginalOptions) {
		return jdanalysis.ErrNormalizationStateChanged
	}
	for _, original := range result.OriginalOptions {
		if current, ok := currentOptions[original.ID]; !ok || current != original {
			return jdanalysis.ErrNormalizationStateChanged
		}
	}
	keptRequirements := map[uuid.UUID]bool{}
	keptOptions := map[uuid.UUID]bool{}
	var pending []pendingAbilityOption
	for _, requirement := range result.AbilityRequirements {
		if requirement.ExistingID == uuid.Nil {
			return errors.New("normalization lost requirement identity")
		}
		keptRequirements[requirement.ExistingID] = true
		changed, err := tx.ExecContext(ctx, `UPDATE job_description_ability_requirements SET operator=$2,required_count=$3
			WHERE id=$1 AND job_description_id=$4`, requirement.ExistingID, requirement.Operator, requirement.RequiredCount, job.JobDescriptionID)
		if err != nil {
			return err
		}
		count, _ := changed.RowsAffected()
		if count != 1 {
			return jdanalysis.ErrNormalizationStateChanged
		}
		for _, option := range requirement.Options {
			if option.ExistingID == uuid.Nil {
				return errors.New("normalization lost option identity")
			}
			if option.CatalogCode != "" {
				var active bool
				if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM abilities WHERE code=$1 AND is_active)`, option.CatalogCode).Scan(&active); err != nil {
					return err
				}
				if !active {
					return errors.New("normalized ability no longer exists")
				}
			}
			keptOptions[option.ExistingID] = true
			metadata, _ := json.Marshal(option.Candidate)
			resolution := "resolved"
			if option.CatalogCode == "" {
				resolution = "pending_review"
			}
			changed, err = tx.ExecContext(ctx, `UPDATE job_description_ability_requirement_options SET
				ability_id=(SELECT id FROM abilities WHERE code=NULLIF($2,'') AND is_active),
				resolution_status=$3,candidate_metadata=$4,review_request_id=NULL,normalization_reason=$9
				WHERE id=$1 AND requirement_id=$5
				  AND ability_id IS NOT DISTINCT FROM NULLIF($6::uuid,'00000000-0000-0000-0000-000000000000'::uuid)
				  AND review_request_id IS NOT DISTINCT FROM NULLIF($7::uuid,'00000000-0000-0000-0000-000000000000'::uuid)
				  AND resolution_status=$8`, option.ExistingID, option.CatalogCode, resolution, metadata, requirement.ExistingID,
				option.OriginalAbilityID, option.OriginalReviewRequestID, option.OriginalResolution, option.NormalizationReason)
			if err != nil {
				return err
			}
			count, _ = changed.RowsAffected()
			if count != 1 {
				return jdanalysis.ErrNormalizationStateChanged
			}
			if resolution == "pending_review" && status == "included" {
				pending = append(pending, pendingAbilityOption{id: option.ExistingID, option: option})
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT requirement.id,option.id FROM job_description_ability_requirements requirement
		LEFT JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
		WHERE requirement.job_description_id=$1`, job.JobDescriptionID)
	if err != nil {
		return err
	}
	var removeOptions, removeRequirements []uuid.UUID
	for rows.Next() {
		var reqID uuid.UUID
		var optID uuid.NullUUID
		if err = rows.Scan(&reqID, &optID); err != nil {
			rows.Close()
			return err
		}
		if !keptRequirements[reqID] {
			removeRequirements = append(removeRequirements, reqID)
		}
		if optID.Valid && !keptOptions[optID.UUID] {
			removeOptions = append(removeOptions, optID.UUID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range removeOptions {
		if _, err = tx.ExecContext(ctx, `DELETE FROM job_description_ability_requirement_options WHERE id=$1`, id); err != nil {
			return err
		}
	}
	for _, id := range removeRequirements {
		if _, err = tx.ExecContext(ctx, `DELETE FROM job_description_ability_requirements WHERE id=$1`, id); err != nil {
			return err
		}
	}
	for _, item := range pending {
		if err = enqueueAbilityReview(ctx, tx, job.UserID, item); err != nil {
			return err
		}
	}
	if status == "included" {
		if err = enqueueJDResultAliases(ctx, tx, job.UserID, job.JobDescriptionID, result); err != nil {
			return err
		}
	}
	if err = cleanupRequirementGroups(ctx, tx, job.JobDescriptionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM job_description_abilities WHERE job_description_id=$1`, job.JobDescriptionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_description_abilities(job_description_id,ability_id,raw_name,evidence,required_level)
		SELECT DISTINCT $1::uuid,option.ability_id,option.raw_label,option.evidence,option.required_level
		FROM job_description_ability_requirements requirement
		JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
		WHERE requirement.job_description_id=$1 AND option.ability_id IS NOT NULL ON CONFLICT DO NOTHING`, job.JobDescriptionID); err != nil {
		return err
	}
	var mentions []byte
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('name',ability.name,'catalog_code',ability.code,
		'qualifier',option.qualifier,'evidence',option.evidence,'required_level',option.required_level)
		ORDER BY requirement.sort_order,option.sort_order),'[]'::jsonb)
		FROM job_description_ability_requirements requirement
		JOIN job_description_ability_requirement_options option ON option.requirement_id=requirement.id
		JOIN abilities ability ON ability.id=option.ability_id
		WHERE requirement.job_description_id=$1`, job.JobDescriptionID).Scan(&mentions)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE job_descriptions SET ability_mentions=$2,normalization_prompt_version=$3,updated_at=NOW() WHERE id=$1`,
		job.JobDescriptionID, mentions, jdanalysis.PromptVersion); err != nil {
		return err
	}
	if status == "included" {
		if err = enqueueJDAbilityGrading(ctx, tx, job.UserID, targetID, job.JobDescriptionID); err != nil {
			return err
		}
	}
	finished, err := tx.ExecContext(ctx, `UPDATE jd_normalization_jobs SET status='succeeded',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,
		last_error='',updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken)
	if err != nil {
		return err
	}
	count, _ := finished.RowsAffected()
	if count != 1 {
		return jdanalysis.ErrNormalizationLeaseLost
	}
	return tx.Commit()
}
func (r *JDNormalizationRepository) FailNormalization(ctx context.Context, job jdanalysis.NormalizationJob, code string) error {
	result, err := r.database.ExecContext(ctx, `UPDATE jd_normalization_jobs SET
		status=CASE WHEN $3 IN ('credential_unavailable','state_changed') THEN 'queued' WHEN $3='source_changed' OR attempts>=max_attempts THEN 'failed' ELSE 'queued' END,
		attempts=CASE WHEN $3 IN ('credential_unavailable','state_changed') THEN GREATEST(attempts-1,0) ELSE attempts END,
		next_attempt_at=CASE WHEN $3='credential_unavailable' THEN NOW()+INTERVAL '1 hour' WHEN $3='state_changed' THEN NOW()+INTERVAL '5 seconds' ELSE NOW()+((attempts*attempts*10)*INTERVAL '1 second') END,
		lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,
		last_error=$3,updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken, code)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return jdanalysis.ErrNormalizationLeaseLost
	}
	return nil
}

var _ jdanalysis.NormalizationRepository = (*JDNormalizationRepository)(nil)
