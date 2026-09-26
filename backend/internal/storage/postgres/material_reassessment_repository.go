package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/LeoninCS/jobpilot-next/backend/internal/profile"
	"github.com/google/uuid"
)

type MaterialReassessmentRepository struct {
	database   *sql.DB
	profile    *ProfileRepository
	embeddings *EmbeddingRepository
}

func NewMaterialReassessmentRepository(db *sql.DB, p *ProfileRepository, e *EmbeddingRepository) *MaterialReassessmentRepository {
	return &MaterialReassessmentRepository{database: db, profile: p, embeddings: e}
}
func (r *MaterialReassessmentRepository) EmbeddingsReady(ctx context.Context) (bool, error) {
	return r.embeddings.EmbeddingsReady(ctx)
}
func (r *MaterialReassessmentRepository) RecoverReassessment(ctx context.Context) error {
	_, err := r.database.ExecContext(ctx, `UPDATE material_reassessment_jobs SET status=CASE WHEN attempts<max_attempts THEN 'queued' ELSE 'failed' END,
		next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='worker_interrupted',updated_at=NOW()
		WHERE status='running' AND lease_expires_at<NOW()`)
	return err
}
func (r *MaterialReassessmentRepository) ClaimReassessment(ctx context.Context) (profile.ReassessmentJob, error) {
	job := profile.ReassessmentJob{LeaseToken: uuid.New()}
	err := r.database.QueryRowContext(ctx, `WITH candidate AS (SELECT id FROM material_reassessment_jobs WHERE status='queued' AND attempts<max_attempts AND next_attempt_at<=NOW()
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE material_reassessment_jobs j SET status='running',attempts=j.attempts+1,lease_token=$1,heartbeat_at=NOW(),lease_expires_at=NOW()+INTERVAL '5 minutes',updated_at=NOW()
		FROM candidate WHERE j.id=candidate.id RETURNING j.id,j.user_id,j.material_id,j.source_hash`, job.LeaseToken).Scan(&job.ID, &job.UserID, &job.MaterialID, &job.SourceHash)
	if errors.Is(err, sql.ErrNoRows) {
		return job, profile.ErrNoReassessmentJob
	}
	return job, err
}
func (r *MaterialReassessmentRepository) HeartbeatReassessment(ctx context.Context, job profile.ReassessmentJob) error {
	result, err := r.database.ExecContext(ctx, `UPDATE material_reassessment_jobs SET heartbeat_at=NOW(),lease_expires_at=NOW()+INTERVAL '5 minutes',updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return profile.ErrReassessmentLeaseLost
	}
	return nil
}
func (r *MaterialReassessmentRepository) LoadReassessment(ctx context.Context, job profile.ReassessmentJob) (profile.Material, []profile.CapabilityInput, error) {
	material, err := r.profile.getMaterial(ctx, job.UserID, job.MaterialID)
	if err != nil {
		return material, nil, err
	}
	if material.Status != profile.MaterialReady || materialHash(material.Text) != job.SourceHash {
		return material, nil, profile.ErrReassessmentSourceChanged
	}
	inputs, err := r.profile.ListCapabilityInputs(ctx, job.UserID)
	return material, inputs, err
}
func materialHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (r *MaterialReassessmentRepository) CompleteReassessment(ctx context.Context, job profile.ReassessmentJob, drafts []profile.EvidenceDraft) error {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockUserMutationTx(ctx, tx, job.UserID); err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT TRUE FROM material_reassessment_jobs WHERE id=$1 AND status='running' AND lease_token=$2 FOR UPDATE`, job.ID, job.LeaseToken).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.ErrReassessmentLeaseLost
	}
	if err != nil {
		return err
	}
	var source, status string
	err = tx.QueryRowContext(ctx, `SELECT source_text,status FROM user_profile_materials WHERE id=$1 AND user_id=$2 FOR UPDATE`, job.MaterialID, job.UserID).Scan(&source, &status)
	if err != nil {
		return err
	}
	if status != "ready" || materialHash(source) != job.SourceHash {
		return profile.ErrReassessmentSourceChanged
	}
	rows, err := tx.QueryContext(ctx, `SELECT ability_id FROM user_profile_evidence WHERE material_id=$1`, job.MaterialID)
	if err != nil {
		return err
	}
	var affected []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		affected = append(affected, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM user_profile_evidence WHERE material_id=$1`, job.MaterialID); err != nil {
		return err
	}
	for _, draft := range drafts {
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_profile_evidence(material_id,ability_id,level,evidence_quote,reason,confidence,raw_label,normalization_reason)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, job.MaterialID, draft.AbilityID, draft.Level, draft.Quote, draft.Reason, draft.Confidence, draft.RawLabel, draft.MappingReason); err != nil {
			return err
		}
		if err = enqueueAliasReview(ctx, tx, job.UserID, draft.AbilityID, aliasReviewSource{MaterialID: job.MaterialID, Label: draft.RawLabel, Evidence: draft.Quote, Reason: draft.MappingReason}); err != nil {
			return err
		}
		affected = append(affected, draft.AbilityID)
	}
	if err = refreshEvidenceLevels(ctx, tx, job.UserID, affected); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE material_reassessment_jobs SET status='succeeded',lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='',updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return profile.ErrReassessmentLeaseLost
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit material reassessment: %w", err)
	}
	return nil
}
func (r *MaterialReassessmentRepository) FailReassessment(ctx context.Context, job profile.ReassessmentJob, code string) error {
	result, err := r.database.ExecContext(ctx, `UPDATE material_reassessment_jobs SET status=CASE WHEN $3='source_changed' OR attempts>=max_attempts THEN 'failed' ELSE 'queued' END,
		next_attempt_at=NOW()+((attempts*attempts*10)*INTERVAL '1 second'),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error=$3,updated_at=NOW()
		WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.LeaseToken, code)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return profile.ErrReassessmentLeaseLost
	}
	return nil
}
